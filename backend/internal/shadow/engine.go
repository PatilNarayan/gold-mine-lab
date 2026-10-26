// Package shadow implements hypothetical (simulated) execution against
// real quote events. Signals come from the existing strategy streams on
// confirmed-closed candles only; fills execute at the FIRST qualifying
// quote after the decision — bid/ask, never mid. No broker, no orders,
// no real money.
//
// Timing model (documented, tested):
//   - A candle is evaluated only once it is CONFIRMED closed — a newer bar
//     has started. The live engine never sees an incomplete bar's close.
//   - Signal at confirmed-close(T) → order decision; fill at the first
//     quote with valid fresh bid/ask at provider_time > decision.
//   - Long entry fills at ASK; exit fills at BID. Mid is recorded for
//     comparison but never used as an executable price.
//   - Quotes missing bid/ask, crossed (ask<bid), or stale are rejected
//     as fill candidates — never substituted, never midpoint-filled.
//   - A pending order older than FillTimeout expires; nothing is forced.
//
// Idempotency: order_key = strategy@version|symbol|tf|bar_time|side is
// UNIQUE — replays after reconnect/restart/repeated-close collapse to one
// row. Position state + last processed bar persist per (strategy,symbol,tf)
// so restarts resume, not replay.
package shadow

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// Config is one shadow engine instance's frozen setup.
type Config struct {
	StrategyID      string
	StrategyVersion string
	Symbol          string
	Timeframe       market.Timeframe
	Provider        string
	InitialEquity   float64 // virtual equity for sizing (e.g. 100000)
	PositionSizePct float64 // % of running equity per position
	CommissionPct   float64 // % of notional per fill — an assumption, not measured
	FillTimeout     time.Duration
	QuoteMaxAge     time.Duration // received - provider_time bound
}

// Store is the persistence the engine needs — implemented by postgres.Store.
type Store interface {
	CreateOrderIfAbsent(ctx context.Context, o repository.ShadowOrderRow) (id int64, created bool, err error)
	GetPendingOrder(ctx context.Context, strategyID, version, symbol string, tf market.Timeframe) (*repository.ShadowOrderRow, error)
	SetOrderStatus(ctx context.Context, orderID int64, status, reason string) error
	UpsertPosition(ctx context.Context, p repository.ShadowPositionRow) error
	GetPosition(ctx context.Context, strategyID, version, symbol string, tf market.Timeframe) (*repository.ShadowPositionRow, error)
	ListOrders(ctx context.Context, strategyID, symbol string, tf market.Timeframe, limit int) ([]repository.ShadowOrderRow, error)
	// FillOrderAndPosition commits order-fill + position transition atomically.
	FillOrderAndPosition(ctx context.Context, orderID int64, f repository.ShadowFillRecord, pos repository.ShadowPositionRow) error
	// FindQuoteInWindow finds the earliest persisted admissible quote in
	// [from,to) — evidence for downtime recovery, never fabricated.
	FindQuoteInWindow(ctx context.Context, provider, symbol string, from, to time.Time) (*repository.QuoteObservationRow, error)
}

// EvidenceLookup supplies persisted quote observations for recovery replay.
type EvidenceLookup func(ctx context.Context, from, to time.Time) (*repository.QuoteObservationRow, error)

// Metrics are honest counters sourced from actual engine events.
type Metrics struct {
	BarsProcessed  int        `json:"barsProcessed"`
	SignalsSeen    int        `json:"signalsSeen"`
	QuotesSeen     int        `json:"quotesSeen"`
	QuotesRejected int        `json:"quotesRejected"`
	Fills          int        `json:"fills"`
	Expired        int        `json:"expired"`
	Missed         int        `json:"missed"`
	Recoveries     int        `json:"recoveries"`
	DupSkips       int        `json:"duplicateEventsSkipped"`
	LastQuoteAt    *time.Time `json:"lastQuoteAt,omitempty"`
	LastFillAt     *time.Time `json:"lastFillAt,omitempty"`
}

// Engine is one strategy/symbol/timeframe shadow executor. Deterministic;
// call OnBarClosed/OnQuote in event order.
type Engine struct {
	cfg    Config
	store  Store
	stream strategy.Stream
	log    *slog.Logger
	now    func() time.Time

	mu      struct{} // serialized by callers (single driver goroutine)
	pos     repository.ShadowPositionRow
	pending *repository.ShadowOrderRow
	equity  float64 // running virtual equity = initial + realized
	metrics Metrics
	session int64 // persisted session identity, 0 = unset
	loaded  bool
}

