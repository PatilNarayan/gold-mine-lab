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
	GetPendingOrder(ctx context.Context, sessionID int64, strategyID, version, symbol string, tf market.Timeframe) (*repository.ShadowOrderRow, error)
	SetOrderStatus(ctx context.Context, orderID int64, status, reason string) error
	UpsertPosition(ctx context.Context, p repository.ShadowPositionRow) error
	GetPosition(ctx context.Context, sessionID int64, strategyID, version, symbol string, tf market.Timeframe) (*repository.ShadowPositionRow, error)
	ListOrders(ctx context.Context, strategyID, symbol string, tf market.Timeframe, limit int) ([]repository.ShadowOrderRow, error)
	// FillOrderAndPosition commits order-fill + position transition atomically.
	FillOrderAndPosition(ctx context.Context, orderID int64, f repository.ShadowFillRecord, pos repository.ShadowPositionRow) error
	// CommitBar commits ONE bar's complete durable transition in a single
	// transaction: the order intent (idempotent on order_key), the order's
	// optional fill or terminal resolution, the ledger decision (idempotent
	// on the session+identity+bar key), and the position/checkpoint upsert.
	// A failed commit writes NOTHING — the caller must leave its in-memory
	// state untouched so durable state stays authoritative on restart.
	CommitBar(ctx context.Context, c repository.ShadowBarCommit) (repository.ShadowBarResult, error)
	// FindQuoteInWindow finds the earliest persisted admissible quote in
	// [from,to) — evidence for downtime recovery, never fabricated.
	FindQuoteInWindow(ctx context.Context, provider, symbol string, from, to time.Time) (*repository.QuoteObservationRow, error)
	// CoverageComplete reports whether persisted recorder coverage PROVES
	// [from,to) was observed — separates confirmed-missed from unobservable.
	CoverageComplete(ctx context.Context, provider, symbol string, from, to time.Time) (bool, error)
	// ListDecisions returns recent ledger rows (newest first).
	ListDecisions(ctx context.Context, strategyID, version, symbol string, tf market.Timeframe, limit int) ([]repository.ShadowDecisionRow, error)
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
	Unobservable   int        `json:"unobservable"`
	Recoveries     int        `json:"recoveries"`
	DupSkips       int        `json:"duplicateEventsSkipped"`
	Decisions      int        `json:"decisionsEvaluated"`
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

	// Ownership: exactly one goroutine may call any Engine method — the
	// Runtime's owner. There is deliberately no mutex: the single-writer
	// contract is structural, and a second caller is a bug, not contention.
	pos      repository.ShadowPositionRow
	pending  *repository.ShadowOrderRow
	hasPos   bool    // a fill applied or a stored row restored — gates Snapshot().Position
	equity   float64 // running virtual equity = initial + realized
	metrics  Metrics
	session  int64 // persisted session identity, 0 = unset
	loaded   bool
	lastEval *pendingEval // evaluated-but-unpersisted bar — retry-safe
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

// EffectiveConfig returns the config the engine actually runs — defaults
// applied. Audit artifacts (session fingerprints) must serialize THIS, not
// the caller's pre-default struct.
func (e *Engine) EffectiveConfig() Config { return e.cfg }

// SetSession stamps the persisted session identity on new orders/positions.
// SetSession stamps the persisted session identity on the engine AND the
// in-memory position row — fill-time position upserts serialize e.pos
// directly, so the stamp must live on the row, not only the engine field.
func (e *Engine) SetSession(id int64) {
	e.session = id
	e.pos.SessionID = &id
}

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
	return fmt.Sprintf("%s@%s|%s|%s|%s|%s|s%d",
		e.cfg.StrategyID, e.cfg.StrategyVersion, e.cfg.Symbol,
		e.cfg.Timeframe, barTime.UTC().Format(time.RFC3339Nano), side,
		e.session)
}

