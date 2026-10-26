package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/shadow"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

// ShadowDriver feeds one shadow.Engine: confirmed-closed candles from the
// live provider's books and quote events from its stream. Stored candles
// are all complete (the REST sync never persists forming bars), so they
// both warm the evaluator and mark already-closed sessions.
type ShadowDriver struct {
	eng       *shadow.Engine
	provider  live.Provider
	data      market.HistoricalSource
	asset     market.Asset
	strat     strategy.Strategy
	cfg       shadow.Config
	sessions  SessionStore
	sessionID int64 // active session identity for TouchSession
	log       *slog.Logger
	poll      time.Duration
}

// SessionStore is the persistence boundary for session identity.
type SessionStore interface {
	GetOrCreateSession(ctx context.Context, sess repository.ShadowSessionRow) (*repository.ShadowSessionRow, error)
	TouchSession(ctx context.Context, id int64) error
}

func NewShadowDriver(eng *shadow.Engine, provider live.Provider, data market.HistoricalSource,
	asset market.Asset, strat strategy.Strategy, cfg shadow.Config, sessions SessionStore, log *slog.Logger) *ShadowDriver {
	return &ShadowDriver{eng: eng, provider: provider, data: data, asset: asset,
		strat: strat, cfg: cfg, sessions: sessions, log: log, poll: 15 * time.Second}
}

// sessionConfig is the frozen engine/policy snapshot stored on the session —
// serialized from the ENGINE's effective config (defaults applied), never
// the caller's pre-default struct.
func (d *ShadowDriver) sessionConfig() []byte {
	c := d.eng.EffectiveConfig()
	cfg, _ := json.Marshal(map[string]any{
		"initialEquity":   c.InitialEquity,
		"positionSizePct": c.PositionSizePct,
		"commissionPct":   c.CommissionPct,
		"fillTimeoutMs":   c.FillTimeout.Milliseconds(),
		"quoteMaxAgeMs":   c.QuoteMaxAge.Milliseconds(),
	})
	return cfg
}

// Start restores state, warms the evaluator from stored candles, subscribes
// to quotes, and polls for newly closed bars until ctx ends.
func (d *ShadowDriver) Start(ctx context.Context) {
	st, ok := d.strat.(strategy.Streamer)
	if !ok {
		d.log.Error("shadow: strategy lacks streaming evaluation — engine disabled", "strategy", d.strat.ID())
		return
	}
	stream := st.NewStream()
	d.eng.SetStream(stream)
	if err := d.eng.Restore(ctx); err != nil {
		d.log.Error("shadow restore failed — engine disabled", "err", err)
		return
	}
	// Session identity: resume the open session on restart (restart_count++),
	// create a new one on first/config-changed start. Never silently resets.
	if sess, err := d.sessions.GetOrCreateSession(ctx, repository.ShadowSessionRow{
		StrategyID:      d.cfg.StrategyID,
		StrategyVersion: d.cfg.StrategyVersion,
		Symbol:          d.cfg.Symbol,
		Timeframe:       string(d.cfg.Timeframe),
		Provider:        d.cfg.Provider,
		Config:          d.sessionConfig(),
	}); err != nil {
		d.log.Error("shadow session bootstrap failed — engine disabled", "err", err)
		return
	} else {
		d.eng.SetSession(sess.ID)
		d.sessionID = sess.ID
		d.log.Info("shadow session", "id", sess.ID, "restarts", sess.RestartCount)
	}

	// Warm the evaluator on stored history. Fresh start (no persisted
	// marker): feed closes to the stream only and advance the marker — a
	// shadow engine must not act on signals that closed before it existed.
	// On restart, bars that closed during downtime go through the RECOVERY
	// path: the signal's decision anchor is its bar close, and fills come
	// only from admissible persisted quote evidence — never the first live
	// quote after restart, never backdated.
	lastBar := d.eng.Snapshot().LastBarTime
	fresh := lastBar == nil
	if fresh {
		// Fresh start: warm the stream on recent stored closes only — no
		// decisions, no recovery (the engine didn't exist for those bars).
		warmup, err := d.data.Tail(d.asset, d.eng.TF(), 400, time.Time{})
		if err == nil {
			for _, c := range warmup {
				stream.Next(c)
				if err := d.eng.MarkBarProcessed(ctx, c.Time); err != nil {
					d.log.Warn("shadow marker persist failed", "bar", c.Time, "err", err)
				}
			}
		}
	} else {
		// Restart: recover EVERYTHING since the persisted marker — Range
		// returns `warmup` bars strictly before `lastBar` (indicator warmup)
		// plus all stored closes after it (recovery decisions). No depth cap
		// beyond what genuinely exists: a long outage recovers fully, bounded
		// by the marker, never by an arbitrary window.
		candles, err := d.data.Range(d.asset, d.eng.TF(), *lastBar, time.Now().UTC(), 200)
		if err == nil {
			for _, c := range candles {
				if !c.Time.After(*lastBar) {
					stream.Next(c) // indicator warmup — already processed
				} else if err := d.eng.ProcessMissedClose(ctx, c); err != nil {
					d.log.Warn("shadow recovery failed", "bar", c.Time, "err", err)
				}
			}
		} else {
			d.log.Warn("shadow recovery candle load failed — state preserved, will retry live", "err", err)
		}
	}

	unsub := d.provider.SubscribeQuotes(func(q live.Quote) {
		if _, err := d.eng.OnQuote(context.Background(), q); err != nil {
			d.log.Warn("shadow quote processing failed", "err", err)
		}
	})
	defer unsub()

	tick := time.NewTicker(d.poll)
	defer tick.Stop()
	tf := string(d.eng.TF())
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := d.eng.Tick(ctx); err != nil {
				d.log.Warn("shadow tick failed", "err", err)
			}
			bars, err := d.provider.Candles(ctx, tf, 3)
			if err != nil || len(bars) < 2 {
				continue
			}
			closed := bars[len(bars)-2] // newest CONFIRMED-closed bar
			cur := d.eng.Snapshot().LastBarTime
			if cur != nil && !closed.Time.After(*cur) {
				continue
			}
			c := market.Candle{Time: closed.Time, Open: closed.Open, High: closed.High,
				Low: closed.Low, Close: closed.Close}
			if closed.Volume != nil {
				c.Volume = *closed.Volume
			}
			if err := d.eng.OnBarClosed(ctx, c); err != nil {
				d.log.Warn("shadow bar processing failed", "bar", c.Time, "err", err)
			}
		}
	}
}
