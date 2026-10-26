package service

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/repository"
)

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type memQuoteRepo struct {
	mu   sync.Mutex
	rows []repository.QuoteObservationRow
}

func (m *memQuoteRepo) InsertQuoteObservations(_ context.Context, rows []repository.QuoteObservationRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, rows...)
	return nil
}
func (m *memQuoteRepo) CountQuoteObservations(context.Context, string) (int, error) {
	return len(m.rows), nil
}
func (m *memQuoteRepo) LatestQuoteObservation(context.Context, string) (*repository.QuoteObservationRow, error) {
	if len(m.rows) == 0 {
		return nil, nil
	}
	r := m.rows[len(m.rows)-1]
	return &r, nil
}

func f64p(v float64) *float64   { return &v }
func tp(t time.Time) *time.Time { return &t }

func quoteAt(t time.Time, bid, ask float64) live.Quote {
	return live.Quote{AssetID: "xauusd", Symbol: "XAU/USD",
		Price: (bid + ask) / 2, PriceKind: "mid",
		Bid: f64p(bid), Ask: f64p(ask), ProviderTime: tp(t),
		ReceivedAt: t.Add(10 * time.Millisecond)}
}

// Distinct quote events persist; identical redeliveries must not create
// rows; missing bid/ask stays NULL (never a fabricated zero spread).
func TestQuoteRecorderDedupeAndSpread(t *testing.T) {
	repo := &memQuoteRepo{}
	rec := NewQuoteRecorder(repo, "tiingo", "XAU/USD", testLogger())
	p := &fakeProvider{}
	rec.Start(context.Background(), p)

	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	p.push(quoteAt(base, 100.0, 100.1))                    // new
	p.push(quoteAt(base, 100.0, 100.1))                    // identical → dropped
	p.push(quoteAt(base.Add(time.Second), 100.0, 100.1))   // new provider time → kept
	p.push(quoteAt(base.Add(2*time.Second), 100.1, 100.2)) // new prices → kept
	p.push(live.Quote{AssetID: "xauusd", Symbol: "XAU/USD", Price: 100.15,
		PriceKind: "mid", ProviderTime: tp(base.Add(3 * time.Second)),
		ReceivedAt: base.Add(3*time.Second + 10*time.Millisecond)}) // no bid/ask
	rec.Stop() // flush on shutdown

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.rows) != 4 {
		t.Fatalf("rows=%d — expected 4 distinct observations", len(repo.rows))
	}
	first := repo.rows[0]
	if first.SpreadBp == nil || *first.SpreadBp <= 0 {
		t.Fatalf("spread not computed: %+v", first)
	}
	if first.Mid == nil || *first.Mid != 100.05 {
		t.Fatalf("mid=%v want 100.05", first.Mid)
	}
	last := repo.rows[3]
	if last.Bid != nil || last.Ask != nil || last.SpreadBp != nil {
		t.Fatalf("missing bid/ask must stay NULL, got %+v", last)
	}
	if last.Mid == nil || *last.Mid != 100.15 {
		t.Fatalf("mid without bid/ask should be the provider price 100.15, got %v", last.Mid)
	}
	for _, r := range repo.rows {
		if r.Stale {
			t.Fatalf("fresh observations marked stale: %+v", r)
		}
	}
}

// A quote whose provider_time lags receipt beyond the bound is flagged
// stale — not silently trusted as fresh.
func TestQuoteRecorderStalenessFlag(t *testing.T) {
	repo := &memQuoteRepo{}
	rec := NewQuoteRecorder(repo, "tiingo", "XAU/USD", testLogger())
	p := &fakeProvider{}
	rec.Start(context.Background(), p)

	old := time.Now().Add(-2 * time.Minute)
	p.push(live.Quote{AssetID: "xauusd", Symbol: "XAU/USD", Price: 100,
		PriceKind: "mid", Bid: f64p(99.9), Ask: f64p(100.1),
		ProviderTime: &old, ReceivedAt: time.Now()})
	rec.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if len(repo.rows) != 1 || !repo.rows[0].Stale {
		t.Fatalf("stale flag missing: %+v", repo.rows)
	}
}