// Restore loads persisted position + pending order + last-bar marker so a
// restart resumes state instead of replaying decisions. Session-scoped: a
// new configuration session starts clean — never inherits prior state.
func (e *Engine) Restore(ctx context.Context) error {
	pos, err := e.store.GetPosition(ctx, e.session, e.cfg.StrategyID, e.cfg.StrategyVersion,
		e.cfg.Symbol, e.cfg.Timeframe)
	if err != nil {
		return err
	}
	if pos != nil {
		e.pos = *pos
		e.hasPos = true
		e.equity = e.cfg.InitialEquity + pos.RealizedPnl
	}
	pend, err := e.store.GetPendingOrder(ctx, e.session, e.cfg.StrategyID, e.cfg.StrategyVersion,
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
// decisionRow builds the per-bar evaluation-ledger row. Source distinguishes
// live closes from recovery replays; replay is idempotent via the unique key.
func (e *Engine) decisionRow(c market.Candle, sig strategy.Signal,
	source string, actionable bool, blocked *string) repository.ShadowDecisionRow {
	action := "neutral"
	if sig.Action == strategy.ActionBuy {
		action = "buy"
	} else if sig.Action == strategy.ActionSell {
		action = "sell"
	}
	d := repository.ShadowDecisionRow{
		SessionID:  nilIfZero(e.session),
		StrategyID: e.cfg.StrategyID, StrategyVersion: e.cfg.StrategyVersion,
		Symbol: e.cfg.Symbol, Timeframe: string(e.cfg.Timeframe),
		BarTime: c.Time, DecidedAt: e.now().UTC(), Source: source,
		Action: action, Reason: sig.Reason, Actionable: actionable, BlockedReason: blocked,
	}
	if v, ok := sig.Metadata["fast_ema"].(float64); ok {
		d.FastEMA = &v
	}
	if v, ok := sig.Metadata["slow_ema"].(float64); ok {
		d.SlowEMA = &v
	}
	return d
}

func nilIfZero(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// barOutcome is everything one bar can durably produce beyond the ledger
// decision + checkpoint (which commitBar always writes). All fields optional.
type barOutcome struct {
	order   *repository.ShadowOrderRow        // order intent to persist
	resolve *repository.ShadowOrderResolution // terminal status for that order
	fill    *repository.ShadowFillRecord      // fill applied to that order
	fillNet float64                           // realized delta when fill closes
	pos     repository.ShadowPositionRow      // post-bar position (post-fill too)
}

// commitBar writes one bar's COMPLETE durable transition in a single
// transaction — order intent, optional fill/resolution, ledger decision,
// and the position/checkpoint — then applies the in-memory effects only
// after the commit succeeds. On error the engine is byte-identical to its
// pre-call state: nothing half-applied, nothing published as committed.
func (e *Engine) commitBar(ctx context.Context, c market.Candle, sig strategy.Signal,
	source string, actionable bool, blocked *string, out barOutcome) error {
	d := e.decisionRow(c, sig, source, actionable, blocked)
	out.pos.StrategyID, out.pos.StrategyVersion = e.cfg.StrategyID, e.cfg.StrategyVersion
	out.pos.Symbol, out.pos.Timeframe = e.cfg.Symbol, string(e.cfg.Timeframe)
	if e.session != 0 {
		out.pos.SessionID = &e.session
	}
	out.pos.LastBarTime = &c.Time
	bc := repository.ShadowBarCommit{
		NewOrder: out.order, Resolve: out.resolve, Fill: out.fill,
		Decision: &d, Position: out.pos,
	}
	res, err := e.store.CommitBar(ctx, bc)
	if err != nil {
		return err // durable marker unchanged — caller marks runtime unhealthy
	}
	// Commit succeeded — apply in-memory effects.
	e.pos = out.pos
	e.lastEval = nil
	e.metrics.Decisions++
	if out.order != nil {
		out.order.ID = res.OrderID
		if res.OrderCreated {
			// Only a live-pending order stays tracked; fills/resolutions are
			// already terminal in the same transaction.
			if out.resolve == nil && out.fill == nil {
				e.pending = out.order
				e.log.Info("shadow decision", "side", out.order.Side, "bar", c.Time, "order", res.OrderID)
			}
		} else {
			e.metrics.DupSkips++
		}
	}
	if out.fill != nil {
		e.equity += out.fillNet
		e.pending = nil
		e.hasPos = true
		e.metrics.Fills++
		ft := out.fill.FilledAt
		e.metrics.LastFillAt = &ft
		e.log.Info("shadow fill", "order", res.OrderID, "side", out.order.Side,
			"price", out.fill.FillPrice)
	}
	return nil
}

// evaluate runs the stream for a bar ONCE per process lifetime. A failed
// persist retries the same bar — the cached signal is reused so the
// streaming indicator state is never double-advanced.
func (e *Engine) evaluate(c market.Candle) strategy.Signal {
	if e.lastEval != nil && e.lastEval.bar.Equal(c.Time) {
		return e.lastEval.sig
	}
	sig := e.stream.Next(c)
	e.lastEval = &pendingEval{bar: c.Time, sig: sig}
	return sig
}

type pendingEval struct {
	bar time.Time
	sig strategy.Signal
}

func (e *Engine) OnBarClosed(ctx context.Context, c market.Candle) error {
	if e.pos.LastBarTime != nil && !c.Time.After(*e.pos.LastBarTime) {
		e.metrics.DupSkips++
		return nil // already processed — reconnect/replay duplicate
	}
	e.metrics.BarsProcessed++
	if e.stream == nil {
		return fmt.Errorf("shadow engine has no strategy stream")
	}
	sig := e.evaluate(c)
	var side string
	e.metrics.SignalsSeen++
	switch sig.Action {
	case strategy.ActionBuy:
		if e.pos.Side != nil {
			return e.commitBar(ctx, c, sig, "live", false, strp("already_long"), barOutcome{pos: e.pos})
		}
		side = "BUY"
	case strategy.ActionSell:
		if e.pos.Side == nil {
			return e.commitBar(ctx, c, sig, "live", false, strp("flat_no_position"), barOutcome{pos: e.pos})
		}
		side = "SELL"
	default:
		return e.commitBar(ctx, c, sig, "live", false, nil, barOutcome{pos: e.pos})
	}
	// One pending order at a time: a live pending order stays until filled
	// or expired; the new signal is recorded in the bar marker regardless.
	if e.pending != nil {
		return e.commitBar(ctx, c, sig, "live", false, strp("pending_order_active"), barOutcome{pos: e.pos})
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
	// Order intent + ledger decision + checkpoint commit atomically: the
	// decision is actionable whether this commit created the order or an
	// identical earlier commit did (order_key dedupe).
	return e.commitBar(ctx, c, sig, "live", true, nil, barOutcome{order: &o, pos: e.pos})
}

func strp(s string) *string { return &s }

// Warmup feeds one already-closed historical bar to the stream AND advances
// the durable resume marker WITHOUT evaluating a signal — fresh-start state
// reconstruction: bars that closed before the engine existed must not
// retroactively create decisions. On failure the marker is reverted (the
// stream is not — the caller must treat the failure as fatal and rebuild).
func (e *Engine) Warmup(ctx context.Context, c market.Candle) error {
	if e.pos.LastBarTime != nil && !c.Time.After(*e.pos.LastBarTime) {
		return nil
	}
	if e.stream != nil {
		e.stream.Next(c)
	}
	prev := e.pos.LastBarTime
	e.pos.LastBarTime = &c.Time
	if err := e.persistBar(ctx); err != nil {
		e.pos.LastBarTime = prev
		return err
	}
	return nil
}

// FeedWarmup pushes one already-processed historical bar through the stream
// WITHOUT touching the durable marker — restart-time indicator
// reconstruction for bars the checkpoint already covers.
func (e *Engine) FeedWarmup(c market.Candle) {
	if e.stream != nil {
		e.stream.Next(c)
	}
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
	e.metrics.BarsProcessed++
	if e.stream == nil {
		return fmt.Errorf("shadow engine has no strategy stream")
	}
	sig := e.evaluate(c)
	e.metrics.SignalsSeen++
	var side string
	switch sig.Action {
	case strategy.ActionBuy:
		if e.pos.Side != nil {
			return e.commitBar(ctx, c, sig, "recovery", false, strp("already_long"), barOutcome{pos: e.pos})
		}
		side = "BUY"
	case strategy.ActionSell:
		if e.pos.Side == nil {
			return e.commitBar(ctx, c, sig, "recovery", false, strp("flat_no_position"), barOutcome{pos: e.pos})
		}
		side = "SELL"
	default:
		return e.commitBar(ctx, c, sig, "recovery", false, nil, barOutcome{pos: e.pos})
	}
	if e.pending != nil {
		return e.commitBar(ctx, c, sig, "recovery", false, strp("pending_order_active"), barOutcome{pos: e.pos})
	}

	// anchor = the bar's close as a REAL instant: the label is Kyiv wall
	// clock rendered naive-UTC — converting through Europe/Kyiv puts the
	// execution window in the same coordinate system as quote times.
	anchor := live.LabelToInstant(c.Time).Add(e.tfDuration())
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

	windowEnd := anchor.Add(e.cfg.FillTimeout)
	if e.now().Before(windowEnd) {
		// Window still open: a live quote AFTER the anchor may legitimately fill.
		e.log.Info("shadow recovery: pending order resumed",
			"side", side, "bar", c.Time, "anchor", anchor)
		return e.commitBar(ctx, c, sig, "recovery", true, nil, barOutcome{order: &o, pos: e.pos})
	}

	// Window closed while offline — consult PERSISTED evidence only.
	obs, err := e.store.FindQuoteInWindow(ctx, e.cfg.Provider, e.cfg.Symbol, anchor, windowEnd)
	if err != nil {
		return err
	}
	if obs == nil {
		// No admissible quote found — but absence of evidence is only proof
		// of absence when the window was OBSERVED. Check recorder coverage:
		// complete → confirmed missed; incomplete/unknown → unobservable.
		status, reason := "missed",
			"no admissible quote recorded in fully observed execution window"
		covered, cerr := e.store.CoverageComplete(ctx, e.cfg.Provider, e.cfg.Symbol, anchor, windowEnd)
		if cerr != nil {
			return cerr
		}
		if !covered {
			status, reason = "unobservable",
				"quote coverage incomplete or unknown for execution window; outcome cannot be determined"
		}
		if covered {
			e.metrics.Missed++
		} else {
			e.metrics.Unobservable++
		}
		e.log.Info("shadow decision unresolved", "status", status, "side", side, "bar", c.Time, "anchor", anchor)
		return e.commitBar(ctx, c, sig, "recovery", true, strp(status),
			barOutcome{order: &o, pos: e.pos,
				resolve: &repository.ShadowOrderResolution{Status: status, Reason: reason}})
	}
	ev := quoteEvidence{
		providerTime: *obs.ProviderTime, receivedAt: obs.ReceivedAt,
		bid: *obs.Bid, ask: *obs.Ask,
	}
	e.log.Info("shadow recovery fill replayed", "side", side, "bar", c.Time,
		"quote_at", obs.ProviderTime)
	fr, pos, net := e.computeFill(&o, ev, obs.ReceivedAt)
	return e.commitBar(ctx, c, sig, "recovery", true, nil,
		barOutcome{order: &o, fill: &fr, fillNet: net, pos: pos})
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

// computeFill derives the fill record, the post-fill position, and the
// realized equity delta from an admissible quote. PURE — no store calls and
// no engine mutation: callers commit atomically, then apply the outcome.
// Single source of fill truth for both live quotes and recovery replays.
func (e *Engine) computeFill(o *repository.ShadowOrderRow, ev quoteEvidence, filledAt time.Time) (repository.ShadowFillRecord, repository.ShadowPositionRow, float64) {
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
	var net float64 // realized delta — nonzero only when a SELL closes a long
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
		net = gross - pos.EntryCommission - comm
		pos.RealizedPnl += net
		pos.RealizedComm += comm
		pos.RealizedSpread += spreadCost * pos.Quantity
		pos.Side = nil
		pos.Quantity = 0
		pos.EntryPrice = nil
		pos.EntryAt = nil
		pos.EntryOrderID = nil
		pos.EntryCommission = 0
	}
	pos.StrategyID, pos.StrategyVersion = e.cfg.StrategyID, e.cfg.StrategyVersion
	pos.Symbol, pos.Timeframe = e.cfg.Symbol, string(e.cfg.Timeframe)
	if e.session != 0 {
		pos.SessionID = &e.session
	}
	return fr, pos, net
}

// fillOrder commits a live-quote fill: order-fill + position transition in
// one transaction; in-memory position, equity, and metrics apply ONLY after
// the commit succeeds — a failed commit leaves engine state untouched.
func (e *Engine) fillOrder(ctx context.Context, o *repository.ShadowOrderRow, ev quoteEvidence, filledAt time.Time) error {
	fr, pos, net := e.computeFill(o, ev, filledAt)
	if err := e.store.FillOrderAndPosition(ctx, o.ID, fr, pos); err != nil {
		return err
	}
	e.pos = pos
	e.equity += net
	e.hasPos = true
	e.metrics.Fills++
	ft := filledAt
	e.metrics.LastFillAt = &ft
	e.log.Info("shadow fill", "order", o.ID, "side", o.Side, "price", fr.FillPrice, "mid", *fr.QuoteMid)
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

// Snapshot is a deep-enough copy for publication: the position is copied by
// value and the pending order is copied to a fresh struct — callers can
// never reach into mutable engine state through the returned references.
func (e *Engine) Snapshot() Snapshot {
	var pos *repository.ShadowPositionRow
	if e.hasPos || e.pos.Side != nil {
		p := e.pos
		pos = &p
	}
	var pend *repository.ShadowOrderRow
	if e.pending != nil {
		p := *e.pending
		pend = &p
	}
	return Snapshot{
		StrategyID: e.cfg.StrategyID, StrategyVersion: e.cfg.StrategyVersion,
		Symbol: e.cfg.Symbol, Timeframe: string(e.cfg.Timeframe),
		Mode: "simulated", Position: pos, Pending: pend,
		RealizedPnl: e.pos.RealizedPnl, RealizedComm: e.pos.RealizedComm,
		RealizedSpread: e.pos.RealizedSpread, Equity: e.equity,
		LastBarTime: e.pos.LastBarTime, SessionID: e.session,
	}
}
