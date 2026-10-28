// Single-writer runtime: exactly one owner goroutine mutates the Engine.
// Producers (provider quote callbacks, the driver's bar/tick poll, timers)
// submit Events to a bounded mailbox; HTTP-facing state comes only from the
// published, immutable RuntimeSnapshot.
//
// Backpressure policy (documented, deliberate): admission is NON-BLOCKING.
// Provider callbacks run on the upstream delivery path and must never stall
// it, so a full mailbox REJECTS the event — and, for execution-relevant
// kinds (quotes, bar closes), the runtime degrades to unhealthy: a dropped
// quote may have been the first admissible fill, and continuing would let
// the ledger report evidence coverage the runtime never observed. A full
// mailbox carrying only a Tick merely delays one expiry sweep, so ticks are
// rejected without degrading.
//
// Failure semantics: any persistence error processing an event marks the
// runtime unhealthy and HALTS further strategy/execution processing —
// PostgreSQL rollback does not roll back an advanced indicator stream, so
// in-process continuation is unsound. Recovery = process restart, which
// rebuilds from committed state (Restore + recovery replay). The failed
// transition is never published as committed.
//
// Ordering: mailbox order IS the processing order (sequence). Same-producer
// events keep FIFO; cross-producer interleaving is arrival order. Market
// semantics (bar_time labels, provider timestamps, decision anchors) are
// enforced inside the engine, not by arrival — see engine.go.
package shadow

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
)

// EventKind identifies a producer input to the runtime.
type EventKind int

const (
	// EvQuote is one provider tick (real instant timestamps).
	EvQuote EventKind = iota
	// EvBarClosed is one confirmed-closed candle (bar label timestamp).
	EvBarClosed
	// EvTick is the periodic maintenance sweep (pending-order expiry).
	EvTick
)

// Event is one mailbox item. Result, when non-nil with capacity ≥ 1,
// receives the processing outcome (nil on success) — producers needing only
// admission semantics leave it nil; tests use it for determinism.
type Event struct {
	Kind   EventKind
	Quote  live.Quote
	Candle market.Candle
	Result chan error
}

// Admission rejections — callers distinguish accepted from refused events.
var (
	ErrMailboxFull      = errors.New("shadow runtime mailbox full — event rejected")
	ErrRuntimeStopped   = errors.New("shadow runtime stopped — event rejected")
	ErrRuntimeUnhealthy = errors.New("shadow runtime unhealthy — event rejected")
)

// HealthState is the runtime's operational state — deliberately separate
// from committed engine state: a persistence failure must never look like
// a healthy idle runtime.
type HealthState string

const (
	HealthStarting  HealthState = "starting"  // boot/replay in progress
	HealthRunning   HealthState = "running"   // processing normally
	HealthUnhealthy HealthState = "unhealthy" // halted; restart required
	HealthStopped   HealthState = "stopped"   // shut down (owner exited)
)

// Health is the operational-state record published with each snapshot.
type Health struct {
	State  HealthState
	Reason string
	Since  time.Time
}

// RuntimeStats are honest admission/processing counters.
type RuntimeStats struct {
	Submitted int64 `json:"submitted"` // events admitted to the mailbox
	Processed int64 `json:"processed"` // events executed by the owner
	Rejected  int64 `json:"rejected"`  // submissions refused (full/unhealthy/stopped)
	Dropped   int64 `json:"dropped"`   // admitted events never processed
}

// RuntimeSnapshot is the immutable, concurrency-safe API view: committed
// engine state + operational health + admission counters, deep-copied at
// publish time — consumers can never mutate engine internals through it.
type RuntimeSnapshot struct {
	Engine  Snapshot
	Metrics Metrics
	Health  Health
	Stats   RuntimeStats
}

// Ownership is the exclusive-runtime lease (Postgres advisory lock in
// production). Lost closes when the lease dies underneath us — the runtime
// must degrade rather than risk a second live writer.
type Ownership interface {
	Lost() <-chan struct{}
	Release(ctx context.Context) error
}

