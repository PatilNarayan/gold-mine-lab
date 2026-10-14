package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/gold-mine-lab/backend/internal/repository"
)

const tradeCols = `id::text, run_id::text, strategy_id, asset_id, side,
	entry_time, entry_price, exit_time, exit_price, quantity, pnl, status, result`

func (s *Store) ListTradesByRun(ctx context.Context, runID string) ([]repository.TradeRow, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+tradeCols+` FROM backtest_trades WHERE run_id = $1 ORDER BY entry_time`, runID)
	if err != nil {
		return nil, fmt.Errorf("list trades by run: %w", err)
	}
	defer rows.Close()
	return scanTrades(rows)
}

func (s *Store) ListAllClosed(ctx context.Context) ([]repository.TradeRow, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+tradeCols+` FROM backtest_trades WHERE status = 'closed' ORDER BY exit_time`)
	if err != nil {
		return nil, fmt.Errorf("list all closed trades: %w", err)
	}
	defer rows.Close()
	return scanTrades(rows)
}

func (s *Store) ListTradesFiltered(ctx context.Context, q repository.TradeQuery) ([]repository.TradeRow, int, error) {
	where := " WHERE true"
	args := []any{}
	add := func(clause string, v any) {
		args = append(args, v)
		where += fmt.Sprintf(clause, len(args))
	}
	if q.StrategyID != "" {
		add(" AND strategy_id = $%d", q.StrategyID)
	}
	if q.Side != "" {
		add(" AND side = $%d", q.Side)
	}
	if q.Result != "" {
		add(" AND result = $%d", q.Result)
	}
	if !q.From.IsZero() {
		add(" AND entry_time >= $%d", q.From)
	}
	if !q.To.IsZero() {
		add(" AND entry_time < $%d", q.To)
	}

	var total int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM backtest_trades`+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count trades: %w", err)
	}

	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	offset := (q.Page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}
	args = append(args, pageSize, offset)
	listQ := `SELECT ` + tradeCols + ` FROM backtest_trades` + where +
		fmt.Sprintf(` ORDER BY entry_time DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))

	rows, err := s.db.Query(ctx, listQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list trades: %w", err)
	}
	defer rows.Close()

	trades, err := scanTrades(rows)
	return trades, total, err
}

func scanTrades(rows pgx.Rows) ([]repository.TradeRow, error) {
	out := []repository.TradeRow{}
	for rows.Next() {
		var t repository.TradeRow
		err := rows.Scan(&t.ID, &t.RunID, &t.StrategyID, &t.AssetID, &t.Side,
			&t.EntryTime, &t.EntryPrice, &t.ExitTime, &t.ExitPrice,
			&t.Quantity, &t.Pnl, &t.Status, &t.Result)
		if err != nil {
			return nil, fmt.Errorf("scan trade: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
