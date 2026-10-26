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