func New(cfg Config, store Store, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	if cfg.FillTimeout <= 0 {
		cfg.FillTimeout = 10 * time.Minute
	}
	if cfg.QuoteMaxAge <= 0 {
		cfg.QuoteMaxAge = 30 * time.Second
	}
	return &Engine{cfg: cfg, store: store, log: log,
		equity: cfg.InitialEquity, now: time.Now}
}

// SetClock overrides the wall clock — tests only.
func (e *Engine) SetClock(now func() time.Time) { e.now = now }

// SetStream installs the evaluator — the driver feeds it stored candles
// for warmup before live closes arrive.
func (e *Engine) SetStream(s strategy.Stream) { e.stream = s }

// TF reports the engine's configured timeframe.
func (e *Engine) TF() market.Timeframe { return e.cfg.Timeframe }

// SetSession stamps the persisted session identity on new orders/positions.
func (e *Engine) SetSession(id int64) { e.session = id }

func (e *Engine) tfDuration() time.Duration {
	switch e.cfg.Timeframe {
	case market.Timeframe4h:
		return 4 * time.Hour
	case market.Timeframe1d:
		return 24 * time.Hour
	default:
		return time.Hour
	}
}

func (e *Engine) orderKey(side string, barTime time.Time) string {
	return fmt.Sprintf("%s@%s|%s|%s|%s|%s",
		e.cfg.StrategyID, e.cfg.StrategyVersion, e.cfg.Symbol,
		e.cfg.Timeframe, barTime.UTC().Format(time.RFC3339Nano), side)
}

// Restore loads persisted position + pending order + last-bar marker so a
// restart resumes state instead of replaying decisions.
func (e *Engine) Restore(ctx context.Context) error {
	pos, err := e.store.GetPosition(ctx, e.cfg.StrategyID, e.cfg.StrategyVersion,
		e.cfg.Symbol, e.cfg.Timeframe)
	if err != nil {
		return err
	}
	if pos != nil {
		e.pos = *pos
		e.equity = e.cfg.InitialEquity + pos.RealizedPnl
	}
	pend, err := e.store.GetPendingOrder(ctx, e.cfg.StrategyID, e.cfg.StrategyVersion,
		e.cfg.Symbol, e.cfg.Timeframe)
	if err != nil {
		return err
	}
	e.pending = pend
	e.loaded = true
	return nil
}

// OnBarClosed processes one CONFIRMED-closed candle. Bars at/before the
// last processed bar are ignored — replay-safe. Signals BUY/SELL create a
// pending order (idempotent via order_key) unless the position already
// matches the intent.
func (e *Engine) OnBarClosed(ctx context.Context, c market.Candle) error {
	if e.pos.LastBarTime != nil && !c.Time.After(*e.pos.LastBarTime) {
		e.metrics.DupSkips++
		return nil // already processed — reconnect/replay duplicate
	}
	e.pos.LastBarTime = &c.Time
	e.metrics.BarsProcessed++
	if e.stream == nil {
		return fmt.Errorf("shadow engine has no strategy stream")
	}
	sig := e.stream.Next(c)
	var side string
	e.metrics.SignalsSeen++
	switch sig.Action {
	case strategy.ActionBuy:
		if e.pos.Side != nil {
			return e.persistBar(ctx) // already long — no decision to make
		}
		side = "BUY"
	case strategy.ActionSell:
		if e.pos.Side == nil {
			return e.persistBar(ctx) // flat — nothing to close
		}
		side = "SELL"
	default:
		return e.persistBar(ctx) // neutral — just advance the marker
	}
	// One pending order at a time: a live pending order stays until filled
	// or expired; the new signal is recorded in the bar marker regardless.
	if e.pending != nil {
		return e.persistBar(ctx)
	}
	o := repository.ShadowOrderRow{
		OrderKey: e.orderKey(side, c.Time), StrategyID: e.cfg.StrategyID,
		StrategyVersion: e.cfg.StrategyVersion, Symbol: e.cfg.Symbol,
		Timeframe: string(e.cfg.Timeframe), Side: side,
		SignalBarTime: c.Time, DecisionAt: e.now().UTC(), Status: "pending",
	}
	if e.session != 0 {
		o.SessionID = &e.session
	}
	id, created, err := e.store.CreateOrderIfAbsent(ctx, o)
	if err != nil {
		return err
	}
	if created {
		o.ID = id
		e.pending = &o
		e.log.Info("shadow decision", "side", side, "bar", c.Time, "order", id)
	} else {
		e.metrics.DupSkips++
	}
	return e.persistBar(ctx)
}

