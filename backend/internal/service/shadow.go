package service

import (
	"context"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/shadow"
)

// ShadowService exposes the shadow engine's state via the API — all
// SIMULATED, clearly labeled, never a broker position.
type ShadowService struct {
	eng      *shadow.Engine
	store    shadow.Store
	sessions SessionReader
	coverage CoverageReader
	cfg      shadow.Config
}

// CoverageReader supplies recorder-health intervals for the status endpoint.
type CoverageReader interface {
	OpenCoverage(ctx context.Context, provider, symbol string) (*repository.QuoteCoverageRow, error)
}

// SessionReader supplies session identity for the status endpoint.
type SessionReader interface {
	GetOpenSession(ctx context.Context, strategyID, version, symbol, tf, provider string) (*repository.ShadowSessionRow, error)
}

func NewShadowService(eng *shadow.Engine, store shadow.Store, sessions SessionReader, coverage CoverageReader, cfg shadow.Config) *ShadowService {
	return &ShadowService{eng: eng, store: store, sessions: sessions, coverage: coverage, cfg: cfg}
}

func orderDTO(o repository.ShadowOrderRow) api.ShadowOrder {
	return api.ShadowOrder{
		ID: o.ID, Side: o.Side, Status: o.Status, SignalBarTime: o.SignalBarTime,
		DecisionAt: o.DecisionAt, RejectReason: o.RejectReason,
		QuoteBid: o.QuoteBid, QuoteAsk: o.QuoteAsk, QuoteMid: o.QuoteMid,
		FillPrice: o.FillPrice, FilledAt: o.FilledAt,
		Quantity: o.Quantity, Commission: o.Commission,
		QuoteProvider: o.QuoteProviderTime, QuoteReceipt: o.QuoteReceivedAt,
	}
}

func (s *ShadowService) ShadowStatus(ctx context.Context) (api.ShadowStatusResponse, error) {
	snap := s.eng.Snapshot()
	out := api.ShadowStatusResponse{
		Mode: snap.Mode, StrategyID: snap.StrategyID,
		StrategyVersion: snap.StrategyVersion, Symbol: snap.Symbol,
		Timeframe: snap.Timeframe, RealizedPnl: snap.RealizedPnl,
		RealizedComm: snap.RealizedComm, RealizedSpread: snap.RealizedSpread,
		Equity: snap.Equity, LastBarTime: snap.LastBarTime,
	}
	if snap.Position != nil && snap.Position.Side != nil {
		out.Position = &api.ShadowPosition{
			Side: *snap.Position.Side, Quantity: snap.Position.Quantity,
			EntryAt: snap.Position.EntryAt,
		}
		if snap.Position.EntryPrice != nil {
			out.Position.EntryPrice = *snap.Position.EntryPrice
		}
	}
	if snap.Pending != nil {
		o := orderDTO(*snap.Pending)
		out.PendingOrder = &o
	}
	// Session identity + honest engine counters.
	if sess, err := s.sessions.GetOpenSession(ctx, s.cfg.StrategyID, s.cfg.StrategyVersion,
		s.cfg.Symbol, string(s.cfg.Timeframe), s.cfg.Provider); err == nil && sess != nil {
		out.Session = &api.ShadowSession{
			ID: sess.ID, Provider: sess.Provider,
			StartedAt: &sess.StartedAt, RestartCount: sess.RestartCount,
		}
	}
	m := s.eng.MetricsSnapshot()
	out.Metrics = api.ShadowMetrics{
		BarsProcessed: m.BarsProcessed, SignalsSeen: m.SignalsSeen,
		QuotesSeen: m.QuotesSeen, QuotesRejected: m.QuotesRejected,
		Fills: m.Fills, Expired: m.Expired, Missed: m.Missed,
		Unobservable: m.Unobservable,
		Recoveries:   m.Recoveries, DupSkips: m.DupSkips,
		LastQuoteAt: m.LastQuoteAt, LastFillAt: m.LastFillAt,
	}
	// Order stats from the persisted rows — counts reflect the database,
	// not just in-memory state (restart-honest).
	orders, err := s.store.ListOrders(ctx, s.cfg.StrategyID, s.cfg.Symbol,
		market.Timeframe(s.cfg.Timeframe), 500)
	if err == nil {
		for _, o := range orders {
			switch o.Status {
			case "filled":
				out.OrderStats.Filled++
			case "expired":
				out.OrderStats.Expired++
			case "rejected":
				out.OrderStats.Rejected++
			case "missed":
				out.OrderStats.Missed++
			case "unobservable":
				out.OrderStats.Unobservable++
			case "pending":
				out.OrderStats.Pending++
			}
		}
	}
	// Recorder health — distinct from provider connectivity.
	if cov, err := s.coverage.OpenCoverage(ctx, s.cfg.Provider, s.cfg.Symbol); err == nil && cov != nil {
		out.Recorder = &api.ShadowRecorder{
			CoverageID: cov.ID, StartedAt: &cov.StartedAt,
			LastHeartbeatAt: &cov.LastHeartbeatAt, LastObsAt: cov.LastObsAt,
			ObsCount: cov.ObsCount, DroppedBatches: cov.DroppedBatches,
			FeedState: cov.FeedState, LastFeedDegradedAt: cov.LastFeedDegradedAt,
			LastHeartbeatGapAt: cov.LastHeartbeatGapAt, LastDropAt: cov.LastDropAt,
		}
	}
	return out, nil
}

func (s *ShadowService) ShadowOrders(ctx context.Context, limit int) (api.ShadowOrdersResponse, error) {
	rows, err := s.store.ListOrders(ctx, s.cfg.StrategyID, s.cfg.Symbol,
		market.Timeframe(s.cfg.Timeframe), limit)
	if err != nil {
		return api.ShadowOrdersResponse{Mode: "simulated"}, err
	}
	out := api.ShadowOrdersResponse{Mode: "simulated", Orders: make([]api.ShadowOrder, 0, len(rows))}
	for _, r := range rows {
		out.Orders = append(out.Orders, orderDTO(r))
	}
	return out, nil
}

func (s *ShadowService) ShadowDecisions(ctx context.Context, limit int) (api.ShadowDecisionsResponse, error) {
	rows, err := s.store.ListDecisions(ctx, s.cfg.StrategyID, s.cfg.StrategyVersion,
		s.cfg.Symbol, s.cfg.Timeframe, limit)
	if err != nil {
		return api.ShadowDecisionsResponse{}, err
	}
	out := api.ShadowDecisionsResponse{Decisions: make([]api.ShadowDecision, 0, len(rows))}
	for _, d := range rows {
		out.Decisions = append(out.Decisions, api.ShadowDecision{
			ID: d.ID, SessionID: d.SessionID, OrderID: d.OrderID,
			BarTime: d.BarTime, DecidedAt: d.DecidedAt, Source: d.Source,
			Action: d.Action, Reason: d.Reason, FastEMA: d.FastEMA, SlowEMA: d.SlowEMA,
			Actionable: d.Actionable, BlockedReason: d.BlockedReason,
		})
	}
	return out, nil
}
