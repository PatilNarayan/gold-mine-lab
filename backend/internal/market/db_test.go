package market

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

// memStore is the deterministic CandleStore double — production uses
// postgres.CandleStore; tests must never need a real database.
type memStore struct {
	candles map[string][]Candle
	cov     map[string]Coverage
	setCov  int
}

func newMemStore() *memStore {
	return &memStore{candles: map[string][]Candle{}, cov: map[string]Coverage{}}
}

func key(assetID string, tf Timeframe) string { return assetID + "|" + string(tf) }

func (s *memStore) UpsertCandles(_ context.Context, assetID string, tf Timeframe, _ string, cs []Candle) error {
	k := key(assetID, tf)
	merged := append(append([]Candle{}, s.candles[k]...), cs...)
	sort.Slice(merged, func(i, j int) bool { return merged[i].Time.Before(merged[j].Time) })
	out := merged[:0]
	for _, c := range merged {
		if len(out) > 0 && out[len(out)-1].Time.Equal(c.Time) {
			out[len(out)-1] = c
			continue
		}
		out = append(out, c)
	}
	s.candles[k] = out
	return nil
}

func (s *memStore) LoadCandles(_ context.Context, assetID string, tf Timeframe, from, to time.Time) ([]Candle, error) {
	var out []Candle
	for _, c := range s.candles[key(assetID, tf)] {
		if !c.Time.Before(from) && c.Time.Before(to) {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *memStore) TailCandles(_ context.Context, assetID string, tf Timeframe, n int, before time.Time) ([]Candle, error) {
	var sel []Candle
	for _, c := range s.candles[key(assetID, tf)] {
		if !c.Time.After(before) {
			sel = append(sel, c)
		}
	}
	if len(sel) > n {
		sel = sel[len(sel)-n:]
	}
	return sel, nil
}

func (s *memStore) Coverage(_ context.Context, assetID string, tf Timeframe) (Coverage, error) {
	return s.cov[key(assetID, tf)], nil
}

func (s *memStore) SetCoverage(_ context.Context, assetID string, tf Timeframe, cov Coverage) error {
	s.setCov++
	s.cov[key(assetID, tf)] = cov
	return nil
}

var testAsset = Asset{ID: "xauusd", Symbol: "XAUUSD", QuoteCurrency: "USD"}

func bars(from time.Time, n int, step time.Duration) []Candle {
	out := make([]Candle, n)
	for i := range out {
		out[i] = Candle{Time: from.Add(time.Duration(i) * step), Open: 1, High: 2, Low: 0.5, Close: 1.5}
	}
	return out
}

// dayPages produces a HistoryFunc emitting `n` daily pages then stopping.
func dayPages(failAt int, failErr error, calls *[]time.Time) HistoryFunc {
	return func(_ context.Context, tf Timeframe, from, to time.Time,
		onPage func([]Candle, time.Time, time.Time) error) error {
		n := 0
		for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
			n++
			if calls != nil {
				*calls = append(*calls, d)
			}
			if failAt > 0 && n == failAt {
				return failErr
			}
			if err := onPage(bars(d, 1, 0), d, d.AddDate(0, 0, 1)); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestDBProviderSyncsOnRead(t *testing.T) {
	store := newMemStore()
	floor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var calls []time.Time
	p := NewDBProvider(store, "tiingo", floor, dayPages(0, nil, &calls))

	from := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)
	cs, err := p.Range(testAsset, Timeframe1d, from, to, 0)
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	if len(cs) != 59 {
		t.Fatalf("Range = %d candles, want 59", len(cs))
	}
	cov, _ := store.Coverage(context.Background(), testAsset.ID, Timeframe1d)
	if cov.First != from || cov.State != SyncComplete {
		t.Fatalf("coverage = %+v", cov)
	}
	if _, err := p.Range(testAsset, Timeframe1d, from, to, 0); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 59 {
		t.Fatalf("second read triggered %d extra provider calls", len(calls)-59)
	}
}

func TestDBProviderClampsToProviderFloor(t *testing.T) {
	store := newMemStore()
	floor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var calls []time.Time
	p := NewDBProvider(store, "tiingo", floor, dayPages(0, nil, &calls))
	pre := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2020, 1, 10, 0, 0, 0, 0, time.UTC)
	if _, err := p.Range(testAsset, Timeframe1d, pre, to, 0); err != nil {
		t.Fatal(err)
	}
	if calls[0] != floor {
		t.Fatalf("fetch started at %v — expected clamp to floor %v", calls[0], floor)
	}
	cov, _ := store.Coverage(context.Background(), testAsset.ID, Timeframe1d)
	if cov.First != floor {
		t.Fatalf("coverage.First = %v, want %v", cov.First, floor)
	}
}

func TestDBProviderNeverMixesProviders(t *testing.T) {
	store := newMemStore()
	store.cov[key("xauusd", Timeframe1d)] = Coverage{
		Provider: "twelvedata", Count: 5,
		First: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		Last:  time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	called := false
	p := NewDBProvider(store, "tiingo", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		func(context.Context, Timeframe, time.Time, time.Time, func([]Candle, time.Time, time.Time) error) error {
			called = true
			return nil
		})
	to := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := p.Range(testAsset, Timeframe1d, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), to, 0); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("foreign-provider coverage was extended — sources must never mix")
	}
}

func TestDBProviderReadOnlyWhenNoHistory(t *testing.T) {
	store := newMemStore()
	store.candles[key("xauusd", Timeframe1d)] = bars(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), 4, 24*time.Hour)
	p := NewDBProvider(store, "tiingo", time.Time{}, nil)
	cs, err := p.Tail(testAsset, Timeframe1d, 10, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 4 {
		t.Fatalf("Tail = %d, want 4", len(cs))
	}
	cs2, err := p.Tail(testAsset, Timeframe1h, 10, time.Time{})
	if err != nil || len(cs2) != 0 {
		t.Fatalf("empty tail = %v, %v", cs2, err)
	}
}

// rateLimit simulates live.RateLimitError's contract without importing live.
type rateLimit struct{ retry time.Duration }

func (r *rateLimit) Error() string                      { return "provider rate limit exceeded (429)" }
func (r *rateLimit) RateLimitRetryAfter() time.Duration { return r.retry }

func TestDBProviderHonorsRetryAfterAndPersistsGate(t *testing.T) {
	store := newMemStore()
	floor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var calls int
	p := NewDBProvider(store, "tiingo", floor,
		func(context.Context, Timeframe, time.Time, time.Time, func([]Candle, time.Time, time.Time) error) error {
			calls++
			return &rateLimit{retry: 2 * time.Hour}
		})
	p.now = func() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }

	from := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2021, 2, 1, 0, 0, 0, 0, time.UTC)
	if _, err := p.Range(testAsset, Timeframe1d, from, to, 0); err != nil {
		t.Fatal(err) // read path must still succeed — serves stored data
	}
	cov, _ := store.Coverage(context.Background(), testAsset.ID, Timeframe1d)
	if cov.State != SyncRateLimited {
		t.Fatalf("state = %q, want rate_limited", cov.State)
	}
	// Retry gate persisted at least the provider's 2h Retry-After.
	if cov.RetryAt.Before(p.now().Add(2 * time.Hour)) {
		t.Fatalf("retry_at = %v — provider guidance not honored", cov.RetryAt)
	}
	// Subsequent reads inside the gate make ZERO provider calls.
	for i := 0; i < 5; i++ {
		if _, err := p.Range(testAsset, Timeframe1d, from, to, 0); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("retry-gated reads still called provider %d times", calls)
	}
	// After the gate expires a sync is attempted again.
	p.now = func() time.Time { return time.Date(2026, 1, 1, 15, 0, 0, 0, time.UTC) }
	_, _ = p.Range(testAsset, Timeframe1d, from, to, 0)
	if calls != 2 {
		t.Fatalf("expected a retry after the gate, calls=%d", calls)
	}
}

