package service

import (
	"context"
	"time"

	"github.com/gold-mine-lab/backend/internal/api"
	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
)

// --- trades -------------------------------------------------------------------

func (s *Service) ListTrades(ctx context.Context, f api.TradeFilters) (api.PagedTrades, error) {
	q := repository.TradeQuery{
		StrategyID: f.StrategyID, Side: f.Side, Result: f.Result,
		Page: f.Page, PageSize: f.PageSize,
	}
	if f.From != "" {
		if t, err := time.Parse("2006-01-02", f.From); err == nil {
			q.From = t
		}
	}
	if f.To != "" {
		if t, err := time.Parse("2006-01-02", f.To); err == nil {
			q.To = t.AddDate(0, 0, 1) // inclusive end date
		}
	}
	rows, total, err := s.repos.Trades.ListTradesFiltered(ctx, q)
	if err != nil {
		return api.PagedTrades{}, err
	}
	items := make([]api.BacktestTrade, 0, len(rows))
	for _, t := range rows {
		items = append(items, tradeDTO(t))
	}
	return api.PagedTrades{Items: items, Total: total, Page: f.Page, PageSize: f.PageSize}, nil
}

// tfOf converts a stored timeframe string back to the domain type.
func tfOf(s string) market.Timeframe { return market.Timeframe(s) }
