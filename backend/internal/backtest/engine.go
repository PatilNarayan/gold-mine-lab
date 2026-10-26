// Package backtest contains the deterministic backtest engine.
//
// Execution model (documented and tested):
//   - Signals are generated at candle CLOSE on bar i.
//   - Fills execute at the NEXT candle's OPEN (bar i+1) — never the same
//     candle, so no look-ahead and no same-candle fills.
//   - V1 supports a single long position: BUY opens when flat, SELL closes
//     when long, NEUTRAL does nothing. No shorts, leverage, pyramiding,
//     partial exits, or trailing stops.
//   - Slippage is adverse: entry pays open*(1+slippage), exit receives
//     open*(1-slippage). Commission is a % of notional charged on each fill.
//   - positionSize is the % of current equity deployed per trade
//     (quantity = equity*positionSize/entryPrice).
//
// Accounting model (documented and reconciled):
//   - entryCommission = qty * entryFill * commission%   (cash out at entry)
//   - exitCommission  = qty * exitFill  * commission%   (cash out at exit)
//   - grossPnL        = (exitFill - entryFill) * qty    (slippage already in fills)
//   - netPnL          = grossPnL - entryCommission - exitCommission
//   - equity          = cash; entry charges entryCommission, exit credits
//     grossPnL - exitCommission, so equity_final = initial + Σ netPnL exactly.
//   - Trade win/loss is classified on netPnL — a trade whose gross is
//     positive but net is negative is a LOSS.
//   - The last open position is liquidated at the final bar's close with
//     adverse slippage + exit commission, and the final equity point is
//     post-liquidation — the curve's terminal value reconciles with netPnL.
//   - Drawdown is computed on the same mark-to-market series (unrealized
//     P&L included); an open position is valued at bar close.
//
// The engine is pure computation — no DB, no HTTP, deterministic.
package backtest