// MarkBarProcessed advances the resume marker WITHOUT evaluating a signal —
// used by the driver to warm up on stored history at fresh start: bars that
// closed before the engine existed must not retroactively create decisions.
func (e *Engine) MarkBarProcessed(ctx context.Context, t time.Time) error {
	if e.pos.LastBarTime != nil && !t.After(*e.pos.LastBarTime) {
		return nil
	}
	e.pos.LastBarTime = &t
	return e.persistBar(ctx)
}

// ProcessMissedClose handles a candle that closed while the engine was
// offline — the recovery path. Semantics:
//
//   - The signal's decision anchor is the bar close (bar_time + duration),
//     NOT the recovery moment — recorded as decision_at, the earliest
//     legitimate execution instant.
//   - If the fill window is still open at recovery, the order stays
//     pending: only quotes with provider_time ≥ the anchor may fill it —
//     never "the first quote after restart" for an already-passed window.
//   - If the window passed, the engine looks for admissible persisted
//     quote evidence inside [anchor, anchor+timeout). Found → fill
//     replayed deterministically at that recorded quote. Missing → the
//     decision is recorded 'missed' — honest evidence absence, never a
//     fabricated or backdated fill.
//   - Order keys are idempotent: re-running recovery produces nothing new.
func (e *Engine) ProcessMissedClose(ctx context.Context, c market.Candle) error {
	if e.pos.LastBarTime != nil && !c.Time.After(*e.pos.LastBarTime) {
		e.metrics.DupSkips++
		return nil
	}
	e.pos.LastBarTime = &c.Time
	e.metrics.BarsProcessed++
	if e.stream == nil {
		return fmt.Errorf("shadow engine has no strategy stream")
	}
	sig := e.stream.Next(c)
	e.metrics.SignalsSeen++
	var side string
	switch sig.Action {
	case strategy.ActionBuy:
		if e.pos.Side != nil {
			return e.persistBar(ctx)
		}
		side = "BUY"
	case strategy.ActionSell:
		if e.pos.Side == nil {
			return e.persistBar(ctx)
		}
		side = "SELL"
	default:
		return e.persistBar(ctx)
	}
	if e.pending != nil {
		return e.persistBar(ctx)
	}

	anchor := c.Time.Add(e.tfDuration()) // bar close = signal becomes actionable
	o := repository.ShadowOrderRow{
		OrderKey: e.orderKey(side, c.Time), StrategyID: e.cfg.StrategyID,
		StrategyVersion: e.cfg.StrategyVersion, Symbol: e.cfg.Symbol,
		Timeframe: string(e.cfg.Timeframe), Side: side,
		SignalBarTime: c.Time, DecisionAt: anchor, Status: "pending",
	}
	if e.session != 0 {
		o.SessionID = &e.session
	}
	e.metrics.Recoveries++
	id, created, err := e.store.CreateOrderIfAbsent(ctx, o)
	if err != nil {
		return err
	}
	if !created {
		e.metrics.DupSkips++
		return e.persistBar(ctx) // already recovered — replay produces nothing
	}
	o.ID = id

	windowEnd := anchor.Add(e.cfg.FillTimeout)
	if e.now().Before(windowEnd) {
		// Window still open: a live quote AFTER the anchor may legitimately fill.
		e.pending = &o
		e.log.Info("shadow recovery: pending order resumed",
			"side", side, "bar", c.Time, "anchor", anchor)
		return e.persistBar(ctx)
	}

	// Window closed while offline — consult PERSISTED evidence only.
	obs, err := e.store.FindQuoteInWindow(ctx, e.cfg.Provider, e.cfg.Symbol, anchor, windowEnd)
	if err != nil {
		return err
	}
	if obs == nil {
		if err := e.store.SetOrderStatus(ctx, id, "missed",
			"execution window passed while engine offline; no admissible quote evidence recorded"); err != nil {
			return err
		}
		e.metrics.Missed++
		e.log.Info("shadow decision missed", "side", side, "bar", c.Time, "anchor", anchor)
		return e.persistBar(ctx)
	}
	ev := quoteEvidence{
		providerTime: *obs.ProviderTime, receivedAt: obs.ReceivedAt,
		bid: *obs.Bid, ask: *obs.Ask,
	}
	e.log.Info("shadow recovery fill replayed", "side", side, "bar", c.Time,
		"quote_at", obs.ProviderTime)
	e.pending = &o
	if err := e.fillOrder(ctx, &o, ev, obs.ReceivedAt); err != nil {
		return err
	}
	return e.persistBar(ctx)
}

