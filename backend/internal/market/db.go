package market

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// HistoricalSource is the candle surface the application service consumes —
// satisfied by DBProvider (provider-synced store) and CSVProvider (tests and
// the legacy demo fixture). All implementations return ascending candles in
// the canonical Kyiv civil-time label convention.
type HistoricalSource interface {
	Fingerprint(asset Asset, tf Timeframe) (string, error)
	GetCandles(ctx context.Context, asset Asset, tf Timeframe, from, to time.Time) ([]Candle, error)
	Range(asset Asset, tf Timeframe, from, to time.Time, warmup int) ([]Candle, error)
	Tail(asset Asset, tf Timeframe, n int, asOf time.Time) ([]Candle, error)
}

// Sync states recorded in candle_sync.state — honest about what is and
// isn't known. A successful request is never confused with complete data.
const (
	SyncComplete    = "complete"     // the requested span was fully fetched
	SyncPartial     = "partial"      // an attempt covered only part of the span
	SyncRateLimited = "rate_limited" // provider refused with 429; retry_at gates the next attempt
	SyncFailed      = "failed"       // non-quota error; bounded backoff applies
	SyncUnavailable = "unavailable"  // no history source configured
)

// Coverage describes synced data for one asset×timeframe: every bar the
// provider reported inside [First, Last] is stored (sparse — closed-market
// intervals legitimately have none). Bars outside the span may exist
// (partial writes) but are not claimed as coverage.
type Coverage struct {
	Provider string
	First    time.Time
	Last     time.Time
	Count    int64
	// Sync bookkeeping — persisted so a restart resumes politely.
	State       string
	LastError   string
	RetryAt     time.Time // next eligible sync attempt; zero = now
	ProgressBar time.Time // farthest contiguous point synced in the in-flight window
	ProgressDir string    // "extend_first" | "extend_last"
}

// CandleStore is the persistent half of DBProvider. The Postgres
// implementation lives in internal/repository/postgres; tests substitute an
// in-memory store.
type CandleStore interface {
	UpsertCandles(ctx context.Context, assetID string, tf Timeframe, provider string, cs []Candle) error
	LoadCandles(ctx context.Context, assetID string, tf Timeframe, from, to time.Time) ([]Candle, error)
	TailCandles(ctx context.Context, assetID string, tf Timeframe, n int, before time.Time) ([]Candle, error)
	Coverage(ctx context.Context, assetID string, tf Timeframe) (Coverage, error)
	SetCoverage(ctx context.Context, assetID string, tf Timeframe, cov Coverage) error
}

// HistoryFunc pages provider candles over [from, to). onPage is invoked per
// fetched page with session-labeled candles and the raw span actually
// covered — pages may legitimately carry zero bars (closed market). A nil
// HistoryFunc means the store is read-only.
type HistoryFunc func(ctx context.Context, tf Timeframe, from, to time.Time,
	onPage func(page []Candle, coveredFrom, coveredTo time.Time) error) error

// RetryableRateLimit is satisfied by provider errors carrying guidance on
// when the next request is eligible (e.g. live.RateLimitError).
type RetryableRateLimit interface {
	error
	RateLimitRetryAfter() time.Duration
}

// DBProvider serves historical candles from Postgres, filling coverage gaps
// from the configured market provider on read. Syncs are single-flight per
// asset×timeframe, commit coverage per page, persist retry gates so a hot
// read path can never hammer the provider, and never fabricate bars.
type DBProvider struct {
	store    CandleStore
	hist     HistoryFunc
	provider string
	floor    time.Time

	mu       sync.Mutex // guards inflight and failures
	inflight map[string]bool
	failures map[string]int // consecutive sync failures → backoff step

	backoff []time.Duration  // escalating retry schedule; tests may shrink
	now     func() time.Time // test hook
}

// NewDBProvider wires a candle store with an optional history source.
// provider is provenance metadata recorded on stored rows.
func NewDBProvider(store CandleStore, provider string, floor time.Time, hist HistoryFunc) *DBProvider {
	return &DBProvider{
		store:    store,
		provider: provider,
		floor:    floor,
		hist:     hist,
		inflight: map[string]bool{},
		failures: map[string]int{},
		backoff:  []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour, 6 * time.Hour},
		now:      time.Now,
	}
}

// Ensure forces a sync of [from, to) — bootstrap pre-warming uses it.
// Unlike the read paths (which serve stored data over a failed sync),
// callers get the outcome: the recorded sync error when it didn't complete.
func (p *DBProvider) Ensure(ctx context.Context, asset Asset, tf Timeframe, from, to time.Time) error {
	if err := p.ensure(ctx, asset, tf, from, to); err != nil {
		return err
	}
	cov, err := p.store.Coverage(ctx, asset.ID, tf)
	if err != nil {
		return err
	}
	if cov.State == SyncRateLimited || cov.State == SyncFailed {
		return errors.New(cov.LastError)
	}
	return nil
}

