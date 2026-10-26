package shadow

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository/postgres"
	"github.com/gold-mine-lab/backend/internal/strategy"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Integration: the REAL production engine + REAL postgres persistence —
// the full signal→order→quote→fill→exit→P&L path with replay/restart.
// Gated on GOLDMINE_TEST_DSN (skips when a test DB isn't configured);
// uses a dedicated test strategy identity so no real session is polluted.
func TestShadowIntegrationWithPostgres(t *testing.T) {
	dsn := os.Getenv("GOLDMINE_TEST_DSN")
	if dsn == "" {
		t.Skip("GOLDMINE_TEST_DSN not set — skipping postgres integration")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := postgres.New(pool)
	// Isolation: dedicated test identity, cleaned before/after.
	sid, ver, sym := "shadow-itest", "0.0.1", "XAU/USD"
	clean := func() {
		pool.Exec(ctx, `DELETE FROM shadow_orders WHERE strategy_id=$1`, sid)
		pool.Exec(ctx, `DELETE FROM shadow_positions WHERE strategy_id=$1`, sid)
	}
	clean()
	defer clean()

	cfg := Config{
		StrategyID: sid, StrategyVersion: ver, Symbol: sym,
		Timeframe: market.Timeframe1h, Provider: "tiingo",
		InitialEquity: 100000, PositionSizePct: 10, CommissionPct: 0.02,
		FillTimeout: time.Hour, QuoteMaxAge: 30 * time.Second,
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := bar1.Add(2 * time.Minute)
	eng := func() *Engine {
		e := New(cfg, store, log)
		e.SetClock(func() time.Time { return now })
		e.SetStream(&scriptedStream{actions: map[time.Time]strategy.Action{
			bar1: strategy.ActionBuy, bar2: strategy.ActionSell}})
		return e
	}

	e := eng()
	if err := e.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	// 1) confirmed close → BUY signal → exactly one persisted order.
	if err := e.OnBarClosed(ctx, bar(bar1)); err != nil {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM shadow_orders WHERE strategy_id=$1`, sid).Scan(&n)
	if n != 1 {
		t.Fatalf("orders=%d — expected exactly 1 persisted", n)
	}
	// 2) post-decision quote → entry fill at ASK.
	filled, err := e.OnQuote(ctx, freshQuote(now.Add(time.Second), 100.00, 100.10))
	if err != nil || !filled {
		t.Fatalf("entry fill failed: %v %v", filled, err)
	}
	pos, _ := store.GetPosition(ctx, sid, ver, sym, market.Timeframe1h)
	if pos == nil || pos.EntryPrice == nil || *pos.EntryPrice != 100.10 {
		t.Fatalf("persisted entry %+v — must be ask 100.10", pos)
	}
	// 3) exit signal → exit fill at BID → realized P&L reconciles.
	if err := e.OnBarClosed(ctx, bar(bar2)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.OnQuote(ctx, freshQuote(now.Add(3*time.Second), 110.00, 110.10)); err != nil {
		t.Fatal(err)
	}
	pos, _ = store.GetPosition(ctx, sid, ver, sym, market.Timeframe1h)
	if pos.Side != nil {
		t.Fatal("position still open after exit")
	}
	qty := 10000.0 / 100.10
	wantNet := (110.00-100.10)*qty - qty*100.10*0.0002 - qty*110.00*0.0002
	if d := pos.RealizedPnl - wantNet; d < -0.01 || d > 0.01 {
		t.Fatalf("realized %v want %v", pos.RealizedPnl, wantNet)
	}
	// 4) restart (new engine, same store) → no duplicate orders.
	e2 := eng()
	if err := e2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	e2.OnBarClosed(ctx, bar(bar2)) // replayed already-processed close
	pool.QueryRow(ctx, `SELECT count(*) FROM shadow_orders WHERE strategy_id=$1`, sid).Scan(&n)
	if n != 2 {
		t.Fatalf("orders=%d after restart+replay — expected exactly 2", n)
	}
}

// CoverageComplete scenario matrix — what the predicate PROVES about a
// window: recorder aliveness + heartbeat continuity + persistence +
// upstream feed health, all window-scoped.
func TestCoverageCompleteScenarios(t *testing.T) {
	dsn := os.Getenv("GOLDMINE_TEST_DSN")
	if dsn == "" {
		t.Skip("GOLDMINE_TEST_DSN not set — skipping postgres integration")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := postgres.New(pool)
	prov, sym := "itest-prov", "XAU/USD"
	defer pool.Exec(ctx, `DELETE FROM quote_coverage WHERE provider=$1`, prov)

	now := time.Now().UTC()
	from, to := now.Add(-2*time.Hour), now.Add(-time.Hour) // window: 2h..1h ago

	newRow := func(started time.Time, lastBeat time.Time) int64 {
		var id int64
		err := pool.QueryRow(ctx, `INSERT INTO quote_coverage
			(provider, symbol, started_at, last_heartbeat_at)
			VALUES ($1,$2,$3,$4) RETURNING id`, prov, sym, started, lastBeat).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	covered := func(tag string, want bool) {
		ok, err := store.CoverageComplete(ctx, prov, sym, from, to)
		if err != nil {
			t.Fatal(err)
		}
		if ok != want {
			t.Fatalf("%s: CoverageComplete=%v want %v", tag, ok, want)
		}
	}

	// 1) No coverage row → unknown (not proof of absence).
	covered("no row", false)

	// 2) Healthy spanning row → complete.
	id := newRow(from.Add(-time.Hour), to.Add(time.Minute))
	covered("healthy span", true)

	// 3) Row that ends before the window's end → incomplete.
	pool.Exec(ctx, `DELETE FROM quote_coverage WHERE id=$1`, id)
	newRow(from.Add(-time.Hour), to.Add(-time.Second)) // heartbeat died in-window
	covered("heartbeat before window end", false)

	// 4) Feed degraded INSIDE the window → incomplete.
	pool.Exec(ctx, `DELETE FROM quote_coverage WHERE provider=$1`, prov)
	id = newRow(from.Add(-time.Hour), to.Add(time.Minute))
	pool.Exec(ctx, `UPDATE quote_coverage SET last_feed_degraded_at=$2 WHERE id=$1`, id, to.Add(-time.Minute))
	covered("feed degraded in window", false)

	// 5) Degradation BEFORE the window → complete (window-scoped).
	pool.Exec(ctx, `UPDATE quote_coverage SET last_feed_degraded_at=$2 WHERE id=$1`, id, from.Add(-time.Hour))
	covered("degraded before window", true)

	// 6) A persistence drop inside the window → incomplete.
	pool.Exec(ctx, `UPDATE quote_coverage SET last_drop_at=$2 WHERE id=$1`, id, to.Add(-time.Second))
	covered("batch dropped in window", false)

	// 7) A heartbeat-gap ending inside the window → incomplete.
	pool.Exec(ctx, `UPDATE quote_coverage SET last_drop_at=NULL, last_heartbeat_gap_at=$2 WHERE id=$1`, id, to.Add(-time.Second))
	covered("heartbeat gap in window", false)

	// ── Transition events (migration 000017): between-heartbeat reality ──
	evt := func(kind string, at time.Time, since *time.Time) {
		_, err := pool.Exec(ctx, `INSERT INTO feed_events
			(coverage_id, provider, symbol, kind, at, since) VALUES ($1,$2,$3,$4,$5,$6)`,
			id, prov, sym, kind, at, since)
		if err != nil {
			t.Fatal(err)
		}
	}
	pool.Exec(ctx, `UPDATE quote_coverage SET last_heartbeat_gap_at=NULL WHERE id=$1`, id)
	pool.Exec(ctx, `DELETE FROM feed_events WHERE coverage_id=$1`, id)

	// 8) Disconnect SPANNING the window (disconnect before, never recovered)
	//    → incomplete: point-containment alone would miss this.
	before := from.Add(-30 * time.Minute)
	evt("disconnect", before, &before)
	covered("disconnect spanning window", false)

	// 9) + recovery before the window → interval closed → complete.
	evt("recovered", from.Add(-10*time.Minute), nil)
	covered("recovered before window", true)

	// 10) disconnect INSIDE window (with since before window) → incomplete.
	pool.Exec(ctx, `DELETE FROM feed_events WHERE coverage_id=$1`, id)
	evt("disconnect", to.Add(-time.Second), &before)
	covered("disconnect inside window", false)

	// 11) recovery inside window but outage started inside window → incomplete.
	evt("recovered", to.Add(-time.Second/2), nil)
	covered("in-window outage", false)

	// 12) persist_fail inside window → incomplete; before → complete.
	pool.Exec(ctx, `DELETE FROM feed_events WHERE coverage_id=$1`, id)
	evt("persist_fail", to.Add(-time.Second), nil)
	covered("persist_fail in window", false)
	pool.Exec(ctx, `DELETE FROM feed_events WHERE coverage_id=$1`, id)
	evt("persist_fail", from.Add(-time.Minute), nil)
	covered("persist_fail before window", true)
}
