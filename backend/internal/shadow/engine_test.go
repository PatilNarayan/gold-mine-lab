package shadow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// ── fakes ───────────────────────────────────────────────────────────────

type memStore struct {
	mu      sync.Mutex
	orders  map[string]*repository.ShadowOrderRow
	nextID  int64
	pos     map[string]*repository.ShadowPositionRow
	obs     []repository.QuoteObservationRow // persisted quote evidence
	txCalls int                              // FillOrderAndPosition invocations — must be atomic-counted
}

func newMemStore() *memStore {
	return &memStore{orders: map[string]*repository.ShadowOrderRow{},
		pos: map[string]*repository.ShadowPositionRow{}}
}

func pkey(sid, ver, sym string, tf market.Timeframe) string {
	return sid + "|" + ver + "|" + sym + "|" + string(tf)
}

func (m *memStore) CreateOrderIfAbsent(_ context.Context, o repository.ShadowOrderRow) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ex, ok := m.orders[o.OrderKey]; ok {
		return ex.ID, false, nil
	}
	m.nextID++
	cp := o
	cp.ID = m.nextID
	cp.CreatedAt = time.Now()
	m.orders[o.OrderKey] = &cp
	return cp.ID, true, nil
}
func (m *memStore) GetPendingOrder(_ context.Context, sid, ver, sym string, tf market.Timeframe) (*repository.ShadowOrderRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.orders {
		if o.Status == "pending" && o.StrategyID == sid {
			cp := *o
			return &cp, nil
		}
	}
	return nil, nil
}
func (m *memStore) SetOrderStatus(_ context.Context, id int64, status, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, o := range m.orders {
		if o.ID == id {
			o.Status = status
			o.RejectReason = &reason
		}
	}
	return nil
}
func (m *memStore) FillOrderAndPosition(_ context.Context, id int64, f repository.ShadowFillRecord, p repository.ShadowPositionRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.txCalls++
	for _, o := range m.orders {
		if o.ID == id {
			o.Status = "filled"
			o.FillPrice = &f.FillPrice
			o.Quantity = &f.Quantity
			o.Commission = &f.Commission
			o.QuoteBid, o.QuoteAsk, o.QuoteMid = f.QuoteBid, f.QuoteAsk, f.QuoteMid
			o.QuoteProviderTime, o.QuoteReceivedAt = f.QuoteProviderTime, f.QuoteReceivedAt
			o.FilledAt = &f.FilledAt
		}
	}
	cp := p
	m.pos[pkey(p.StrategyID, p.StrategyVersion, p.Symbol, market.Timeframe(p.Timeframe))] = &cp
	return nil
}
func (m *memStore) UpdateOrderFilled(context.Context, int64, repository.ShadowFillRecord) error {
	return errors.New("non-atomic path must not be used")
}
func (m *memStore) FindQuoteInWindow(_ context.Context, provider, symbol string, from, to time.Time) (*repository.QuoteObservationRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *repository.QuoteObservationRow
	for _, o := range m.obs {
		if o.ProviderTime == nil || o.Bid == nil || o.Ask == nil || o.Stale {
			continue
		}
		if o.ProviderTime.Before(from) || !o.ProviderTime.Before(to) {
			continue
		}
		if *o.Ask < *o.Bid || *o.Bid <= 0 {
			continue
		}
		cp := o
		if best == nil || o.ProviderTime.Before(*best.ProviderTime) {
			best = &cp
		}
	}
	return best, nil
}
func (m *memStore) UpsertPosition(_ context.Context, p repository.ShadowPositionRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := p
	m.pos[pkey(p.StrategyID, p.StrategyVersion, p.Symbol, market.Timeframe(p.Timeframe))] = &cp
	return nil
}
func (m *memStore) GetPosition(_ context.Context, sid, ver, sym string, tf market.Timeframe) (*repository.ShadowPositionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.pos[pkey(sid, ver, sym, tf)]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, nil
}
func (m *memStore) ListOrders(_ context.Context, sid, sym string, tf market.Timeframe, limit int) ([]repository.ShadowOrderRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []repository.ShadowOrderRow
	for _, o := range m.orders {
		out = append(out, *o)
	}
	return out, nil
}