// CoverageInfo reports the recorded coverage + sync state for one series.
func (p *DBProvider) CoverageInfo(ctx context.Context, asset Asset, tf Timeframe) (Coverage, error) {
	return p.store.Coverage(ctx, asset.ID, tf)
}

// InFlight reports whether a sync is actively running for this series —
// distinct from a persisted 'partial' state, which means the last attempt
// stopped mid-window and will resume on the next eligible request.
func (p *DBProvider) InFlight(asset Asset, tf Timeframe) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inflight[asset.ID+"|"+string(tf)]
}

const syncTimeout = 10 * time.Minute

// tryBegin claims the single-flight slot; false = a sync is already running
// for this series — callers serve stored data instead of queueing.
func (p *DBProvider) tryBegin(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inflight[key] {
		return false
	}
	p.inflight[key] = true
	return true
}

func (p *DBProvider) end(key string) {
	p.mu.Lock()
	delete(p.inflight, key)
	p.mu.Unlock()
}

// ensure fetches any missing edges of the requested span, committing
// coverage per page. Failure leaves previously committed progress intact —
// resumption continues from the persisted span.
func (p *DBProvider) ensure(ctx context.Context, asset Asset, tf Timeframe, from, to time.Time) error {
	if p.hist == nil || !to.After(from) {
		return nil
	}
	if !p.floor.IsZero() && from.Before(p.floor) {
		from = p.floor // never claim coverage the provider cannot supply
	}
	if !from.Before(to) {
		return nil
	}
	key := asset.ID + "|" + string(tf)
	if !p.tryBegin(key) {
		return nil
	}
	defer p.end(key)

	ctx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()

	cov, err := p.store.Coverage(ctx, asset.ID, tf)
	if err != nil {
		return err
	}
	if cov.Provider != "" && cov.Provider != p.provider {
		return nil // never extend foreign-provider coverage — no silent mixing
	}
	if !cov.RetryAt.IsZero() && p.now().Before(cov.RetryAt) {
		return nil // inside the persisted backoff window — serve stored
	}

	prefix := !cov.First.IsZero() && from.Before(cov.First)
	suffix := cov.First.IsZero() || to.After(cov.Last)
	if !prefix && !suffix {
		return nil
	}

	save := func() {
		// Persist on a fresh context: a dead request context must not lose
		// committed progress or the retry gate.
		_ = p.store.SetCoverage(context.Background(), asset.ID, tf, cov)
	}

	syncSpan := func(f, t time.Time, dir string) error {
		// Resume mid-window: when an earlier attempt died after committing
		// pages (ProgressBar = farthest committed edge in this direction),
		// continue from it — the bars are already stored, refetching them
		// would burn provider budget for nothing.
		if cov.ProgressDir == dir && !cov.ProgressBar.IsZero() &&
			cov.ProgressBar.After(f) && cov.ProgressBar.Before(t) {
			f = cov.ProgressBar
		}
		cov.ProgressDir = dir
		return p.hist(ctx, tf, f, t, func(page []Candle, covFrom, covTo time.Time) error {
			if len(page) > 0 {
				if err := p.store.UpsertCandles(ctx, asset.ID, tf, p.provider, page); err != nil {
					return err
				}
			}
			// Coverage tracks fetched SPAN, not row count — an empty page is
			// a synced market-closed interval, not a hole to retry.
			if cov.Provider == "" {
				cov.Provider = p.provider
				cov.First, cov.Last = covFrom, covTo
			} else {
				if covTo.After(cov.Last) && !covFrom.After(cov.Last) {
					cov.Last = covTo
				}
				if covFrom.Before(cov.First) && !covTo.Before(cov.First) {
					cov.First = covFrom
				}
			}
			cov.ProgressBar = covTo // resume checkpoint for this window
			cov.State = SyncPartial
			save()
			return nil
		})
	}

	fail := func(err error) error {
		cov.LastError = err.Error()
		// ProgressDir/ProgressBar persist — the next attempt resumes the
		// same window from the committed edge instead of restarting it.
		p.mu.Lock()
		step := p.failures[key]
		if step > len(p.backoff)-1 {
			step = len(p.backoff) - 1
		}
		p.failures[key] = step + 1
		p.mu.Unlock()
		base := p.backoff[step]
		wait := base + time.Duration(rand.Int63n(int64(base)/4+1)) // jitter
		cov.State = SyncFailed
		var rl RetryableRateLimit
		if errors.As(err, &rl) {
			cov.State = SyncRateLimited
			if w := rl.RateLimitRetryAfter(); w > wait {
				wait = w
			}
		}
		cov.RetryAt = p.now().Add(wait)
		save()
		return nil // recorded — callers serve stored data, never fabricate
	}
	succeed := func() error {
		// Exact stored count over the verified span — for provenance.
		if cs, err := p.store.LoadCandles(ctx, asset.ID, tf, cov.First, cov.Last.Add(time.Nanosecond)); err == nil {
			cov.Count = int64(len(cs))
		}
		cov.State = SyncComplete
		cov.LastError = ""
		cov.RetryAt = time.Time{}
		cov.ProgressBar = time.Time{}
		cov.ProgressDir = ""
		p.mu.Lock()
		delete(p.failures, key)
		p.mu.Unlock()
		save()
		return nil
	}

	if prefix {
		if err := syncSpan(from, cov.First, "extend_first"); err != nil {
			return fail(err)
		}
	}
	if suffix {
		f := cov.Last
		if f.IsZero() {
			f = from
		}
		if err := syncSpan(f, to, "extend_last"); err != nil {
			return fail(err)
		}
	}
	return succeed()
}

