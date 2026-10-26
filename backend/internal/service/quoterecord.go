package service

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/repository"
)

// QuoteRecorder persists DISTINCT live quote observations carrying bid/ask —
// prospective market-cost evidence. Quotes arrive synchronously on the
// provider's delivery path, so the callback only validates + enqueues; a
// worker drains in batches. Identical (provider_time, bid, ask, price)
// redeliveries are dropped — a resend is not a new market event.
type QuoteRecorder struct {
	repo      repository.QuoteObservationRepository
	coverage  repository.QuoteCoverageRepository
	provider  string
	symbol    string
	log       *slog.Logger
	staleAt   time.Duration // provider_time lag beyond this → stale=true
	heartbeat time.Duration // coverage-interval heartbeat cadence

	mu   sync.Mutex
	last obsKey
	seen bool

	in      chan repository.QuoteObservationRow
	cancel  context.CancelFunc
	done    chan struct{}
	dropped atomic.Int64 // quotes dropped when the buffer was full

	covID          int64         // active coverage interval (0 = none)
	lastObsAt      atomic.Value  // time.Time — last persisted observation
	obsCount       atomic.Int64  // successfully persisted rows
	failedBatches  atomic.Int64  // persistence failures (degrades coverage)
	feed           live.Provider // sampled at each heartbeat for feed health
	lastDropAt     atomic.Value  // time.Time — last failed persistence batch
	lastDegradedAt atomic.Value  // time.Time — last unhealthy feed observation
	lastGapAt      atomic.Value  // time.Time — last detected heartbeat stall
}

// obsKey is a value-comparable dedupe fingerprint — pointers would compare
// by address, not content, and defeat deduplication.
type obsKey struct {
	providerTime time.Time
	bid, ask     float64
	hasTime      bool
	hasBid       bool
	hasAsk       bool
	price        float64
}

func keyOf(q live.Quote) obsKey {
	k := obsKey{price: q.Price}
	if q.ProviderTime != nil {
		k.providerTime, k.hasTime = *q.ProviderTime, true
	}
	if q.Bid != nil {
		k.bid, k.hasBid = *q.Bid, true
	}
	if q.Ask != nil {
		k.ask, k.hasAsk = *q.Ask, true
	}
	return k
}

func NewQuoteRecorder(repo repository.QuoteObservationRepository, coverage repository.QuoteCoverageRepository, provider, symbol string, log *slog.Logger) *QuoteRecorder {
	return &QuoteRecorder{repo: repo, coverage: coverage, provider: provider, symbol: symbol, log: log,
		staleAt: 30 * time.Second, heartbeat: 30 * time.Second,
		in: make(chan repository.QuoteObservationRow, 512), done: make(chan struct{})}
}

// Dropped reports how many validated observations were lost to a full
// buffer — a real count, never hidden.
func (r *QuoteRecorder) Dropped() int64 { return r.dropped.Load() }

// Start opens a coverage interval, subscribes to the provider, and launches
// the flush worker. Coverage heartbeats prove observation capability even
// when zero quotes arrive — "alive and persisting" ≠ "quotes existed".
func (r *QuoteRecorder) Start(ctx context.Context, provider live.Provider) {
	ctx, r.cancel = context.WithCancel(ctx)
	if r.coverage != nil {
		if id, err := r.coverage.StartCoverage(ctx, r.provider, r.symbol); err != nil {
			r.log.Warn("quote coverage start failed — intervals will show unknown coverage", "err", err)
		} else {
			r.covID = id
		}
	}
	r.feed = provider
	unsub := provider.SubscribeQuotes(r.onQuote)
	// Subscribe to provider STATE TRANSITIONS — the only honest record that
	// a disconnect happened between heartbeat samples. Optional capability;
	// providers expose it via a type-asserted hook so the interface stays
	// unchanged.
	if sp, ok := provider.(interface {
		SubscribeState(func(prev, next live.State, detail string)) func()
	}); ok {
		unsubState := sp.SubscribeState(r.onStateChange)
		old := unsub
		unsub = func() { old(); unsubState() }
	}
	go r.flushLoop(ctx, unsub)
}