// scripted stream emits a fixed action at chosen bar times.
type scriptedStream struct {
	actions map[time.Time]strategy.Action
}

func (s *scriptedStream) Next(c market.Candle) strategy.Signal {
	return strategy.Signal{Action: s.actions[c.Time]}
}

var testCfg = Config{
	StrategyID: "ema-cross", StrategyVersion: "1.0.0", Symbol: "XAU/USD",
	Timeframe: market.Timeframe1h, InitialEquity: 100000,
	PositionSizePct: 10, CommissionPct: 0.02,
	FillTimeout: time.Minute, QuoteMaxAge: 30 * time.Second,
}

var (
	bar1 = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	bar2 = bar1.Add(time.Hour)
	bar3 = bar2.Add(time.Hour)
	t0   = bar1.Add(2 * time.Minute) // decision moment
)

func mkEngine(store Store, actions map[time.Time]strategy.Action, now time.Time) *Engine {
	e := New(testCfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.SetClock(func() time.Time { return now })
	e.SetStream(&scriptedStream{actions: actions})
	return e
}

func bar(t time.Time) market.Candle {
	return market.Candle{Time: t, Open: 100, High: 101, Low: 99, Close: 100}
}

// freshQuote builds a quote whose provider time equals `at` — it must
// fall AFTER the decision time to qualify as a fill candidate.
func freshQuote(at time.Time, bid, ask float64) live.Quote {
	return live.Quote{AssetID: "xauusd", Symbol: "XAU/USD",
		Price: (bid + ask) / 2, PriceKind: "mid",
		Bid: &bid, Ask: &ask, ProviderTime: &at, ReceivedAt: at.Add(100 * time.Millisecond)}
}

// ── tests ───────────────────────────────────────────────────────────────

// Signal at confirmed close → pending order → first valid quote fills at ASK.
func TestShadowEntryFillsAtAsk(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	if err := e.OnBarClosed(context.Background(), bar(bar1)); err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().Pending == nil || e.Snapshot().Pending.Side != "BUY" {
		t.Fatal("no pending BUY after buy signal")
	}
	// An invalid quote (missing ask) must NOT fill.
	q := freshQuote(t0.Add(time.Second), 100, 100.10)
	q.Ask = nil
	filled, _ := e.OnQuote(context.Background(), q)
	if filled {
		t.Fatal("filled on a quote with missing ask")
	}
	// Valid quote → fill at ask 100.10, not mid 100.05.
	filled, err := e.OnQuote(context.Background(), freshQuote(t0.Add(2*time.Second), 100.00, 100.10))
	if err != nil || !filled {
		t.Fatalf("fill failed: %v %v", filled, err)
	}
	snap := e.Snapshot()
	if snap.Position == nil || snap.Position.EntryPrice == nil {
		t.Fatal("no open position after fill")
	}
	if *snap.Position.EntryPrice != 100.10 {
		t.Fatalf("entry %v — must be ASK 100.10, not mid", *snap.Position.EntryPrice)
	}
	// qty = equity*10%/fill = 10000/100.10 ≈ 99.9
	if snap.Position.Quantity < 99 || snap.Position.Quantity > 101 {
		t.Fatalf("qty %v — expected ~99.9", snap.Position.Quantity)
	}
	if snap.Position.EntryCommission <= 0 {
		t.Fatal("entry commission not recorded")
	}
}

// Exit fills at BID and realizes net = gross - both commissions.
func TestShadowExitFillsAtBidReconciles(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, map[time.Time]strategy.Action{
		bar1: strategy.ActionBuy, bar2: strategy.ActionSell}, t0)
	ctx := context.Background()
	e.OnBarClosed(ctx, bar(bar1))
	e.OnQuote(ctx, freshQuote(t0, 100.00, 100.10)) // entry at 100.10
	e.OnBarClosed(ctx, bar(bar2))
	if e.Snapshot().Pending == nil || e.Snapshot().Pending.Side != "SELL" {
		t.Fatal("no pending SELL")
	}
	filled, _ := e.OnQuote(ctx, freshQuote(t0.Add(3*time.Second), 110.00, 110.10))
	if !filled {
		t.Fatal("exit not filled")
	}
	snap := e.Snapshot()
	if snap.Position != nil && snap.Position.Side != nil {
		t.Fatal("position still open after exit")
	}
	// qty=10000/100.10≈99.900; gross=(110-100.10)*qty≈989.01;
	// comms = qty*100.10*0.0002 + qty*110*0.0002
	qty := 10000.0 / 100.10
	gross := (110.00 - 100.10) * qty
	comms := qty*100.10*0.0002 + qty*110.00*0.0002
	want := gross - comms
	if diff := snap.RealizedPnl - want; diff < -0.01 || diff > 0.01 {
		t.Fatalf("realized %v want ~%v", snap.RealizedPnl, want)
	}
}

