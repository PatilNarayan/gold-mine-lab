package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/strategy/indicators"
)

// LiveService adapts the managed live provider to the API contract. Live data
// is strictly observational: it never enters the CSV provider, the backtest
// engine, or any persisted research record.
type LiveService struct {
	provider   live.Provider // nil when live data is disabled
	staleAfter time.Duration
	hub        *liveHub
}

// NewLiveService wires the live market service. When enabled is false the
// service answers status honestly and refuses quote/candle requests.
func NewLiveService(provider live.Provider) *LiveService {
	s := &LiveService{provider: provider, staleAfter: 30 * time.Second, hub: &liveHub{subs: map[chan api.LiveStreamEvent]struct{}{}}}
	if provider != nil {
		s.staleAfter = provider.Status().StaleAfter
	}
	return s
}

// liveTimeframes are the intervals the provider supports for XAU/USD.
var liveTimeframes = map[market.Timeframe]bool{
	market.Timeframe1m: true, market.Timeframe5m: true, market.Timeframe15m: true,
	market.Timeframe1h: true, market.Timeframe4h: true, market.Timeframe1d: true,
}

// LiveStatus reports provider truth — disabled, config errors, connection
// state, last quote time, last error. Always succeeds.
func (s *LiveService) LiveStatus(_ context.Context) api.LiveStatusResponse {
	if s.provider == nil {
		return api.LiveStatusResponse{
			Enabled: false, State: string(live.StateDisabled), Provider: "twelvedata",
		}
	}
	st := s.provider.Status()
	fmtTime := func(t *time.Time) *string {
		if t == nil {
			return nil
		}
		v := t.UTC().Format(time.RFC3339)
		return &v
	}
	return api.LiveStatusResponse{
		Enabled:               st.Enabled,
		State:                 string(st.State),
		Provider:              st.Provider,
		Transport:             st.Transport,
		Symbol:                st.Symbol,
		LastQuoteAt:           fmtTime(st.LastQuoteAt),
		LastFetchAt:           fmtTime(st.LastFetchAt),
		LastError:             st.LastError,
		StaleAfterSeconds:     int(st.StaleAfter.Seconds()),
		PollIntervalSeconds:   int(st.PollInterval.Seconds()),
		EstimatedDailyCredits: st.EstimatedDailyCredits,
	}
}

// LiveQuote returns the newest provider quote for xauusd, flagged stale when
// older than the configured staleness window. Never fabricates a price.
func (s *LiveService) LiveQuote(ctx context.Context, assetID string) (api.LiveQuoteResponse, error) {
	if err := s.requireLive(assetID); err != nil {
		return api.LiveQuoteResponse{}, err
	}
	st := s.provider.Status()
	q, ok := s.provider.LatestQuote()
	if !ok {
		return api.LiveQuoteResponse{}, fmt.Errorf("%w: no live quote received yet (state=%s)", api.ErrUnavailable, st.State)
	}
	status := "fresh"
	if time.Since(q.ReceivedAt) > s.staleAfter {
		status = "stale"
	}
	var provTS *string
	if q.ProviderTime != nil {
		v := q.ProviderTime.UTC().Format(time.RFC3339)
		provTS = &v
	}
	return api.LiveQuoteResponse{
		AssetID:           q.AssetID,
		Provider:          st.Provider,
		Symbol:            q.Symbol,
		Price:             q.Price,
		PriceKind:         q.PriceKind,
		Bid:               q.Bid,
		Ask:               q.Ask,
		DayVolume:         q.DayVolume,
		ProviderTimestamp: provTS,
		ReceivedAt:        q.ReceivedAt.UTC().Format(time.RFC3339),
		Status:            status,
	}, nil
}

