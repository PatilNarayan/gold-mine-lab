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
	repo     repository.QuoteObservationRepository
	provider string
	symbol   string
	log      *slog.Logger
	staleAt  time.Duration // provider_time lag beyond this → stale=true

	mu   sync.Mutex
	last obsKey
	seen bool

	in      chan repository.QuoteObservationRow
	cancel  context.CancelFunc
	done    chan struct{}
	dropped atomic.Int64 // quotes dropped when the buffer was full
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

func NewQuoteRecorder(repo repository.QuoteObservationRepository, provider, symbol string, log *slog.Logger) *QuoteRecorder {
	return &QuoteRecorder{repo: repo, provider: provider, symbol: symbol, log: log,
		staleAt: 30 * time.Second, in: make(chan repository.QuoteObservationRow, 512), done: make(chan struct{})}
}

// Dropped reports how many validated observations were lost to a full
// buffer — a real count, never hidden.
func (r *QuoteRecorder) Dropped() int64 { return r.dropped.Load() }

// Start subscribes to the provider and launches the flush worker.
// Returns the unsubscribe func; call Stop to release everything.
func (r *QuoteRecorder) Start(ctx context.Context, provider live.Provider) {
	ctx, r.cancel = context.WithCancel(ctx)
	unsub := provider.SubscribeQuotes(r.onQuote)
	go r.flushLoop(ctx, unsub)
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
	pending := make([]repository.QuoteObservationRow, 0, 256)

	flush := func() {
		if len(pending) == 0 {
			return
		}
		if err := r.repo.InsertQuoteObservations(context.Background(), pending); err != nil {
			r.log.Warn("quote observation insert failed — dropping batch",
				"count", len(pending), "err", err)
		}
		pending = pending[:0]
	}
	for {
		select {
		case row := <-r.in:
			pending = append(pending, row)
			if len(pending) >= 256 {
				flush()
			}
		case <-tick.C:
			flush()
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
