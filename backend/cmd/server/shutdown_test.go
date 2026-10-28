package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/shadow"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// ── minimal shadow.Store fake ─────────────────────────────────────────────

type fakeStore struct {
	gate     chan struct{} // armed: CommitBar blocks until closed
	gateOnce sync.Once
	entered  chan struct{}
}

func newFakeStore() *fakeStore {
	return &fakeStore{gate: make(chan struct{}), entered: make(chan struct{})}
}

func (f *fakeStore) GetPendingOrder(context.Context, int64, string, string, string, market.Timeframe) (*repository.ShadowOrderRow, error) {
	return nil, nil
}
func (f *fakeStore) SetOrderStatus(context.Context, int64, string, string) error { return nil }
func (f *fakeStore) UpsertPosition(context.Context, repository.ShadowPositionRow) error {
	return nil
}
func (f *fakeStore) GetPosition(context.Context, int64, string, string, string, market.Timeframe) (*repository.ShadowPositionRow, error) {
	return nil, nil
}
func (f *fakeStore) ListOrders(context.Context, string, string, market.Timeframe, int) ([]repository.ShadowOrderRow, error) {
	return nil, nil
}
func (f *fakeStore) FillOrderAndPosition(context.Context, int64, repository.ShadowFillRecord, repository.ShadowPositionRow) error {
	return nil
}
func (f *fakeStore) CommitBar(context.Context, repository.ShadowBarCommit) (repository.ShadowBarResult, error) {
	if f.gate != nil {
		f.gateOnce.Do(func() { close(f.entered) })
		<-f.gate
	}
	return repository.ShadowBarResult{}, nil
}
func (f *fakeStore) FindQuoteInWindow(context.Context, string, string, time.Time, time.Time) (*repository.QuoteObservationRow, error) {
	return nil, nil
}
func (f *fakeStore) CoverageComplete(context.Context, string, string, time.Time, time.Time) (bool, error) {
	return false, nil
}
func (f *fakeStore) ListDecisions(context.Context, string, string, string, market.Timeframe, int) ([]repository.ShadowDecisionRow, error) {
	return nil, nil
}

func testEngine(store *fakeStore) *shadow.Engine {
	e := shadow.New(shadow.Config{
		StrategyID: "t", StrategyVersion: "1", Symbol: "XAU/USD",
		Timeframe: market.Timeframe1h, Provider: "t",
		InitialEquity: 1000, PositionSizePct: 1, CommissionPct: 0.01,
		FillTimeout: time.Minute, QuoteMaxAge: time.Minute,
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.SetStream(strategy.NewEMACross().NewStream())
	if err := e.Restore(context.Background()); err != nil {
		panic(err)
	}
	return e
}

// ── lifecycle regression tests ────────────────────────────────────────────

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// The single shutdown owner must run steps in the documented order:
// cancel producers → driver join → runtime drain → recorder → provider →
// DB last. A regression that lets provider/DB close first (the old LIFO
// defer bug) is caught here.
func TestOrderedShutdownSequence(t *testing.T) {
	order := []string{}
	store := newFakeStore()
	rt := shadow.NewRuntime(testEngine(store), shadow.RuntimeOptions{Logger: testLog()})
	rt.Start(context.Background(), nil)
	defer func() {
		close(store.gate) // release any blocked commit for cleanup
	}()

	driverDone := make(chan struct{})
	close(driverDone) // driver already exited

	cancel := func() { order = append(order, "cancel") }
	rec := func() { order = append(order, "recorder") }
	closeProvider := func() error {
		if rt.Snapshot().Health.State != shadow.HealthStopped {
			order = append(order, "provider-before-rt-stop")
		}
		order = append(order, "provider")
		return nil
	}
	closeDB := func() {
		order = append(order, "db")
	}

	orderedShutdown(testLog(), 5*time.Second, cancel, driverDone, rt, rec, closeProvider, closeDB)

	want := []string{"cancel", "recorder", "provider", "db"}
	if len(order) != len(want) {
		t.Fatalf("shutdown steps %v want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("shutdown order %v want %v", order, want)
		}
	}
	if rt.Snapshot().Health.State != shadow.HealthStopped {
		t.Fatal("runtime must be stopped when shutdown returns")
	}
}

// A runtime stuck inside a store call must not hang shutdown: the drain
// deadline expires, the warning path runs, and recorder/provider/DB still
// close — bounded, never silently "drained".
func TestOrderedShutdownBoundedOnBlockedRuntime(t *testing.T) {
	order := []string{}
	store := newFakeStore()
	rt := shadow.NewRuntime(testEngine(store), shadow.RuntimeOptions{Logger: testLog()})
	rt.Start(context.Background(), nil)
	defer close(store.gate)

	// Occupy the owner goroutine inside a blocked CommitBar.
	if err := rt.Submit(context.Background(), shadow.Event{
		Kind:   shadow.EvBarClosed,
		Candle: market.Candle{Time: time.Now(), Open: 1, High: 1, Low: 1, Close: 1},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owner never reached the blocked commit")
	}

	start := time.Now()
	orderedShutdown(testLog(), 100*time.Millisecond,
		func() { order = append(order, "cancel") }, nil, rt,
		func() { order = append(order, "recorder") },
		func() error { order = append(order, "provider"); return nil },
		func() { order = append(order, "db") })
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("shutdown hung on blocked runtime: %v", elapsed)
	}
	if len(order) == 0 || order[len(order)-1] != "db" {
		t.Fatalf("db must close last even on blocked drain: %v", order)
	}
	for _, want := range []string{"cancel", "recorder", "provider", "db"} {
		found := false
		for _, got := range order {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("step %q skipped on blocked runtime: %v", want, order)
		}
	}
	// The drain was NOT silent: the admitted event is counted submitted but
	// never processed — unresolved work is reported, not claimed durable.
	s := rt.Snapshot()
	if s.Stats.Processed != 0 || s.Stats.Submitted == 0 {
		t.Fatalf("blocked event must be admitted-but-unprocessed: %+v", s.Stats)
	}
}

// Partial startup (no runtime, no recorder, no provider) still shuts down.
func TestOrderedShutdownNilSafe(t *testing.T) {
	db := false
	orderedShutdown(testLog(), time.Second, func() {}, nil, nil, nil,
		func() error { return errors.New("close failed") }, func() { db = true })
	if !db {
		t.Fatal("provider close failure must not skip pool close")
	}
}