import (
	"context"
	"fmt"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// Input is one backtest run's full specification.
type Input struct {
	Strategy       strategy.Strategy
	Candles        []market.Candle // ascending; may include warm-up bars
	WindowStart    int             // signals are only acted on at indices >= WindowStart
	InitialCapital float64
	PositionSize   float64 // % of equity per trade (e.g. 5 = 5%)
	Commission     float64 // % of notional per fill (e.g. 0.02 = 0.02%)
	Slippage       float64 // % adverse price adjustment (e.g. 0.01)
}

// EngineVersion identifies the accounting model that produced a result —
// persisted on every run so historical experiments keep their provenance.
// "2.1" = net-P&L trades + post-liquidation terminal equity point.
const EngineVersion = "2.1.0"

// Trade is one closed (or still-open) position record.
// Pnl is NET (after both commissions); GrossPnl excludes commissions.
type Trade struct {
	Side            string     `json:"side"`
	EntryTime       time.Time  `json:"entryTime"`
	EntryPrice      float64    `json:"entryPrice"`
	ExitTime        *time.Time `json:"exitTime,omitempty"`
	ExitPrice       *float64   `json:"exitPrice,omitempty"`
	Quantity        float64    `json:"quantity"`
	GrossPnl        *float64   `json:"grossPnl,omitempty"`
	Pnl             *float64   `json:"pnl,omitempty"` // net of commissions
	EntryCommission float64    `json:"entryCommission"`
	ExitCommission  float64    `json:"exitCommission"`
	Status          string     `json:"status"`
	Result          string     `json:"result"` // classified on NET pnl
}

// EquityPoint is one bar of the equity curve.
type EquityPoint struct {
	Time     time.Time `json:"time"`
	Equity   float64   `json:"equity"`
	Drawdown float64   `json:"drawdown"` // fraction below running peak, negative or zero
}

// Result is what the engine produced.
// BarsInMarket / EligibleBars support bar-based time-in-market: a bar counts
// as in-market when a position was held DURING it — the entry bar (filled at
// its open) counts, the exit bar (closed at its open) does not, and the
// end-of-window liquidation bar counts. EligibleBars = evaluation-window
// bars only; warm-up bars never count either way.
type Result struct {
	Trades       []Trade
	Equity       []EquityPoint
	BarsInMarket int
	EligibleBars int
}

// Validate guards against nonsensical inputs before a run starts.
func (in Input) Validate() error {
	if in.Strategy == nil {
		return fmt.Errorf("strategy is required")
	}
	if len(in.Candles) == 0 {
		return fmt.Errorf("no candles")
	}
	if in.WindowStart < 0 || in.WindowStart >= len(in.Candles) {
		return fmt.Errorf("window start %d out of range %d", in.WindowStart, len(in.Candles))
	}
	if in.InitialCapital <= 0 {
		return fmt.Errorf("initial capital must be positive, got %v", in.InitialCapital)
	}
	if in.PositionSize <= 0 || in.PositionSize > 100 {
		return fmt.Errorf("position size must be in (0, 100], got %v", in.PositionSize)
	}
	if in.Commission < 0 || in.Commission > 10 {
		return fmt.Errorf("commission must be in [0, 10], got %v", in.Commission)
	}
	if in.Slippage < 0 || in.Slippage > 10 {
		return fmt.Errorf("slippage must be in [0, 10], got %v", in.Slippage)
	}
	for i := 1; i < len(in.Candles); i++ {
		if !in.Candles[i].Time.After(in.Candles[i-1].Time) {
			return fmt.Errorf("candles must be strictly ascending (idx %d: %s <= %s)",
				i, in.Candles[i].Time, in.Candles[i-1].Time)
		}
	}
	return nil
}

// Engine runs the backtest. Stateless; safe for concurrent use.
type Engine struct{}

func NewEngine() *Engine { return &Engine{} }

// Run processes candles chronologically. Strategy signals are computed on bar
// i's close and fill at bar i+1's open. The strategy is only ever shown
// candles up to the current bar — no future data reaches evaluation.
func (e *Engine) Run(ctx context.Context, in Input) (Result, error) {
	if err := in.Validate(); err != nil {
		return Result{}, fmt.Errorf("invalid backtest input: %w", err)
	}

	equity := in.InitialCapital
	peak := equity
	var pos *Trade
	res := Result{Equity: make([]EquityPoint, 0, len(in.Candles)-in.WindowStart)}

	// Pending signal from the previous bar's close, filled at this bar's open.
	var pending strategy.Action

	// Prefer O(1)-per-bar streaming when the strategy supports it.
	var stream strategy.Stream
	if st, ok := in.Strategy.(strategy.Streamer); ok {
		stream = st.NewStream()
	}

	for i := 0; i < len(in.Candles); i++ {
		c := in.Candles[i]
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}

		// Fill yesterday's signal at today's open.
		if pending == strategy.ActionBuy && pos == nil && i >= in.WindowStart {
			fill := c.Open * (1 + in.Slippage/100)
			qty := equity * (in.PositionSize / 100) / fill
			entryComm := qty * fill * (in.Commission / 100)
			equity -= entryComm
			pos = &Trade{Side: "BUY", EntryTime: c.Time, EntryPrice: fill,
				Quantity: qty, EntryCommission: entryComm, Status: "open", Result: "open"}
		} else if pending == strategy.ActionSell && pos != nil {
			exitPrice := c.Open * (1 - in.Slippage/100)
			*pos = closeTrade(*pos, c.Time, exitPrice, in.Commission)
			equity += (*pos.GrossPnl - pos.ExitCommission)
			res.Trades = append(res.Trades, *pos)
			pos = nil
		}
		pending = strategy.ActionNeutral

		// Position held during this bar: entry filled at the open counts,
		// an exit at the open does not (position already closed).
		inMarketThisBar := pos != nil

		// Evaluate this bar (close). Streaming evaluators see only this bar;
		// prefix evaluation sees candles[:i+1] — never the future.
		var sig strategy.Signal
		if stream != nil {
			sig = stream.Next(c)
		} else {
			s, err := in.Strategy.Evaluate(ctx, in.Candles[:i+1])
			if err != nil {
				return Result{}, fmt.Errorf("strategy evaluate at %s: %w", c.Time, err)
			}
			sig = s
		}
		if i >= in.WindowStart {
			pending = sig.Action
		}

		// Final bar: liquidate any open position at its close so the curve's
		// terminal point is post-liquidation realized equity — it reconciles
		// exactly with Σ netPnL.
		if i == len(in.Candles)-1 && pos != nil {
			exitPrice := c.Close * (1 - in.Slippage/100)
			*pos = closeTrade(*pos, c.Time, exitPrice, in.Commission)
			equity += (*pos.GrossPnl - pos.ExitCommission)
			res.Trades = append(res.Trades, *pos)
			pos = nil
		}

		// Mark-to-market equity at this bar's close (window bars only).
		if i >= in.WindowStart {
			res.EligibleBars++
			if inMarketThisBar {
				res.BarsInMarket++
			}
			mtm := equity
			if pos != nil {
				mtm += (c.Close - pos.EntryPrice) * pos.Quantity
			}
			if mtm > peak {
				peak = mtm
			}
			res.Equity = append(res.Equity, EquityPoint{
				Time: c.Time, Equity: mtm,
				Drawdown: (mtm - peak) / peak,
			})
		}
	}
	return res, nil
}

// closeTrade finalizes an open position at exitPrice. Gross P&L comes from
// the fill prices (slippage is already in them); net deducts both
// commissions. Result is classified on net — a gross win can be a net loss.
func closeTrade(t Trade, exitTime time.Time, exitPrice, commissionPct float64) Trade {
	gross := (exitPrice - t.EntryPrice) * t.Quantity
	exitComm := t.Quantity * exitPrice * (commissionPct / 100)
	net := gross - t.EntryCommission - exitComm
	t.ExitTime, t.ExitPrice = &exitTime, &exitPrice
	t.GrossPnl, t.Pnl = &gross, &net
	t.ExitCommission = exitComm
	t.Status = "closed"
	// Result is classified on net: a gross win can still be a net loss, and
	// an exact break-even is 'breakeven' — never silently lumped into 'loss'.
	t.Result = "breakeven"
	if net > 0 {
		t.Result = "win"
	} else if net < 0 {
		t.Result = "loss"
	}
	return t
}
