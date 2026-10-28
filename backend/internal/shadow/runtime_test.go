package shadow

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// ── helpers ───────────────────────────────────────────────────────────────

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mkRuntime(e *Engine, opts RuntimeOptions) *Runtime {
	if opts.Logger == nil {
		opts.Logger = discardLog()
	}
	return NewRuntime(e, opts)
}

// submitSync admits an event and waits for the owner to process it —
// deterministic, no sleeps.
func submitSync(t *testing.T, rt *Runtime, ev Event) error {
	t.Helper()
	res := make(chan error, 1)
	ev.Result = res
	if err := rt.Submit(context.Background(), ev); err != nil {
		return err
	}
	select {
	case err := <-res:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("event never processed")
		return nil
	}
}

func barEvent(c market.Candle) Event { return Event{Kind: EvBarClosed, Candle: c} }
func quoteEvent(q live.Quote) Event  { return Event{Kind: EvQuote, Quote: q} }
func tickEvent() Event               { return Event{Kind: EvTick} }

func startTestRuntime(t *testing.T, e *Engine, opts RuntimeOptions) *Runtime {
	t.Helper()
	rt := mkRuntime(e, opts)
	rt.Start(context.Background(), nil)
	// Owner starts asynchronously; first submitSync provides the barrier.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = rt.Stop(ctx)
	})
	return rt
}

// ── A. Concurrency ────────────────────────────────────────────────────────

// Concurrent quote/bar/tick submissions racing snapshot reads — exactly one
// owner goroutine mutates the engine; all submitted events are accounted
// for. Designed to run under -race.
func TestRuntimeConcurrentSubmitAndSnapshot(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{})

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				switch i % 3 {
				case 0:
					_ = rt.Submit(context.Background(), quoteEvent(freshQuote(t0.Add(time.Duration(i)*time.Millisecond), 100, 100.1)))
				case 1:
					_ = rt.Submit(context.Background(), barEvent(bar(bar1)))
				default:
					_ = rt.Submit(context.Background(), tickEvent())
				}
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_ = rt.Snapshot() // must never race or tear
			}
		}()
	}
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	s := rt.Snapshot()
	if s.Stats.Submitted != s.Stats.Processed+s.Stats.Dropped {
		t.Fatalf("admitted events unaccounted: %+v", s.Stats)
	}
	if s.Health.State != HealthStopped {
		t.Fatalf("health %q want stopped", s.Health.State)
	}
}

// The published snapshot must be immutable: mutating a returned Position or
// Pending must not reach back into engine state.
func TestRuntimeSnapshotIsolation(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{})
	if err := submitSync(t, rt, barEvent(bar(bar1))); err != nil {
		t.Fatal(err)
	}
	s1 := rt.Snapshot()
	if s1.Engine.Pending == nil {
		t.Fatal("expected pending order in snapshot")
	}
	// Mutate every reachable reference.
	s1.Engine.Pending.Side = "SELL"
	s1.Engine.Pending.Status = "filled"
	qty := 999.0
	s1.Engine.Pending.Quantity = &qty
	if s1.Engine.Position != nil {
		s1.Engine.Position.Quantity = -1
	}
	s2 := rt.Snapshot()
	if s2.Engine.Pending.Side != "BUY" || s2.Engine.Pending.Status != "pending" {
		t.Fatalf("snapshot shares mutable engine state: %+v", s2.Engine.Pending)
	}
	if s2.Engine.Pending.Quantity != nil {
		t.Fatal("snapshot order fields leaked back into the engine")
	}
}

// ── B. Duplicate processing ───────────────────────────────────────────────

func TestRuntimeDuplicateBarIsIdempotent(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{})
	for i := 0; i < 3; i++ {
		if err := submitSync(t, rt, barEvent(bar(bar1))); err != nil {
			t.Fatal(err)
		}
	}
	orders, _ := store.ListOrders(context.Background(), "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	if len(orders) != 1 {
		t.Fatalf("orders=%d want 1", len(orders))
	}
	if len(store.Decisions()) != 1 {
		t.Fatalf("decisions=%d want 1", len(store.Decisions()))
	}
	if rt.Snapshot().Metrics.DupSkips == 0 {
		t.Fatal("duplicate deliveries must be counted")
	}
}

