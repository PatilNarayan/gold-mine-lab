package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/shadow"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// ShadowDriver is the PRODUCER side of the shadow runtime: it translates
// provider inputs into runtime events and supplies the bootstrap routine
// that runs on the runtime's single owner goroutine. It never touches the
// engine directly — the runtime owns every mutation.
type ShadowDriver struct {
	rt        *shadow.Runtime
	provider  live.Provider
	data      market.HistoricalSource
	asset     market.Asset
	strat     strategy.Strategy
	cfg       shadow.Config
	sessions  SessionStore
	sessionID int64
	log       *slog.Logger
	poll      time.Duration
}

// SessionStore is the persistence boundary for session identity.
type SessionStore interface {
	GetOrCreateSession(ctx context.Context, sess repository.ShadowSessionRow) (*repository.ShadowSessionRow, error)
	TouchSession(ctx context.Context, id int64) error
}

func NewShadowDriver(rt *shadow.Runtime, provider live.Provider, data market.HistoricalSource,
	asset market.Asset, strat strategy.Strategy, cfg shadow.Config, sessions SessionStore, log *slog.Logger) *ShadowDriver {
	return &ShadowDriver{rt: rt, provider: provider, data: data, asset: asset,
		strat: strat, cfg: cfg, sessions: sessions, log: log, poll: 15 * time.Second}
}

// sessionConfig is the frozen engine/policy snapshot stored on the session —
// serialized from the ENGINE's effective config (defaults applied), never
// the caller's pre-default struct.
func (d *ShadowDriver) sessionConfig(eng *shadow.Engine) []byte {
	c := eng.EffectiveConfig()
	cfg, _ := json.Marshal(map[string]any{
		"initialEquity":   c.InitialEquity,
		"positionSizePct": c.PositionSizePct,
		"commissionPct":   c.CommissionPct,
		"fillTimeoutMs":   c.FillTimeout.Milliseconds(),
		"quoteMaxAgeMs":   c.QuoteMaxAge.Milliseconds(),
	})
	return cfg
}

// Bootstrap runs ON the runtime's owner goroutine before any live event is
// processed: session identity → persisted state restore → indicator warmup
// / downtime recovery. Any failure aborts boot and marks the runtime
// unhealthy — a partially-restored engine must never go live.
func (d *ShadowDriver) Bootstrap(ctx context.Context, eng *shadow.Engine) error {
	st, ok := d.strat.(strategy.Streamer)
	if !ok {
		return fmt.Errorf("strategy %s lacks streaming evaluation", d.strat.ID())
	}
	eng.SetStream(st.NewStream())
	// Session identity FIRST: resume the open session on restart
	// (restart_count++) or create a new one on first/config-changed start —
	// BEFORE Restore so session-scoped state (position/marker/P&L) loads
	// under the correct identity. Never silently resets.
	sess, err := d.sessions.GetOrCreateSession(ctx, repository.ShadowSessionRow{
		StrategyID:      d.cfg.StrategyID,
		StrategyVersion: d.cfg.StrategyVersion,
		Symbol:          d.cfg.Symbol,
		Timeframe:       string(d.cfg.Timeframe),
		Provider:        d.cfg.Provider,
		Config:          d.sessionConfig(eng),
	})
	if err != nil {
		return fmt.Errorf("session bootstrap: %w", err)
	}
	eng.SetSession(sess.ID)
	d.sessionID = sess.ID
	d.log.Info("shadow session", "id", sess.ID, "restarts", sess.RestartCount)
	if err := eng.Restore(ctx); err != nil {
		return fmt.Errorf("restore: %w", err)
	}

	// Warm the evaluator on stored history. Fresh start (no persisted
	// marker): feed closes to the stream and advance the durable marker —
	// a shadow engine must not act on signals that closed before it
	// existed. On restart, bars that closed during downtime go through the
	// RECOVERY path: the signal's decision anchor is its bar close, and
	// fills come only from admissible persisted quote evidence — never the
	// first live quote after restart, never backdated.
	lastBar := eng.Snapshot().LastBarTime
	if lastBar == nil {
		warmup, err := d.data.Tail(d.asset, eng.TF(), 400, time.Time{})
		if err != nil {
			return fmt.Errorf("warmup load: %w", err)
		}
		for _, c := range warmup {
			if err := eng.Warmup(ctx, c); err != nil {
				return fmt.Errorf("warmup persist at %s: %w", c.Time, err)
			}
		}
		return nil
	}
	// Restart: recover EVERYTHING since the persisted marker — Range
	// returns `warmup` bars strictly before `lastBar` (indicator warmup)
	// plus all stored closes after it (recovery decisions). Bounded by the
	// marker, never by an arbitrary window. Label-space bound: bar_time is
	// Kyiv-naive — NowLabel, not Now.
	candles, err := d.data.Range(d.asset, eng.TF(), *lastBar, live.NowLabel(), 200)
	if err != nil {
		return fmt.Errorf("recovery load: %w", err)
	}
	for _, c := range candles {
		if !c.Time.After(*lastBar) {
			eng.FeedWarmup(c) // indicator warmup — already processed
		} else if err := eng.ProcessMissedClose(ctx, c); err != nil {
			return fmt.Errorf("recovery bar %s: %w", c.Time, err)
		}
	}
	return nil
}

// Start attaches producers to the runtime and polls for newly closed bars
// until ctx ends. Producer calls never block the provider's delivery path:
// Submit is non-blocking; a rejection means the runtime has already
// degraded itself (mailbox overflow → unhealthy) or is shutting down.
func (d *ShadowDriver) Start(ctx context.Context) {
	unsub := d.provider.SubscribeQuotes(func(q live.Quote) {
		if err := d.rt.Submit(ctx, shadow.Event{Kind: shadow.EvQuote, Quote: q}); err != nil &&
			errors.Is(err, shadow.ErrMailboxFull) {
			d.log.Warn("shadow quote event rejected — mailbox saturated", "err", err)
		}
	})
	defer unsub()

	tick := time.NewTicker(d.poll)
	defer tick.Stop()
	tf := string(d.cfg.Timeframe)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if !d.rt.Healthy() {
				continue // runtime halted — producers idle, API still reports truth
			}
			if d.sessionID != 0 {
				if err := d.sessions.TouchSession(ctx, d.sessionID); err != nil {
					d.log.Debug("shadow session touch failed", "err", err)
				}
			}
			if err := d.rt.Submit(ctx, shadow.Event{Kind: shadow.EvTick}); err != nil {
				d.log.Debug("shadow tick rejected", "err", err)
			}
			bars, err := d.provider.Candles(ctx, tf, 3)
			if err != nil || len(bars) < 2 {
				continue
			}
			closed := bars[len(bars)-2] // newest CONFIRMED-closed bar
			cur := d.rt.Snapshot().Engine.LastBarTime
			if cur != nil && !closed.Time.After(*cur) {
				continue
			}
			c := market.Candle{Time: closed.Time, Open: closed.Open, High: closed.High,
				Low: closed.Low, Close: closed.Close}
			if closed.Volume != nil {
				c.Volume = *closed.Volume
			}
			if err := d.rt.Submit(ctx, shadow.Event{Kind: shadow.EvBarClosed, Candle: c}); err != nil {
				d.log.Debug("shadow bar event rejected", "bar", c.Time, "err", err)
			}
		}
	}
}