// LiveCandles returns a coherent recent window from the provider — history
// seeded by its time-series endpoint plus the forming bar updated by the
// stream. It is never stitched onto the historical CSV series.
func (s *LiveService) LiveCandles(ctx context.Context, assetID string, tf market.Timeframe, limit int) (api.LiveCandlesResponse, error) {
	if err := s.requireLive(assetID); err != nil {
		return api.LiveCandlesResponse{}, err
	}
	if !liveTimeframes[tf] {
		return api.LiveCandlesResponse{}, &api.ValidationError{Msg: fmt.Sprintf("unsupported live timeframe %q", tf)}
	}
	// Fetch the whole book for indicator warmup — an EMA seeded at the first
	// VISIBLE candle would diverge from the same-timestamp EMA the historical
	// endpoints produce. Only the last `limit` bars are returned to the client.
	cs, err := s.provider.Candles(ctx, string(tf), 0) // 0 → provider's full book
	if err != nil {
		return api.LiveCandlesResponse{}, fmt.Errorf("%w: %v", api.ErrUnavailable, err)
	}
	full := cs
	if limit > 0 && len(cs) > limit {
		cs = cs[len(cs)-limit:]
	}
	st := s.provider.Status()
	out := api.LiveCandlesResponse{
		AssetID:   assetID,
		Provider:  st.Provider,
		Symbol:    st.Symbol,
		Timeframe: tf,
		Status:    string(st.State),
		Candles:   make([]api.LiveCandle, 0, len(cs)),
		Overlays:  liveOverlays(full, len(cs)),
	}
	for _, c := range cs {
		out.Candles = append(out.Candles, api.LiveCandle{
			Time: c.Time.UTC().Format(time.RFC3339),
			Open: c.Open, High: c.High, Low: c.Low, Close: c.Close,
			Volume: c.Volume,
		})
	}
	return out, nil
}

// --- live streaming --------------------------------------------------------

// liveTFOrder is the deterministic timeframe order used in stream frames.
var liveTFOrder = []market.Timeframe{
	market.Timeframe1m, market.Timeframe5m, market.Timeframe15m,
	market.Timeframe1h, market.Timeframe4h, market.Timeframe1d,
}

// liveHub fans provider quote events out to every connected stream client.
// The provider subscription is created once — browser clients never multiply
// upstream work. Frames are dropped per slow client rather than buffered
// without bound: every bars frame carries complete snapshots, so the next
// tick heals any drop.
type liveHub struct {
	mu      sync.Mutex
	subs    map[chan api.LiveStreamEvent]struct{}
	started bool
	lastKey string // last broadcast status signature
}

// LiveStream registers a subscriber and returns its channel plus an
// unsubscribe func. The initial snapshot (status, latest quote, newest bar
// per timeframe) is pushed under the hub lock so a broadcast can never
// interleave a newer frame ahead of the snapshot.
func (s *LiveService) LiveStream(_ context.Context) (<-chan api.LiveStreamEvent, func(), error) {
	ch := make(chan api.LiveStreamEvent, 64)
	s.hub.mu.Lock()
	s.hub.subs[ch] = struct{}{}
	if s.provider != nil && !s.hub.started {
		s.hub.started = true
		s.provider.SubscribeQuotes(s.onProviderQuote)
		go s.statusTicker()
	}
	s.hub.push(ch, api.LiveStreamEvent{Type: "status", Status: ptr(s.LiveStatus(context.Background()))})
	if q, ok := s.providerSnapshot(); ok {
		s.hub.push(ch, api.LiveStreamEvent{Type: "quote", Quote: &q})
	}
	if upd := s.liveBars(); len(upd) > 0 {
		s.hub.push(ch, api.LiveStreamEvent{Type: "bars", Updates: upd})
	}
	s.hub.mu.Unlock()
	return ch, func() {
		s.hub.mu.Lock()
		delete(s.hub.subs, ch)
		s.hub.mu.Unlock()
	}, nil
}

// providerSnapshot maps the cached provider quote onto the API shape without
// the asset check — the provider only ever serves xauusd.
func (s *LiveService) providerSnapshot() (api.LiveQuoteResponse, bool) {
	if s.provider == nil {
		return api.LiveQuoteResponse{}, false
	}
	q, ok := s.provider.LatestQuote()
	if !ok {
		return api.LiveQuoteResponse{}, false
	}
	return s.mapQuote(q), true
}

func (s *LiveService) mapQuote(q live.Quote) api.LiveQuoteResponse {
	st := s.provider.Status()
	status := "fresh"
	if time.Since(q.ReceivedAt) > s.staleAfter {
		status = "stale"
	}
	var provTS *string
	if q.ProviderTime != nil {
		v := q.ProviderTime.UTC().Format(time.RFC3339)
		provTS = &v
	}
	return api.LiveQuoteResponse{
		AssetID: q.AssetID, Provider: st.Provider, Symbol: q.Symbol,
		Price: q.Price, PriceKind: q.PriceKind, Bid: q.Bid, Ask: q.Ask,
		DayVolume: q.DayVolume, ProviderTimestamp: provTS,
		ReceivedAt: q.ReceivedAt.UTC().Format(time.RFC3339), Status: status,
	}
}