// Replay/reconnect: the same closed bar re-delivered must not create a
// second order — idempotency key + last-bar marker both protect.
func TestShadowDuplicateCloseIsIdempotent(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	ctx := context.Background()
	e.OnBarClosed(ctx, bar(bar1))
	e.OnBarClosed(ctx, bar(bar1)) // replayed event
	e.OnBarClosed(ctx, bar(bar1))
	orders, _ := store.ListOrders(ctx, "ema-cross", "XAU/USD", market.Timeframe1h, 100)
	if len(orders) != 1 {
		t.Fatalf("orders=%d — duplicate close created extra orders", len(orders))
	}
}

// Restart: position + pending + lastBar restore; a replayed close doesn't
// re-decide and the pending order still fills.
func TestShadowRestartRecovery(t *testing.T) {
	store := newMemStore()
	e1 := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	ctx := context.Background()
	e1.OnBarClosed(ctx, bar(bar1))
	e1.OnQuote(ctx, freshQuote(t0, 100.0, 100.1)) // entry fill
	e1.OnBarClosed(ctx, bar(bar2))                // neutral bar advances marker

	// "Restart": fresh engine, same store.
	e2 := mkEngine(store, map[time.Time]strategy.Action{bar2: strategy.ActionBuy}, t0)
	if err := e2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	snap := e2.Snapshot()
	if snap.Position == nil || snap.Position.Side == nil || *snap.Position.Side != "BUY" {
		t.Fatal("open position lost across restart")
	}
	if *snap.Position.EntryPrice != 100.1 {
		t.Fatalf("restored entry %v want 100.1", *snap.Position.EntryPrice)
	}
	// Replaying bar2 (already processed) must not create an order.
	e2.OnBarClosed(ctx, bar(bar2))
	orders, _ := store.ListOrders(ctx, "ema-cross", "XAU/USD", market.Timeframe1h, 100)
	fills := 0
	for _, o := range orders {
		if o.Status == "filled" {
			fills++
		}
	}
	if len(orders) != 1 || fills != 1 {
		t.Fatalf("restart replay produced %d orders", len(orders))
	}
}

