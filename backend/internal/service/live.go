package service

import (
	"context"
	"fmt"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/live"
	"github.com/gold-mine-lab/backend/internal/market"
)

// LiveService adapts the managed live provider to the API contract. Live data
// is strictly observational: it never enters the CSV provider, the backtest
// engine, or any persisted research record.
type LiveService struct {
	provider   live.Provider // nil when live data is disabled
	staleAfter time.Duration
}

// NewLiveService wires the live market service. When enabled is false the
// service answers status honestly and refuses quote/candle requests.
func NewLiveService(provider live.Provider) *LiveService {
	s := &LiveService{provider: provider, staleAfter: 30 * time.Second}
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
	cs, err := s.provider.Candles(ctx, string(tf), limit)
	if err != nil {
		return api.LiveCandlesResponse{}, fmt.Errorf("%w: %v", api.ErrUnavailable, err)
	}
	st := s.provider.Status()
	out := api.LiveCandlesResponse{
		AssetID:   assetID,
		Provider:  st.Provider,
		Symbol:    st.Symbol,
		Timeframe: tf,
		Status:    string(st.State),
		Candles:   make([]api.LiveCandle, 0, len(cs)),
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
