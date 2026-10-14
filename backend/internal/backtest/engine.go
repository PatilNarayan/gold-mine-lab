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

// Trade is one closed (or still-open) position record.
type Trade struct {
	Side       string     `json:"side"`
	EntryTime  time.Time  `json:"entryTime"`
	EntryPrice float64    `json:"entryPrice"`
	ExitTime   *time.Time `json:"exitTime,omitempty"`
	ExitPrice  *float64   `json:"exitPrice,omitempty"`
	Quantity   float64    `json:"quantity"`
	Pnl        *float64   `json:"pnl,omitempty"`
	Status     string     `json:"status"`
	Result     string     `json:"result"`
}

// EquityPoint is one bar of the equity curve.
type EquityPoint struct {
	Time     time.Time `json:"time"`
	Equity   float64   `json:"equity"`
	Drawdown float64   `json:"drawdown"` // fraction below running peak, negative or zero
}

// Result is what the engine produced.
type Result struct {
	Trades []Trade
	Equity []EquityPoint
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
				Quantity: qty, Status: "open", Result: "open"}
		} else if pending == strategy.ActionSell && pos != nil {
			fill := c.Open * (1 - in.Slippage/100)
			exitComm := pos.Quantity * fill * (in.Commission / 100)
			equity -= exitComm
			pnl := (fill - pos.EntryPrice) * pos.Quantity
			equity += pnl
			pos.ExitTime, pos.ExitPrice, pos.Pnl = &c.Time, &fill, &pnl
			pos.Status = "closed"
			pos.Result = "loss"
			if pnl > 0 {
				pos.Result = "win"
			}
			res.Trades = append(res.Trades, *pos)
			pos = nil
		}
		pending = strategy.ActionNeutral

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

		// Mark-to-market equity at this bar's close (window bars only).
		if i >= in.WindowStart {
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

	// Close any open position at the last bar's close for realized P&L.
	if pos != nil {
		last := in.Candles[len(in.Candles)-1]
		fill := last.Close * (1 - in.Slippage/100)
		exitComm := pos.Quantity * fill * (in.Commission / 100)
		equity -= exitComm
		pnl := (fill - pos.EntryPrice) * pos.Quantity
		pos.ExitTime, pos.ExitPrice, pos.Pnl = &last.Time, &fill, &pnl
		pos.Status = "closed"
		pos.Result = "loss"
		if pnl > 0 {
			pos.Result = "win"
		}
		res.Trades = append(res.Trades, *pos)
	}
	return res, nil
}