func TestDBProviderResumesMidWindow(t *testing.T) {
	// Prefix extension dies mid-window: the disconnected span was committed
	// page-by-page, so the resume must continue from progress_bar — not
	// restart at the window's beginning and re-burn provider calls.
	store := newMemStore()
	floor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	// Existing coverage near the present.
	store.cov[key("xauusd", Timeframe1d)] = Coverage{
		Provider: "tiingo", Count: 5,
		First: time.Date(2021, 6, 1, 0, 0, 0, 0, time.UTC),
		Last:  time.Date(2021, 6, 10, 0, 0, 0, 0, time.UTC),
	}
	from := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

	var calls1 []time.Time
	p := NewDBProvider(store, "tiingo", floor, dayPages(30, errors.New("boom"), &calls1))
	p.backoff = []time.Duration{0}
	to := time.Date(2021, 6, 10, 0, 0, 0, 0, time.UTC)
	if _, err := p.Range(testAsset, Timeframe1d, from, to, 0); err != nil {
		t.Fatal(err)
	}
	if len(calls1) != 30 {
		t.Fatalf("calls=%d", len(calls1))
	}
	cov, _ := store.Coverage(context.Background(), testAsset.ID, Timeframe1d)
	if cov.ProgressBar.IsZero() {
		t.Fatal("no resume checkpoint persisted")
	}
	checkpoint := cov.ProgressBar

	var calls2 []time.Time
	p2 := NewDBProvider(store, "tiingo", floor, dayPages(0, nil, &calls2))
	p2.backoff = []time.Duration{0}
	if _, err := p2.Range(testAsset, Timeframe1d, from, to, 0); err != nil {
		t.Fatal(err)
	}
	if !calls2[0].Equal(checkpoint) {
		t.Fatalf("resume refetched from %v — expected continuation at checkpoint %v", calls2[0], checkpoint)
	}
	// 29 pages committed before the failure; resume covers Jan-30 → Jun-1
	// (existing coverage edge) — 122 calls, no committed page refetched.
	if len(calls2) != 122 {
		t.Fatalf("resume made %d calls — expected 122 (Jan-30→Jun-1)", len(calls2))
	}
}