// onStateChange persists feed transition events AT EVENT TIME — a
// disconnect between heartbeat samples still lands in the audit trail.
// 'since' bounds the degraded interval's start at the last proven-healthy
// evidence (last persisted quote receipt; else the provider's own newest
// quote time) — never an invented disconnect instant.
func (r *QuoteRecorder) onStateChange(prev, next live.State, detail string) {
	if r.coverage == nil || r.covID == 0 {
		return
	}
	degraded := (prev == live.StateConnected) &&
		(next == live.StateDisconnected || next == live.StateConnecting ||
			next == live.StateConfigError)
	recovered := next == live.StateConnected &&
		(prev == live.StateDisconnected || prev == live.StateConnecting)
	if !degraded && !recovered {
		return
	}
	kind := "recovered"
	var since *time.Time
	if degraded {
		kind = "disconnect"
		now := time.Now().UTC()
		r.lastDegradedAt.Store(now)
		if v := r.lastObsAt.Load(); v != nil {
			if t, ok := v.(time.Time); ok {
				since = &t
			}
		}
		if since == nil {
			if st := r.feed.Status(); st.LastQuoteAt != nil {
				since = st.LastQuoteAt
			}
		}
	}
	ev := repository.FeedEventRow{
		CoverageID: r.covID, Provider: r.provider, Symbol: r.symbol,
		Kind: kind, At: time.Now().UTC(), Since: since, Detail: detail,
	}
	if err := r.coverage.InsertFeedEvent(context.Background(), ev); err != nil {
		r.log.Debug("feed event insert failed", "kind", kind, "err", err)
	}
}

func (r *QuoteRecorder) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	<-r.done
}

// onQuote runs on the provider's synchronous delivery path — cheap only:
// validate, dedupe, enqueue. Never blocks.
func (r *QuoteRecorder) onQuote(q live.Quote) {
	k := keyOf(q)
	r.mu.Lock()
	if r.seen && r.last == k {
		r.mu.Unlock()
		return
	}
	r.seen, r.last = true, k
	r.mu.Unlock()

	receivedAt := q.ReceivedAt
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	row := repository.QuoteObservationRow{
		Provider: r.provider, Symbol: r.symbol,
		ProviderTime: q.ProviderTime, Bid: q.Bid, Ask: q.Ask,
		ReceivedAt: receivedAt,
	}
	// Mid + spread: present only when BOTH legs exist — missing bid/ask is
	// NULL, never a fabricated zero spread.
	mid := q.Price
	if q.Bid != nil && q.Ask != nil && *q.Ask >= *q.Bid {
		mid = (*q.Bid + *q.Ask) / 2
		spread := *q.Ask - *q.Bid
		row.SpreadPrice = &spread
		if mid > 0 {
			bp := spread / mid * 10000
			row.SpreadBp = &bp
		}
	}
	row.Mid = &mid
	if q.ProviderTime != nil && row.ReceivedAt.Sub(*q.ProviderTime) > r.staleAt {
		row.Stale = true
	}
	select {
	case r.in <- row:
	default:
		r.dropped.Add(1)
	}
}