func (p *DBProvider) GetCandles(ctx context.Context, asset Asset, tf Timeframe, from, to time.Time) ([]Candle, error) {
	if err := p.ensure(ctx, asset, tf, from, to); err != nil {
		return nil, err
	}
	return p.store.LoadCandles(ctx, asset.ID, tf, from, to)
}

// Range returns [from, to) plus up to `warmup` candles immediately before
// from — same contract as the CSV provider. Uncovered history is not an
// error: whatever genuinely exists is returned, which may be empty.
func (p *DBProvider) Range(asset Asset, tf Timeframe, from, to time.Time, warmup int) ([]Candle, error) {
	ctx := context.Background()
	if err := p.ensure(ctx, asset, tf, from, to); err != nil {
		return nil, err
	}
	out, err := p.store.LoadCandles(ctx, asset.ID, tf, from, to)
	if err != nil {
		return nil, err
	}
	if warmup > 0 {
		// TailCandles is inclusive of `before`; the warmup slice must be
		// strictly before `from` — fetch one extra and drop the boundary bar
		// or the window's first bar would appear twice.
		pre, err := p.store.TailCandles(ctx, asset.ID, tf, warmup+1, from)
		if err != nil {
			return nil, err
		}
		if n := len(pre); n > 0 && !pre[n-1].Time.Before(from) {
			pre = pre[:n-1]
		}
		out = append(pre, out...)
	}
	return out, nil
}

// Tail returns the last n candles at or before asOf (zero = dataset latest).
// When a history source exists it also refreshes recent coverage when stale;
// a failed refresh still serves whatever is stored.
func (p *DBProvider) Tail(asset Asset, tf Timeframe, n int, asOf time.Time) ([]Candle, error) {
	ctx := context.Background()
	bound := asOf
	if bound.IsZero() {
		bound = p.now().UTC()
	}
	if p.hist != nil {
		cov, err := p.store.Coverage(ctx, asset.ID, tf)
		if err != nil {
			return nil, err
		}
		if cov.First.IsZero() || cov.Last.Before(bound.Add(-freshnessWindow(tf))) {
			from := cov.Last
			if cov.First.IsZero() {
				from = bound.Add(-defaultTailLookback(tf, n))
			}
			_ = p.ensure(ctx, asset, tf, from, bound) // best-effort; serve stored
		}
	}
	return p.store.TailCandles(ctx, asset.ID, tf, n, bound)
}

func freshnessWindow(tf Timeframe) time.Duration {
	switch tf {
	case Timeframe1d:
		return 48 * time.Hour
	case Timeframe4h:
		return 8 * time.Hour
	default:
		return 2 * time.Hour
	}
}

func defaultTailLookback(tf Timeframe, n int) time.Duration {
	bar := map[Timeframe]time.Duration{
		Timeframe1m: time.Minute, Timeframe5m: 5 * time.Minute,
		Timeframe15m: 15 * time.Minute, Timeframe1h: time.Hour,
		Timeframe4h: 4 * time.Hour, Timeframe1d: 24 * time.Hour,
	}[tf]
	if bar <= 0 {
		bar = time.Hour
	}
	return bar * time.Duration(n) * 2
}

// Fingerprint identifies the synced dataset deterministically: provider +
// coverage + count. The provider prefix distinguishes these from legacy CSV
// file fingerprints (which carry no provider component) — unambiguous
// provenance for backtest records.
func (p *DBProvider) Fingerprint(asset Asset, tf Timeframe) (string, error) {
	cov, err := p.store.Coverage(context.Background(), asset.ID, tf)
	if err != nil {
		return "", err
	}
	if cov.Count == 0 {
		return "", fmt.Errorf("no synced candles for %s %s", asset.ID, tf)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%s|%s|%d",
		cov.Provider, asset.ID, tf, cov.First.Format(time.RFC3339), cov.Last.Format(time.RFC3339), cov.Count)))
	return fmt.Sprintf("%s:%s:%s@sha256:%x", cov.Provider, asset.ID, tf, sum), nil
}

// Compile-time check: both production paths satisfy the service contract.
var (
	_ HistoricalSource = (*DBProvider)(nil)
	_ HistoricalSource = (*CSVProvider)(nil)
)