// Restart + redelivery preserves the committed outcome: the second runtime
// restores durable state and the replayed bar is a dup-skip, not a new order.
func TestRuntimeRestartRedelivery(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()

	e1 := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	rt1 := startTestRuntime(t, e1, RuntimeOptions{})
	if err := submitSync(t, rt1, barEvent(bar(bar1))); err != nil {
		t.Fatal(err)
	}
	stopCtx, c := context.WithTimeout(ctx, 5*time.Second)
	defer c()
	if err := rt1.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}

	e2 := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	e2.SetSession(e1.Snapshot().SessionID)
	if err := e2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	rt2 := startTestRuntime(t, e2, RuntimeOptions{})
	if err := submitSync(t, rt2, barEvent(bar(bar1))); err != nil {
		t.Fatal(err)
	}
	orders, _ := store.ListOrders(ctx, "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	if len(orders) != 1 {
		t.Fatalf("restart replay produced %d orders", len(orders))
	}
}

// ── C. Persistence failures ───────────────────────────────────────────────

type failAlwaysStore struct {
	*memStore
	err error
}

func (f *failAlwaysStore) CommitBar(context.Context, repository.ShadowBarCommit) (repository.ShadowBarResult, error) {
	return repository.ShadowBarResult{}, f.err
}

// A persistence failure must NOT advance the durable checkpoint, must mark
// the runtime unhealthy, and must halt further processing — the failed
// transition is never published as committed.
func TestRuntimePersistenceFailureHaltsRuntime(t *testing.T) {
	base := newMemStore()
	store := &failAlwaysStore{memStore: base, err: errors.New("disk on fire")}
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{})

	if err := submitSync(t, rt, barEvent(bar(bar1))); err == nil {
		t.Fatal("engine error must reach the event result")
	}
	s := rt.Snapshot()
	if s.Health.State != HealthUnhealthy {
		t.Fatalf("health %q want unhealthy", s.Health.State)
	}
	if s.Health.Reason == "" {
		t.Fatal("unhealthy runtime must carry a failure reason")
	}
	if s.Engine.LastBarTime != nil {
		t.Fatal("failed transition published as committed — marker advanced")
	}
	if s.Engine.Pending != nil {
		t.Fatal("failed transition published a pending order")
	}
	// Further processing halts.
	if err := rt.Submit(context.Background(), barEvent(bar(bar2))); !errors.Is(err, ErrRuntimeUnhealthy) {
		t.Fatalf("submit on unhealthy runtime: %v", err)
	}
	// Admitted-but-unprocessed events are counted, never silently claimed.
	_ = submitSync(t, rt, tickEvent())
}

// ambiguousStore simulates a commit that SUCCEEDED but whose client saw an
// error — the classic ambiguous-commit case.
type ambiguousStore struct {
	*memStore
	arm bool
}

func (a *ambiguousStore) CommitBar(ctx context.Context, c repository.ShadowBarCommit) (repository.ShadowBarResult, error) {
	res, err := a.memStore.CommitBar(ctx, c)
	if a.arm {
		a.arm = false
		return res, errors.New("ambiguous: connection lost during commit")
	}
	return res, err
}

// Ambiguous commit resolution: inspect durable state, don't blindly repeat.
// After the ambiguous failure the runtime goes unhealthy; on "restart" the
// restored engine sees the committed marker and the replayed bar dup-skips —
// no double order, no double decision.
func TestRuntimeAmbiguousCommitResolvedByState(t *testing.T) {
	base := newMemStore()
	store := &ambiguousStore{memStore: base, arm: true}
	e1 := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	rt1 := startTestRuntime(t, e1, RuntimeOptions{})

	if err := submitSync(t, rt1, barEvent(bar(bar1))); err == nil {
		t.Fatal("ambiguous commit must surface as an error")
	}
	if rt1.Snapshot().Health.State != HealthUnhealthy {
		t.Fatal("ambiguous commit must degrade the runtime, not retry blindly")
	}
	stopCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	_ = rt1.Stop(stopCtx)

	// "Restart": restore inspects durable state — the commit DID land.
	e2 := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	if err := e2.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e2.Snapshot().LastBarTime == nil {
		t.Fatal("committed marker must be visible to the rebuilt engine")
	}
	rt2 := startTestRuntime(t, e2, RuntimeOptions{})
	if err := submitSync(t, rt2, barEvent(bar(bar1))); err != nil {
		t.Fatal(err)
	}
	orders, _ := store.ListOrders(context.Background(), "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	if len(orders) != 1 {
		t.Fatalf("ambiguous commit re-executed: %d orders", len(orders))
	}
	if len(store.Decisions()) != 1 {
		t.Fatalf("ambiguous commit duplicated decisions: %d", len(store.Decisions()))
	}
}

