package postgres

import (
	"context"
	"fmt"
)

func (s *Store) GetMetrics(ctx context.Context, runID string) (map[string]float64, error) {
	rows, err := s.db.Query(ctx,
		`SELECT metric_name, metric_value FROM backtest_metrics WHERE run_id = $1`, runID)
	if err != nil {
		return nil, fmt.Errorf("get metrics: %w", err)
	}
	defer rows.Close()

	out := map[string]float64{}
	for rows.Next() {
		var name string
		var v float64
		if err := rows.Scan(&name, &v); err != nil {
			return nil, fmt.Errorf("scan metric: %w", err)
		}
		out[name] = v
	}
	return out, rows.Err()
}

// AggregateMetrics summarizes completed runs for the dashboard. Joins each
// run's persisted metric rows rather than recomputing anything here.
func (s *Store) AggregateMetrics(ctx context.Context) (netPnl, winRateMean, maxDrawdown float64, totalRuns int, err error) {
	const q = `
		SELECT
		    coalesce(sum(case when m.metric_name = 'net_pnl' then m.metric_value end), 0),
		    coalesce(avg(case when m.metric_name = 'win_rate' then m.metric_value end), 0),
		    coalesce(max(case when m.metric_name = 'max_drawdown' then m.metric_value end), 0),
		    count(DISTINCT r.id)
		FROM backtest_runs r
		LEFT JOIN backtest_metrics m ON m.run_id = r.id
		WHERE r.status = 'completed'`
	err = s.db.QueryRow(ctx, q).Scan(&netPnl, &winRateMean, &maxDrawdown, &totalRuns)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("aggregate metrics: %w", err)
	}
	return netPnl, winRateMean, maxDrawdown, totalRuns, nil
}

// BestStrategyByNetPnl picks the strategy whose best completed run has the
// highest net P&L.
func (s *Store) BestStrategyByNetPnl(ctx context.Context) (string, string, bool, error) {
	const q = `
		SELECT r.strategy_id, st.name
		FROM backtest_runs r
		JOIN backtest_metrics m ON m.run_id = r.id AND m.metric_name = 'net_pnl'
		JOIN strategies st ON st.id = r.strategy_id
		WHERE r.status = 'completed'
		ORDER BY m.metric_value DESC
		LIMIT 1`
	var id, name string
	err := s.db.QueryRow(ctx, q).Scan(&id, &name)
	if err != nil {
		if err.Error() == "no rows in result set" {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("best strategy: %w", err)
	}
	return id, name, true, nil
}