func TestDBProviderResumesFromCheckpoint(t *testing.T) {
	store := newMemStore()
	floor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	from := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2021, 1, 10, 0, 0, 0, 0, time.UTC)

	// First attempt dies on page 4 — committed pages must persist.
	var calls1 []time.Time
	p := NewDBProvider(store, "tiingo", floor, dayPages(4, errors.New("connection reset"), &calls1))
	p.backoff = []time.Duration{0} // retry immediately for the test
	if _, err := p.Range(testAsset, Timeframe1d, from, to, 0); err != nil {
		t.Fatal(err)
	}
	cov, _ := store.Coverage(context.Background(), testAsset.ID, Timeframe1d)
	if cov.State != SyncFailed {
		t.Fatalf("state = %q", cov.State)
	}
	if !cov.Last.Equal(from.AddDate(0, 0, 3)) { // 3 pages committed before page 4 died
		t.Fatalf("coverage.Last = %v — checkpoint not persisted per page", cov.Last)
	}
	if len(store.candles[key("xauusd", Timeframe1d)]) != 3 {
		t.Fatalf("stored %d bars — page commits lost", len(store.candles[key("xauusd", Timeframe1d)]))
	}

	// "Restart": a NEW DBProvider over the same store resumes from coverage —
	// no full-history redownload.
	var calls2 []time.Time
	p2 := NewDBProvider(store, "tiingo", floor, dayPages(0, nil, &calls2))
	cs, err := p2.Range(testAsset, Timeframe1d, from, to, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 9 {
		t.Fatalf("Range = %d candles, want 9", len(cs))
	}
	if calls2[0].Before(cov.Last) {
		t.Fatalf("resume re-fetched committed history: first call %v < checkpoint %v", calls2[0], cov.Last)
	}
	cov2, _ := store.Coverage(context.Background(), testAsset.ID, Timeframe1d)
	if cov2.State != SyncComplete {
		t.Fatalf("final state = %q", cov2.State)
	}
}