// posFailStore fails the marker/position upsert — warmup checkpoint guard.
type posFailStore struct{ *memStore }

func (f *posFailStore) UpsertPosition(context.Context, repository.ShadowPositionRow) error {
	return errors.New("injected marker-write failure")
}

// fillFailStore fails order fills — the equity-before-commit bug guard.
type fillFailStore struct {
	*memStore
	arm bool
}

func (f *fillFailStore) FillOrderAndPosition(ctx context.Context, id int64, fr repository.ShadowFillRecord, p repository.ShadowPositionRow) error {
	if f.arm {
		f.arm = false
		return errors.New("injected fill failure")
	}
	return f.memStore.FillOrderAndPosition(ctx, id, fr, p)
}

// A failed fill must leave equity AND position untouched — in-memory state
// must never drift ahead of durable state.
func TestRuntimeFillFailureLeavesStateConsistent(t *testing.T) {
	base := newMemStore()
	store := &fillFailStore{memStore: base, arm: true}
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{})

	if err := submitSync(t, rt, barEvent(bar(bar1))); err != nil {
		t.Fatal(err)
	}
	before := rt.Snapshot().Engine.Equity
	if err := submitSync(t, rt, quoteEvent(freshQuote(t0, 100, 100.1))); err == nil {
		t.Fatal("injected fill failure must surface")
	}
	s := rt.Snapshot()
	if s.Engine.Equity != before {
		t.Fatalf("equity drifted on failed fill: %v → %v", before, s.Engine.Equity)
	}
	if s.Health.State != HealthUnhealthy {
		t.Fatal("failed fill must degrade the runtime")
	}
}

// ── D. Recovery / warmup honesty ─────────────────────────────────────────

// Warmup reconstructs indicator state without inventing decisions, orders,
// or trades — and a persist failure on the marker is fatal (no divergence).
func TestRuntimeWarmupHonesty(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, nil, t0)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := e.Warmup(ctx, bar(bar1.Add(time.Duration(i)*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	if got := e.Snapshot().LastBarTime; got == nil || !got.Equal(bar1.Add(4*time.Hour)) {
		t.Fatalf("warmup marker %v", got)
	}
	orders, _ := store.ListOrders(ctx, "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	if len(orders) != 0 || len(store.Decisions()) != 0 {
		t.Fatal("warmup must never create decisions or orders")
	}
	// Marker persist failure → error surfaces and the durable marker does
	// NOT advance (bootstrap must treat it as fatal — no divergence).
	bad := &posFailStore{memStore: newMemStore()}
	e2 := mkEngine(bad, nil, t0)
	if err := e2.Warmup(ctx, bar(bar1)); err == nil {
		t.Fatal("warmup marker failure must surface")
	}
	if e2.Snapshot().LastBarTime != nil {
		t.Fatal("failed warmup must not advance the checkpoint")
	}
}

// ── E. Overload ───────────────────────────────────────────────────────────

// blockingStore stalls one CommitBar so the owner is busy while the mailbox
// fills — deterministic overload without sleeps.
type blockingStore struct {
	*memStore
	enter   chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingStore) CommitBar(ctx context.Context, c repository.ShadowBarCommit) (repository.ShadowBarResult, error) {
	b.once.Do(func() { close(b.enter); <-b.release })
	return b.memStore.CommitBar(ctx, c)
}

// Mailbox saturation: execution-relevant events are REFUSED (never silently
// dropped) and the runtime degrades — it cannot certify complete observation.
func TestRuntimeMailboxOverflowDegrades(t *testing.T) {
	store := &blockingStore{memStore: newMemStore(),
		enter: make(chan struct{}), release: make(chan struct{})}
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{MailboxSize: 1})

	// Event 1: admitted, owner picks it up and blocks inside CommitBar.
	if err := rt.Submit(context.Background(), barEvent(bar(bar1))); err != nil {
		t.Fatal(err)
	}
	<-store.enter // owner is now occupied
	// Event 2: fills the 1-slot mailbox.
	if err := rt.Submit(context.Background(), barEvent(bar(bar2))); err != nil {
		t.Fatal(err)
	}
	// Event 3: mailbox full → explicit rejection + runtime degrades.
	if err := rt.Submit(context.Background(), barEvent(bar(bar3))); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("expected ErrMailboxFull, got %v", err)
	}
	close(store.release)
	deadline := time.Now().Add(5 * time.Second)
	for rt.Snapshot().Health.State == HealthRunning && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	s := rt.Snapshot()
	if s.Health.State != HealthUnhealthy {
		t.Fatalf("overflow must degrade the runtime, got %q", s.Health.State)
	}
	if s.Stats.Rejected == 0 {
		t.Fatal("rejected events must be counted")
	}
}