// OnQuote offers a live quote to a pending order. A fill requires:
// qualifying bid/ask, fresh provider timestamp, and arrival after the
// decision. Returns true when a fill occurred.
func (e *Engine) OnQuote(ctx context.Context, q live.Quote) (bool, error) {
	e.metrics.QuotesSeen++
	qt := q.ReceivedAt
	e.metrics.LastQuoteAt = &qt
	if e.pending == nil {
		return false, nil
	}
	o := e.pending
	// Expiry: no qualifying quote within the window → expire, keep honest.
	if e.now().Sub(o.DecisionAt) > e.cfg.FillTimeout {
		if err := e.store.SetOrderStatus(ctx, o.ID, "expired", "no qualifying quote within timeout"); err != nil {
			return false, err
		}
		e.pending = nil
		e.metrics.Expired++
		e.log.Info("shadow order expired", "order", o.ID)
		return false, nil
	}
	if !e.quoteUsable(q, o.DecisionAt) {
		e.metrics.QuotesRejected++
		return false, nil // wait for a qualifying quote — never invent one
	}
	// Long entry fills at ask; exit fills at bid. Mid recorded, never used.
	ev := quoteEvidence{
		providerTime: *q.ProviderTime, receivedAt: q.ReceivedAt,
		bid: *q.Bid, ask: *q.Ask,
	}
	return true, e.fillOrder(ctx, o, ev, e.now().UTC())
}

// quoteEvidence is the quote snapshot a fill is computed from — a live
// event or a persisted observation replayed during recovery.
type quoteEvidence struct {
	providerTime time.Time
	receivedAt   time.Time
	bid, ask     float64
}

// fillOrder computes fill/qty/commission, transitions the position, and
// commits order+position atomically. Single source of fill truth for both
// live quotes and recovery replays.
func (e *Engine) fillOrder(ctx context.Context, o *repository.ShadowOrderRow, ev quoteEvidence, filledAt time.Time) error {
	var fill float64
	if o.Side == "BUY" {
		fill = ev.ask
	} else {
		fill = ev.bid
	}
	mid := (ev.bid + ev.ask) / 2
	var spreadCost float64 // measured spread drag vs mid for this fill
	if o.Side == "BUY" {
		spreadCost = fill - mid
	} else {
		spreadCost = mid - fill
	}
	var qty float64
	if o.Side == "BUY" {
		qty = e.equity * e.cfg.PositionSizePct / 100 / fill
	} else {
		qty = e.pos.Quantity // close the whole position
	}
	comm := qty * fill * e.cfg.CommissionPct / 100
	fr := repository.ShadowFillRecord{
		QuoteProviderTime: &ev.providerTime, QuoteReceivedAt: &ev.receivedAt,
		QuoteBid: &ev.bid, QuoteAsk: &ev.ask, QuoteMid: &mid,
		FillPrice: fill, FilledAt: filledAt, Quantity: qty, Commission: comm,
	}

	pos := e.pos
	if o.Side == "BUY" {
		s := "BUY"
		pos.Side = &s
		pos.Quantity = qty
		pos.EntryPrice = &fill
		pos.EntryAt = &ev.receivedAt
		pos.EntryOrderID = &o.ID
		pos.EntryCommission = comm
		pos.RealizedComm += comm
		pos.RealizedSpread += spreadCost * qty
	} else {
		// Realize P&L: (bid-exit - ask-entry) * qty - both commissions.
		gross := (fill - *pos.EntryPrice) * pos.Quantity
		net := gross - pos.EntryCommission - comm
		pos.RealizedPnl += net
		pos.RealizedComm += comm
		pos.RealizedSpread += spreadCost * pos.Quantity
		e.equity += net
		pos.Side = nil
		pos.Quantity = 0
		pos.EntryPrice = nil
		pos.EntryAt = nil
		pos.EntryOrderID = nil
		pos.EntryCommission = 0
	}
	pos.StrategyID, pos.StrategyVersion = e.cfg.StrategyID, e.cfg.StrategyVersion
	pos.Symbol, pos.Timeframe = e.cfg.Symbol, string(e.cfg.Timeframe)

	if err := e.store.FillOrderAndPosition(ctx, o.ID, fr, pos); err != nil {
		return err
	}
	e.pos = pos
	e.metrics.Fills++
	ft := filledAt
	e.metrics.LastFillAt = &ft
	e.log.Info("shadow fill", "order", o.ID, "side", o.Side, "price", fill, "mid", mid)
	e.pending = nil
	return nil
}