// OwnershipKey derives the stable advisory-lock identity for a runtime's
// configuration — namespaced so it can never collide with other lock users.
func OwnershipKey(c Config) int64 {
	h := fnv.New64a()
	fmt.Fprintf(h, "gml-shadow|%s|%s|%s|%s|%s",
		c.StrategyID, c.StrategyVersion, c.Symbol, c.Timeframe, c.Provider)
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

// RuntimeOptions tunes a Runtime — all optional.
type RuntimeOptions struct {
	MailboxSize int // default 1024
	Ownership   Ownership
	Logger      *slog.Logger
	Now         func() time.Time // clock injection for tests
}

// Runtime owns one Engine. Construct once; Start launches the single owner
// goroutine; Stop is idempotent and drains admitted work.
type Runtime struct {
	eng     *Engine
	mailbox chan Event
	log     *slog.Logger
	own     Ownership
	now     func() time.Time

	snap   atomic.Value // RuntimeSnapshot
	health atomic.Value // Health
	stats  runtimeStats

	stopping   atomic.Bool // admission closed
	started    atomic.Bool // owner goroutine exists
	stopCtx    chan context.Context
	stopped    chan struct{}
	stopOnce   sync.Once
	finishOnce sync.Once
}

type runtimeStats struct {
	submitted, processed, rejected, dropped atomic.Int64
}

func NewRuntime(eng *Engine, opts RuntimeOptions) *Runtime {
	if opts.MailboxSize <= 0 {
		opts.MailboxSize = 1024
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	r := &Runtime{
		eng: eng, mailbox: make(chan Event, opts.MailboxSize),
		log: opts.Logger, own: opts.Ownership, now: opts.Now,
		stopCtx: make(chan context.Context, 1), stopped: make(chan struct{}),
	}
	r.health.Store(Health{State: HealthStarting, Since: opts.Now().UTC()})
	r.publish()
	return r
}

// Start launches the single owner goroutine — idempotent. bootstrap, when
// non-nil, runs ON the owner goroutine BEFORE any event is consumed, so
// session resolution, state restore, warmup and recovery replay are
// serialized ahead of live input by construction.
func (r *Runtime) Start(ctx context.Context, bootstrap func(context.Context, *Engine) error) {
	if !r.started.CompareAndSwap(false, true) {
		return
	}
	go r.run(ctx, bootstrap)
}

// Submit admits one event. It NEVER blocks a producer on a full mailbox:
// full → the event is rejected and (for quote/bar kinds) the runtime
// degrades — correctness cannot be maintained past a lost observation.
func (r *Runtime) Submit(ctx context.Context, ev Event) error {
	if r.stopping.Load() {
		r.stats.rejected.Add(1)
		return ErrRuntimeStopped
	}
	if r.health.Load().(Health).State == HealthUnhealthy {
		r.stats.rejected.Add(1)
		return ErrRuntimeUnhealthy
	}
	select {
	case r.mailbox <- ev:
		r.stats.submitted.Add(1)
		return nil
	case <-ctx.Done():
		r.stats.rejected.Add(1)
		return ctx.Err()
	default:
	}
	r.stats.rejected.Add(1)
	if ev.Kind == EvTick {
		return ErrMailboxFull // cosmetic — one expiry sweep is safely deferrable
	}
	r.fail(fmt.Errorf("mailbox overflow: dropped %v event — cannot certify complete observation", ev.Kind))
	return ErrMailboxFull
}

// Healthy reports whether the owner is processing events.
func (r *Runtime) Healthy() bool {
	return r.health.Load().(Health).State == HealthRunning
}

// Health returns the current operational state.
func (r *Runtime) Health() Health { return r.health.Load().(Health) }

// Snapshot returns a deep copy of the latest published snapshot — safe to
// hold, safe to read AND mutate from any goroutine: consumers can never
// reach engine internals or corrupt the shared published copy through it.
func (r *Runtime) Snapshot() RuntimeSnapshot {
	s := r.snap.Load().(RuntimeSnapshot)
	if s.Engine.Position != nil {
		p := *s.Engine.Position
		s.Engine.Position = &p
	}
	if s.Engine.Pending != nil {
		o := *s.Engine.Pending
		s.Engine.Pending = &o
	}
	return s
}

// Stop closes admission, drains admitted work while the runtime is healthy
// (or until ctx expires), then joins the owner goroutine. Idempotent;
// reports ctx.Err() when the drain deadline passed with work outstanding —
// the remaining events are counted in Stats.Dropped, never claimed durable.
func (r *Runtime) Stop(ctx context.Context) error {
	r.stopOnce.Do(func() {
		r.stopping.Store(true)
		if !r.started.Load() {
			r.setHealth(HealthStopped, "never started")
			r.publish()
			r.finish()
			return
		}
		r.stopCtx <- ctx
	})
	select {
	case <-r.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) finish() {
	r.finishOnce.Do(func() { close(r.stopped) })
}

func (r *Runtime) setHealth(s HealthState, reason string) {
	r.health.Store(Health{State: s, Reason: reason, Since: r.now().UTC()})
}

// fail marks the runtime unhealthy and halts processing. In-memory engine
// state may be partially ahead of durable state after a failed commit, so
// the only sound continuation is a restart that rebuilds from Postgres.
func (r *Runtime) fail(err error) {
	r.setHealth(HealthUnhealthy, err.Error())
	r.publish()
	r.log.Error("shadow runtime unhealthy — halted until restart (durable state is authoritative)",
		"err", err)
}

func (r *Runtime) publish() {
	r.snap.Store(RuntimeSnapshot{
		Engine:  r.eng.Snapshot(),
		Metrics: r.eng.MetricsSnapshot(),
		Health:  r.health.Load().(Health),
		Stats: RuntimeStats{
			Submitted: r.stats.submitted.Load(), Processed: r.stats.processed.Load(),
			Rejected: r.stats.rejected.Load(), Dropped: r.stats.dropped.Load(),
		},
	})
}

func (r *Runtime) run(ctx context.Context, bootstrap func(context.Context, *Engine) error) {
	defer r.finish()
	if r.own != nil {
		defer func() {
			rc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.own.Release(rc); err != nil {
				r.log.Warn("shadow ownership release failed", "err", err)
			}
		}()
	}
	var lost <-chan struct{}
	if r.own != nil {
		lost = r.own.Lost()
	}
	if bootstrap != nil {
		if err := bootstrap(ctx, r.eng); err != nil {
			r.fail(fmt.Errorf("bootstrap: %w", err))
		}
	}
	if r.Health().State == HealthStarting {
		r.setHealth(HealthRunning, "")
	}
	r.publish()
	for {
		if r.Healthy() {
			select {
			case ev := <-r.mailbox:
				r.process(ctx, ev)
			case sc := <-r.stopCtx:
				r.drain(sc)
				return
			case <-lost:
				r.fail(errors.New("runtime ownership lost — another writer may hold the lease"))
			case <-ctx.Done():
				r.drain(ctx)
				return
			}
			continue
		}
		// Unhealthy: drain-and-discard so producers see a bounded mailbox and
		// nothing half-processes; the admission path already rejects.
		select {
		case <-r.mailbox:
			r.stats.dropped.Add(1)
			r.publish()
		case sc := <-r.stopCtx:
			r.drain(sc)
			return
		case <-ctx.Done():
			r.drain(ctx)
			return
		}
	}
}

// process executes ONE event on the owner goroutine and republishes state.
// An engine error degrades the runtime — the failed transition is never
// published as committed (engine state is only mutated post-commit).
func (r *Runtime) process(ctx context.Context, ev Event) {
	var err error
	switch ev.Kind {
	case EvQuote:
		_, err = r.eng.OnQuote(ctx, ev.Quote)
	case EvBarClosed:
		err = r.eng.OnBarClosed(ctx, ev.Candle)
	case EvTick:
		err = r.eng.Tick(ctx)
	default:
		err = fmt.Errorf("unknown event kind %d", ev.Kind)
	}
	r.stats.processed.Add(1)
	if ev.Result != nil {
		select {
		case ev.Result <- err:
		default:
		}
	}
	if err != nil {
		r.fail(err)
	}
	r.publish()
}

// drain finishes admitted work while healthy; anything left when the
// deadline passes or the runtime is unhealthy is counted dropped — never
// claimed durable.
func (r *Runtime) drain(ctx context.Context) {
	defer r.publish()
	for r.Healthy() {
		select {
		case ev := <-r.mailbox:
			r.process(ctx, ev)
		default:
			r.setHealth(HealthStopped, "shutdown")
			return
		case <-ctx.Done():
			r.stats.dropped.Add(int64(len(r.mailbox)))
			r.setHealth(HealthStopped, "shutdown deadline — admitted events left unprocessed")
			return
		}
	}
	r.stats.dropped.Add(int64(len(r.mailbox)))
	if r.Health().State != HealthStopped {
		r.setHealth(HealthStopped, "shutdown")
	}
}