// A tick dropped on a full mailbox does NOT degrade the runtime — deferring
// one expiry sweep loses nothing execution-relevant.
func TestRuntimeTickOverflowIsBenign(t *testing.T) {
	store := &blockingStore{memStore: newMemStore(),
		enter: make(chan struct{}), release: make(chan struct{})}
	e := mkEngine(store, map[time.Time]strategy.Action{bar1: strategy.ActionBuy}, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{MailboxSize: 1})
	if err := rt.Submit(context.Background(), barEvent(bar(bar1))); err != nil {
		t.Fatal(err)
	}
	<-store.enter
	if err := rt.Submit(context.Background(), barEvent(bar(bar2))); err != nil {
		t.Fatal(err)
	}
	if err := rt.Submit(context.Background(), tickEvent()); !errors.Is(err, ErrMailboxFull) {
		t.Fatalf("tick on full mailbox: %v", err)
	}
	close(store.release)
	deadline := time.Now().Add(5 * time.Second)
	for rt.Snapshot().Health.State == HealthRunning && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := rt.Snapshot().Health.State; got != HealthRunning {
		t.Fatalf("tick overflow degraded the runtime: %q", got)
	}
}

// ── F. Lifecycle ──────────────────────────────────────────────────────────

func TestRuntimeStopWithActiveProducers(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, nil, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			_ = rt.Submit(context.Background(), quoteEvent(freshQuote(t0.Add(time.Duration(i)*time.Millisecond), 100, 100.1)))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("stop under producers: %v", err)
	}
	<-done
	if rt.Snapshot().Health.State != HealthStopped {
		t.Fatal("runtime must report stopped")
	}
}