// flushLoop batches queued observations into group inserts; on shutdown it
// drains the buffer before exiting so accepted observations aren't lost.
func (r *QuoteRecorder) flushLoop(ctx context.Context, unsub func()) {
	defer close(r.done)
	defer unsub()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	beat := time.NewTicker(r.heartbeat)
	defer beat.Stop()
	pending := make([]repository.QuoteObservationRow, 0, 256)

	var lastBeat time.Time // heartbeat self-continuity detector
	beatFn := func() {
		if r.coverage == nil || r.covID == 0 {
			return
		}
		now := time.Now().UTC()
		// Stall self-detection: a beat arriving >2×cadence late means the
		// process couldn't certify aliveness for the elapsed interval —
		// record it so those windows stay unobservable.
		if !lastBeat.IsZero() && now.Sub(lastBeat) > 2*r.heartbeat {
			r.lastGapAt.Store(now)
			r.log.Warn("coverage heartbeat gap detected", "gap", now.Sub(lastBeat))
		}
		lastBeat = now
		// Feed health: the managed provider's OWN snapshot (state +
		// newest-quote freshness) — a live recorder over a dead feed is
		// degraded coverage, not proof of observability.
		feedOK, feedState := true, "none"
		if r.feed != nil {
			st := r.feed.Status()
			feedState = string(st.State)
			staleAfter := st.StaleAfter
			if staleAfter <= 0 {
				staleAfter = r.staleAt
			}
			feedOK = st.State == live.StateConnected &&
				st.LastQuoteAt != nil && now.Sub(*st.LastQuoteAt) <= staleAfter
		}
		if !feedOK {
			r.lastDegradedAt.Store(now)
		}
		sample := repository.CoverageSample{
			ObsCount: r.obsCount.Load(), FeedState: feedState,
			DroppedBatches: int(r.failedBatches.Load()),
		}
		if v := r.lastObsAt.Load(); v != nil {
			if t, ok := v.(time.Time); ok {
				sample.LastObsAt = &t
			}
		}
		if v := r.lastDropAt.Load(); v != nil {
			if t, ok := v.(time.Time); ok {
				sample.LastDropAt = &t
			}
		}
		if v := r.lastDegradedAt.Load(); v != nil {
			if t, ok := v.(time.Time); ok {
				sample.LastFeedDegradedAt = &t
			}
		}
		if v := r.lastGapAt.Load(); v != nil {
			if t, ok := v.(time.Time); ok {
				sample.LastHeartbeatGapAt = &t
			}
		}
		if err := r.coverage.HeartbeatCoverage(context.Background(), r.covID, sample); err != nil {
			r.log.Debug("coverage heartbeat failed", "err", err)
		}
	}

	flush := func() {
		if len(pending) == 0 {
			return
		}
		if err := r.repo.InsertQuoteObservations(context.Background(), pending); err != nil {
			r.log.Warn("quote observation insert failed — dropping batch",
				"count", len(pending), "err", err)
			r.failedBatches.Add(1)
			r.lastDropAt.Store(time.Now().UTC())
			if r.coverage != nil && r.covID != 0 {
				t := time.Now().UTC()
				_ = r.coverage.InsertFeedEvent(context.Background(), repository.FeedEventRow{
					CoverageID: r.covID, Provider: r.provider, Symbol: r.symbol,
					Kind: "persist_fail", At: t, Since: &t, Detail: err.Error(),
				})
			}
		} else {
			r.obsCount.Add(int64(len(pending)))
			last := pending[len(pending)-1].ReceivedAt
			r.lastObsAt.Store(last)
		}
		pending = pending[:0]
	}
	defer func() {
		// Close the interval honestly: ended_at records clean shutdown;
		// a crash leaves ended_at NULL and the heartbeat goes stale —
		// either way, windows past the heartbeat show unknown coverage.
		if r.coverage != nil && r.covID != 0 {
			if err := r.coverage.EndCoverage(context.Background(), r.covID); err != nil {
				r.log.Debug("coverage close failed", "err", err)
			}
		}
	}()

	for {
		select {
		case row := <-r.in:
			pending = append(pending, row)
			if len(pending) >= 256 {
				flush()
			}
		case <-tick.C:
			flush()
		case <-beat.C:
			beatFn()
		case <-ctx.Done():
			// Drain what remains — accepted observations must not vanish.
			for {
				select {
				case row := <-r.in:
					pending = append(pending, row)
				default:
					flush()
					return
				}
			}
		}
	}
}