// Tick lets the driver expire a stale pending order even when no quotes
// arrive — an order should not sit pending forever on a silent feed.
func (e *Engine) Tick(ctx context.Context) error {
	if e.pending != nil && e.now().Sub(e.pending.DecisionAt) > e.cfg.FillTimeout {
		o := e.pending
		if err := e.store.SetOrderStatus(ctx, o.ID, "expired", "no qualifying quote within timeout"); err != nil {
			return err
		}
		e.pending = nil
		e.metrics.Expired++
		e.log.Info("shadow order expired", "order", o.ID)
	}
	return nil
}

// quoteUsable enforces the fill-quality policy: valid legs, uncrossed,
// provider-time supplied and fresh, and event after the decision.
func (e *Engine) quoteUsable(q live.Quote, decisionAt time.Time) bool {
	if q.Bid == nil || q.Ask == nil || q.ProviderTime == nil {
		return false
	}
	b, a := *q.Bid, *q.Ask
	if b <= 0 || a < b || math.IsNaN(b) || math.IsNaN(a) {
		return false
	}
	if q.ReceivedAt.Sub(*q.ProviderTime) > e.cfg.QuoteMaxAge {
		return false
	}
	if q.ProviderTime.Before(decisionAt) {
		return false // quote predates the decision — future info is required
	}
	return true
}

func (e *Engine) persistBar(ctx context.Context) error {
	p := e.pos
	p.StrategyID, p.StrategyVersion = e.cfg.StrategyID, e.cfg.StrategyVersion
	p.Symbol, p.Timeframe = e.cfg.Symbol, string(e.cfg.Timeframe)
	if e.session != 0 {
		p.SessionID = &e.session
	}
	return e.store.UpsertPosition(ctx, p)
}

// Snapshot is the API-facing state — honest, no fabrication.
type Snapshot struct {
	StrategyID      string                        `json:"strategyId"`
	StrategyVersion string                        `json:"strategyVersion"`
	Symbol          string                        `json:"symbol"`
	Timeframe       string                        `json:"timeframe"`
	Mode            string                        `json:"mode"` // always "simulated"
	Position        *repository.ShadowPositionRow `json:"position,omitempty"`
	Pending         *repository.ShadowOrderRow    `json:"pendingOrder,omitempty"`
	RealizedPnl     float64                       `json:"realizedPnl"`
	RealizedComm    float64                       `json:"realizedCommissions"`
	RealizedSpread  float64                       `json:"realizedSpreadCost"`
	Equity          float64                       `json:"equity"`
	LastBarTime     *time.Time                    `json:"lastBarTime,omitempty"`
	SessionID       int64                         `json:"sessionId,omitempty"`
}

// MetricsSnapshot returns the engine's honest counters.
func (e *Engine) MetricsSnapshot() Metrics { return e.metrics }

func (e *Engine) Snapshot() Snapshot {
	var pos *repository.ShadowPositionRow
	if e.pos.StrategyID != "" || e.pos.Side != nil {
		p := e.pos
		pos = &p
	}
	return Snapshot{
		StrategyID: e.cfg.StrategyID, StrategyVersion: e.cfg.StrategyVersion,
		Symbol: e.cfg.Symbol, Timeframe: string(e.cfg.Timeframe),
		Mode: "simulated", Position: pos, Pending: e.pending,
		RealizedPnl: e.pos.RealizedPnl, RealizedComm: e.pos.RealizedComm,
		RealizedSpread: e.pos.RealizedSpread, Equity: e.equity,
		LastBarTime: e.pos.LastBarTime,
	}
}