// Stale/crossed/missing quotes never fill; order expires after timeout.
func TestShadowQuoteQualityAndExpiry(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	ctx := context.Background()
	e.OnBarClosed(ctx, bar(bar1))

	stale := freshQuote(t0.Add(time.Second), 100, 100.1)
	old := t0.Add(-time.Hour)
	stale.ProviderTime = &old // provider timestamp ancient → stale
	if f, _ := e.OnQuote(ctx, stale); f {
		t.Fatal("filled on stale quote")
	}
	if f, _ := e.OnQuote(ctx, freshQuote(t0.Add(time.Second), 100.2, 100.1)); f {
		t.Fatal("filled on crossed quote (bid>ask)")
	}
	// Provider time BEFORE the decision must not fill (would embed past info).
	pre := freshQuote(t0.Add(time.Second), 100, 100.1)
	before := t0.Add(-time.Minute)
	pre.ProviderTime = &before
	if f, _ := e.OnQuote(ctx, pre); f {
		t.Fatal("filled on a quote predating the decision")
	}
	// Timeout: advance clock past FillTimeout → next tick expires it.
	e.SetClock(func() time.Time { return t0.Add(2 * time.Minute) })
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	snap := e.Snapshot()
	if snap.Pending != nil {
		t.Fatal("pending order survived past FillTimeout")
	}
	orders, _ := store.ListOrders(ctx, "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	if orders[0].Status != "expired" {
		t.Fatalf("order status %q want expired", orders[0].Status)
	}
}

// A sell signal while flat and a buy while long are both ignored — no
// shorting, no pyramiding.
func TestShadowIgnoresNonActionableSignals(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionSell}, t0)
	e.OnBarClosed(context.Background(), bar(bar1))
	if e.Snapshot().Pending != nil {
		t.Fatal("SELL while flat must not create an order")
	}
}

// Order-key uniqueness: same signal at the same bar produces ONE row
// across engine instances (restart-safe idempotency).
func TestShadowOrderKeyStableAcrossEngines(t *testing.T) {
	store := newMemStore()
	e1 := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	e2 := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	ctx := context.Background()
	// Simulate a replayed close reaching a "new" engine post-restart.
	e1.OnBarClosed(ctx, bar(bar1))
	e2.OnBarClosed(ctx, bar(bar1))
	orders, _ := store.ListOrders(ctx, "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	if len(orders) != 1 {
		t.Fatalf("expected 1 order row across engines, got %d", len(orders))
	}
}

// ── Phase 5: downtime recovery semantics ────────────────────────────────

// mkRecoveryEngine: engine "started" at now, then ProcessMissedClose is
// fed closes that happened while it was offline.
func obsQuote(at time.Time, bid, ask float64) repository.QuoteObservationRow {
	return repository.QuoteObservationRow{Provider: "tiingo", Symbol: "XAU/USD",
		ProviderTime: &at, Bid: &bid, Ask: &ask, ReceivedAt: at.Add(100 * time.Millisecond)}
}

// Signal fired while offline; window long gone; no evidence → 'missed',
// never a fill.
func TestRecoveryMissedNoEvidence(t *testing.T) {
	store := newMemStore()         // no observations at all
	now := bar1.Add(3 * time.Hour) // recovered 3h after the signal
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, now)
	ctx := context.Background()
	if err := e.ProcessMissedClose(ctx, bar(bar1)); err != nil {
		t.Fatal(err)
	}
	orders, _ := store.ListOrders(ctx, "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	if len(orders) != 1 || orders[0].Status != "missed" {
		t.Fatalf("expected 1 missed order, got %+v", orders)
	}
	if orders[0].RejectReason == nil {
		t.Fatal("missed order must carry a reason")
	}
	if e.Snapshot().Position != nil {
		t.Fatal("missed decision must not open a position")
	}
	if m := e.MetricsSnapshot(); m.Missed != 1 || m.Recoveries != 1 {
		t.Fatalf("metrics wrong: %+v", m)
	}
}