func TestDBProviderSingleFlight(t *testing.T) {
	store := newMemStore()
	release := make(chan struct{})
	var calls int
	var mu sync.Mutex
	hist := func(context.Context, Timeframe, time.Time, time.Time, func([]Candle, time.Time, time.Time) error) error {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release // hold the sync open
		return nil
	}
	p := NewDBProvider(store, "tiingo", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), hist)
	p.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

	done := make(chan struct{})
	go func() {
		_, _ = p.Range(testAsset, Timeframe1d, time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2021, 2, 1, 0, 0, 0, 0, time.UTC), 0)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond) // let the first sync start
	// Concurrent requests for the same series must NOT queue another sync —
	// they serve stored data immediately.
	for i := 0; i < 10; i++ {
		if _, err := p.Range(testAsset, Timeframe1d, time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2021, 2, 1, 0, 0, 0, 0, time.UTC), 0); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	if calls != 1 {
		t.Fatalf("concurrent reads spawned %d syncs", calls)
	}
	mu.Unlock()
	close(release)
	<-done
}

func TestDBProviderFingerprintDeterministic(t *testing.T) {
	store := newMemStore()
	p := NewDBProvider(store, "tiingo", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), dayPages(0, nil, nil))
	from, to := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2021, 1, 10, 0, 0, 0, 0, time.UTC)
	if _, err := p.Range(testAsset, Timeframe1d, from, to, 0); err != nil {
		t.Fatal(err)
	}
	fp1, err := p.Fingerprint(testAsset, Timeframe1d)
	if err != nil {
		t.Fatal(err)
	}
	fp2, _ := p.Fingerprint(testAsset, Timeframe1d)
	if fp1 != fp2 || fp1 == "" {
		t.Fatalf("fingerprint unstable: %q vs %q", fp1, fp2)
	}
	// Provider prefix distinguishes DB-synced datasets from legacy CSV hashes.
	if got := fp1[:7]; got != "tiingo:" {
		t.Fatalf("fingerprint lacks provider prefix: %q", fp1)
	}
}

func TestDBProviderRangeWarmupBoundaryNoDup(t *testing.T) {
	// `from` lands exactly on a stored bar: TailCandles is inclusive, the
	// window load is >= from — the boundary bar must appear exactly once.
	store := newMemStore()
	floor := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var all []Candle
	for i := 0; i < 20; i++ {
		all = append(all, Candle{Time: floor.Add(time.Duration(i) * 24 * time.Hour),
			Open: 1, High: 1, Low: 1, Close: 1})
	}
	if err := store.UpsertCandles(context.Background(), testAsset.ID, Timeframe1d, "tiingo", all); err != nil {
		t.Fatal(err)
	}
	store.cov[key("xauusd", Timeframe1d)] = Coverage{
		Provider: "tiingo", State: SyncComplete, Count: 20,
		First: all[0].Time, Last: all[len(all)-1].Time.Add(24 * time.Hour),
	}
	p := NewDBProvider(store, "tiingo", floor, nil)
	from := all[7].Time // exactly on a stored bar
	out, err := p.Range(testAsset, Timeframe1d, from, all[19].Time.Add(24*time.Hour), 5)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[time.Time]int{}
	for _, c := range out {
		seen[c.Time]++
		if seen[c.Time] > 1 {
			t.Fatalf("duplicate bar %v in Range output", c.Time)
		}
	}
	// 5 warmup bars strictly before `from` + window [from, to).
	if len(out) != 5+13 {
		t.Fatalf("got %d candles — expected 5 warmup + 13 window", len(out))
	}
}