// Stop drains accepted work before returning — nothing admitted is lost.
func TestRuntimeStopDrainsAcceptedWork(t *testing.T) {
	store := newMemStore()
	e := mkEngine(store, map[time.Time]strategy.Action{
		bar1: strategy.ActionBuy, bar2: strategy.ActionSell}, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{})
	for _, c := range []market.Candle{bar(bar1), bar(bar2), bar(bar3)} {
		if err := rt.Submit(context.Background(), barEvent(c)); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	s := rt.Snapshot()
	if s.Stats.Submitted != 3 || s.Stats.Processed != 3 || s.Stats.Dropped != 0 {
		t.Fatalf("drain lost work: %+v", s.Stats)
	}
	if len(store.Decisions()) != 3 {
		t.Fatalf("decisions=%d want 3", len(store.Decisions()))
	}
}

// A drain deadline is honored AND reported — unprocessed events are counted
// dropped, never claimed durable.
func TestRuntimeStopDeadlineReported(t *testing.T) {
	store := &blockingStore{memStore: newMemStore(),
		enter: make(chan struct{}), release: make(chan struct{})}
	e := mkEngine(store, nil, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{})
	if err := rt.Submit(context.Background(), barEvent(bar(bar1))); err != nil {
		t.Fatal(err)
	}
	<-store.enter // owner blocked inside the store
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := rt.Stop(ctx); err == nil {
		t.Fatal("blocked drain must surface the deadline")
	}
	close(store.release) // let the owner finish for cleanup
}

// Second-owner refusal: loss of the ownership lease degrades the runtime.
type fakeOwnership struct {
	lost     chan struct{}
	released bool
}

func (f *fakeOwnership) Lost() <-chan struct{} { return f.lost }
func (f *fakeOwnership) Release(context.Context) error {
	f.released = true
	return nil
}

func TestRuntimeOwnershipLossDegrades(t *testing.T) {
	store := newMemStore()
	own := &fakeOwnership{lost: make(chan struct{})}
	e := mkEngine(store, nil, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{Ownership: own})
	if err := submitSync(t, rt, barEvent(bar(bar1))); err != nil {
		t.Fatal(err)
	}
	close(own.lost)
	deadline := time.Now().Add(5 * time.Second)
	for rt.Snapshot().Health.State == HealthRunning && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if rt.Snapshot().Health.State != HealthUnhealthy {
		t.Fatal("lost ownership must degrade the runtime")
	}
}

func TestRuntimeStopIsIdempotent(t *testing.T) {
	e := mkEngine(newMemStore(), nil, t0)
	rt := mkRuntime(e, RuntimeOptions{})
	rt.Start(context.Background(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rt.Stop(ctx); err != nil {
		t.Fatal("second Stop must return immediately")
	}
}

// Never-started runtimes still stop cleanly.
func TestRuntimeStopNeverStarted(t *testing.T) {
	e := mkEngine(newMemStore(), nil, t0)
	rt := mkRuntime(e, RuntimeOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("unstarted Stop: %v", err)
	}
	if rt.Snapshot().Health.State != HealthStopped {
		t.Fatal("unstarted runtime must report stopped")
	}
}

// Submissions on a cancelled context return the context error, not a lie.
func TestRuntimeSubmitHonorsContext(t *testing.T) {
	store := &blockingStore{memStore: newMemStore(),
		enter: make(chan struct{}), release: make(chan struct{})}
	e := mkEngine(store, nil, t0)
	rt := startTestRuntime(t, e, RuntimeOptions{MailboxSize: 1})
	if err := rt.Submit(context.Background(), barEvent(bar(bar1))); err != nil {
		t.Fatal(err)
	}
	<-store.enter
	if err := rt.Submit(context.Background(), barEvent(bar(bar2))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := rt.Submit(ctx, barEvent(bar(bar3))); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled submit: %v", err)
	}
	close(store.release)
}

// ── G. Determinism ────────────────────────────────────────────────────────

// Same committed initial state + ordered inputs + config + clock ⇒
// equivalent decisions and outcomes.
func TestRuntimeDeterministicOutcomes(t *testing.T) {
	run := func() (*memStore, RuntimeSnapshot) {
		store := newMemStore()
		e := mkEngine(store, map[time.Time]strategy.Action{
			bar1: strategy.ActionBuy, bar3: strategy.ActionSell}, t0)
		rt := mkRuntime(e, RuntimeOptions{Logger: discardLog()})
		rt.Start(context.Background(), nil)
		for _, ev := range []Event{
			barEvent(bar(bar1)),
			quoteEvent(freshQuote(t0, 100, 100.1)),
			barEvent(bar(bar2)),
			barEvent(bar(bar3)),
			quoteEvent(freshQuote(t0.Add(time.Second), 110, 110.1)),
		} {
			res := make(chan error, 1)
			ev.Result = res
			if err := rt.Submit(context.Background(), ev); err != nil {
				t.Fatal(err)
			}
			if err := <-res; err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rt.Stop(ctx); err != nil {
			t.Fatal(err)
		}
		return store, rt.Snapshot()
	}
	s1, snap1 := run()
	s2, snap2 := run()
	if snap1.Engine.Equity != snap2.Engine.Equity ||
		snap1.Engine.RealizedPnl != snap2.Engine.RealizedPnl {
		t.Fatalf("nondeterministic outcome: %+v vs %+v", snap1.Engine, snap2.Engine)
	}
	if len(s1.Decisions()) != len(s2.Decisions()) {
		t.Fatal("decision counts diverge")
	}
	o1, _ := s1.ListOrders(context.Background(), "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	o2, _ := s2.ListOrders(context.Background(), "ema-cross", "XAU/USD", market.Timeframe1h, 10)
	sort.Slice(o1, func(i, j int) bool { return o1[i].ID < o1[j].ID })
	sort.Slice(o2, func(i, j int) bool { return o2[i].ID < o2[j].ID })
	if len(o1) != len(o2) {
		t.Fatal("order counts diverge")
	}
	for i := range o1 {
		a, b := o1[i], o2[i]
		if a.Side != b.Side || a.Status != b.Status ||
			(a.FillPrice == nil) != (b.FillPrice == nil) ||
			(a.FillPrice != nil && *a.FillPrice != *b.FillPrice) {
			t.Fatalf("order %d diverged: %+v vs %+v", i, a, b)
		}
	}
}