// Window passed but persisted evidence exists → deterministic replay fill
// at the recorded quote (ASK for entry), not the restart-time quote.
func TestRecoveryReplaysPersistedEvidence(t *testing.T) {
	store := newMemStore()
	anchor := bar1.Add(time.Hour)           // 1h bar close
	evQuote := anchor.Add(30 * time.Second) // inside the 1-minute fill window
	store.obs = append(store.obs, obsQuote(evQuote, 100.0, 100.1))
	now := anchor.Add(30 * time.Minute) // within the test's 1min timeout? No —
	// cfg FillTimeout=1m in testCfg → window long gone → replay path.
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, now)
	ctx := context.Background()
	if err := e.ProcessMissedClose(ctx, bar(bar1)); err != nil {
		t.Fatal(err)
	}
	orders, _ := store.ListOrders(ctx, "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	if len(orders) != 1 || orders[0].Status != "filled" {
		t.Fatalf("expected replayed fill, got %+v", orders)
	}
	o := orders[0]
	if o.FillPrice == nil || *o.FillPrice != 100.1 {
		t.Fatalf("replay fill %v — must be the recorded ASK", o.FillPrice)
	}
	if o.FilledAt == nil || *o.FilledAt != evQuote.Add(100*time.Millisecond) {
		t.Fatalf("filled_at %v — must be the evidence's receipt time", o.FilledAt)
	}
	if o.DecisionAt != anchor {
		t.Fatalf("decision_at %v — must be the bar-close anchor, not restart time", o.DecisionAt)
	}
	if e.Snapshot().Position == nil {
		t.Fatal("replayed fill must open the position")
	}
}

// Re-running recovery over the same window creates nothing new.
func TestRecoveryIsIdempotent(t *testing.T) {
	store := newMemStore()
	now := bar1.Add(3 * time.Hour)
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, now)
	ctx := context.Background()
	e.ProcessMissedClose(ctx, bar(bar1))
	e.ProcessMissedClose(ctx, bar(bar1)) // replayed recovery
	orders, _ := store.ListOrders(ctx, "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	if len(orders) != 1 {
		t.Fatalf("recovery produced %d orders — must be exactly 1", len(orders))
	}
	if e.MetricsSnapshot().DupSkips == 0 {
		t.Fatal("replayed decision should be counted as a duplicate skip")
	}
}

// Window still open at recovery → pending resumes; ONLY quotes after the
// anchor may fill (a quote predating the signal close is not evidence).
func TestRecoveryWindowStillOpen(t *testing.T) {
	store := newMemStore()
	anchor := bar1.Add(time.Hour)
	now := anchor.Add(10 * time.Second) // within the 1min test timeout
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, now)
	ctx := context.Background()
	if err := e.ProcessMissedClose(ctx, bar(bar1)); err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().Pending == nil {
		t.Fatal("open-window recovery must leave the order pending")
	}
	// A "live" quote BEFORE the anchor must be refused; one after must fill.
	if f, _ := e.OnQuote(ctx, freshQuote(anchor.Add(-time.Second), 100, 100.1)); f {
		t.Fatal("quote predating the decision anchor filled — look-ahead")
	}
	filled, _ := e.OnQuote(ctx, freshQuote(anchor.Add(2*time.Second), 100, 100.1))
	if !filled {
		t.Fatal("valid post-anchor quote did not fill the resumed order")
	}
	if *e.Snapshot().Position.EntryPrice != 100.1 {
		t.Fatal("entry must be the observed ask")
	}
}

// Restart BEFORE the signal's bar closes: position+marker restore; the
// signal is then processed once live — no duplicate.
func TestRestartBeforeSignal(t *testing.T) {
	store := newMemStore()
	e1 := mkEngine(store, map[time.Time]strategy.Action{bar2: strategy.ActionBuy}, t0)
	ctx := context.Background()
	e1.OnBarClosed(ctx, bar(bar1)) // neutral bar before restart

	e2 := mkEngine(store, map[time.Time]strategy.Action{bar2: strategy.ActionBuy}, t0)
	if err := e2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	e2.OnBarClosed(ctx, bar(bar2)) // live close after restart → signal
	if e2.Snapshot().Pending == nil {
		t.Fatal("post-restart signal not processed")
	}
	orders, _ := store.ListOrders(ctx, "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	if len(orders) != 1 {
		t.Fatalf("expected exactly 1 order, got %d", len(orders))
	}
}