// liveBars builds one LiveBarUpdate per timeframe: the newest bar (the
// forming candle) plus its EMA20/EMA50 overlay points computed over the same
// closed+forming window the REST endpoint returns.
func (s *LiveService) liveBars() []api.LiveBarUpdate {
	if s.provider == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out := make([]api.LiveBarUpdate, 0, len(liveTFOrder))
	for _, tf := range liveTFOrder {
		cs, err := s.provider.Candles(ctx, string(tf), 0) // full book → warmed EMA
		if err != nil || len(cs) == 0 {
			continue
		}
		last := cs[len(cs)-1]
		upd := api.LiveBarUpdate{
			Timeframe: string(tf),
			Candle: &api.LiveCandle{
				Time: last.Time.UTC().Format(time.RFC3339),
				Open: last.Open, High: last.High, Low: last.Low, Close: last.Close,
				Volume: last.Volume,
			},
		}
		ov := liveOverlays(cs, 0)
		if n := len(ov.EMA20); n > 0 {
			pt := ov.EMA20[n-1]
			upd.EMA20 = &pt
		}
		if n := len(ov.EMA50); n > 0 {
			pt := ov.EMA50[n-1]
			upd.EMA50 = &pt
		}
		out = append(out, upd)
	}
	return out
}

// onProviderQuote is invoked by the provider per accepted tick: quote frame +
// per-timeframe bar frames + a status frame when the visible state changed.
func (s *LiveService) onProviderQuote(q live.Quote) {
	qr := s.mapQuote(q)
	s.hub.broadcast(api.LiveStreamEvent{Type: "quote", Quote: &qr})
	if upd := s.liveBars(); len(upd) > 0 {
		s.hub.broadcast(api.LiveStreamEvent{Type: "bars", Updates: upd})
	}
	st := s.LiveStatus(context.Background())
	s.hub.maybeStatus(st)
}

// statusTicker pushes status frames on visible transitions that arrive with
// no quote event — e.g. connected→stale when the feed goes quiet.
func (s *LiveService) statusTicker() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for range t.C {
		s.hub.maybeStatus(s.LiveStatus(context.Background()))
	}
}

// broadcast sends ev to every subscriber; a full client buffer drops the
// frame (snapshots are self-healing — the next tick resends current state).
func (h *liveHub) broadcast(ev api.LiveStreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		h.push(ch, ev)
	}
}

// maybeStatus broadcasts only on visible transitions (state or error) —
// per-tick timestamps already ride the quote frames.
func (h *liveHub) maybeStatus(st api.LiveStatusResponse) {
	key := st.State + "|" + st.LastError
	h.mu.Lock()
	if key == h.lastKey {
		h.mu.Unlock()
		return
	}
	h.lastKey = key
	h.mu.Unlock()
	h.broadcast(api.LiveStreamEvent{Type: "status", Status: &st})
}

func (h *liveHub) push(ch chan api.LiveStreamEvent, ev api.LiveStreamEvent) {
	select {
	case ch <- ev:
	default:
	}
}

// liveOverlays computes EMA20/EMA50 over the FULL candle window cs (closed +
// forming), using the same indicators.EMA implementation the historical
// endpoints use, then emits only the last `tail` points so the overlay aligns
// with the candles actually returned. The EMA still sees the entire warmup —
// it is never reseeded at an arbitrary visible boundary.
func liveOverlays(cs []live.Candle, tail int) api.Overlays {
	closes := make([]float64, len(cs))
	for i, c := range cs {
		closes[i] = c.Close
	}
	toPts := func(vals []float64, warmup int) []api.SeriesPoint {
		pts := make([]api.SeriesPoint, 0, max(0, len(vals)-warmup+1))
		for i := warmup - 1; i < len(vals); i++ {
			pts = append(pts, api.SeriesPoint{
				Time:  cs[i].Time.UTC().Format(time.RFC3339),
				Value: round2(vals[i]),
			})
		}
		if tail > 0 && len(pts) > tail {
			pts = pts[len(pts)-tail:]
		}
		return pts
	}
	return api.Overlays{
		EMA20: toPts(indicators.EMA(closes, 20), 20),
		EMA50: toPts(indicators.EMA(closes, 50), 50),
	}
}

// requireLive enforces the single-asset policy and the feature flag.
// Unsupported assets get an explicit 400 — never a silent substitute.
func (s *LiveService) requireLive(assetID string) error {
	if assetID != market.CanonicalAssetID {
		return &api.ValidationError{Msg: fmt.Sprintf("unsupported asset %q — only %s (XAUUSD) is supported", assetID, market.CanonicalAssetID)}
	}
	if s.provider == nil {
		return fmt.Errorf("%w: live market data is disabled (set LIVE_MARKET_ENABLED=true and a provider credential)", api.ErrUnavailable)
	}
	return nil
}
