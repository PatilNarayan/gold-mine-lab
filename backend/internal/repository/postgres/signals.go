package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/gold-mine-lab/backend/internal/strategy"
)

// InsertSignals writes signals idempotently — the (strategy, asset, ts) unique
// constraint makes regeneration safe. Returns rows inserted.
func (s *Store) InsertSignals(ctx context.Context, signals []strategy.Signal) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after commit

	batch := &pgx.Batch{}
	for _, sig := range signals {
		md, _ := json.Marshal(sig.Metadata)
		batch.Queue(`
			INSERT INTO strategy_signals (strategy_id, asset_id, timestamp, action, score, price, reason, metadata)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (strategy_id, asset_id, timestamp) DO NOTHING`,
			sig.StrategyID, sig.AssetID, sig.Timestamp, string(sig.Action), sig.Score, sig.Price, sig.Reason, md)
	}
	res := tx.SendBatch(ctx, batch)
	inserted := 0
	for i := 0; i < batch.Len(); i++ {
		tag, err := res.Exec()
		if err != nil {
			res.Close()
			return 0, fmt.Errorf("insert signal %d: %w", i, err)
		}
		inserted += int(tag.RowsAffected())
	}
	if err := res.Close(); err != nil {
		return 0, fmt.Errorf("close batch: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return inserted, nil
}

const signalCols = `id::text, strategy_id, asset_id, action, score, price, timestamp, reason, metadata`

func (s *Store) ListSignals(ctx context.Context, assetID, strategyID string, limit int) ([]strategy.Signal, error) {
	q := `SELECT ` + signalCols + ` FROM strategy_signals WHERE true`
	args := []any{}
	if assetID != "" {
		args = append(args, assetID)
		q += fmt.Sprintf(" AND asset_id = $%d", len(args))
	}
	if strategyID != "" {
		args = append(args, strategyID)
		q += fmt.Sprintf(" AND strategy_id = $%d", len(args))
	}
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY timestamp DESC LIMIT $%d", len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list signals: %w", err)
	}
	defer rows.Close()

	out := []strategy.Signal{}
	for rows.Next() {
		sig, err := scanSignal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sig)
	}
	return out, rows.Err()
}

func (s *Store) LatestSignal(ctx context.Context, strategyID string) (*strategy.Signal, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+signalCols+` FROM strategy_signals WHERE strategy_id = $1 ORDER BY timestamp DESC LIMIT 1`,
		strategyID)
	sig, err := scanSignal(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &sig, nil
}

func scanSignal(row pgx.Row) (strategy.Signal, error) {
	var sig strategy.Signal
	var action string
	var md []byte
	err := row.Scan(&sig.ID, &sig.StrategyID, &sig.AssetID, &action, &sig.Score, &sig.Price, &sig.Timestamp, &sig.Reason, &md)
	if err != nil {
		return sig, err
	}
	sig.Action = strategy.Action(action)
	sig.Timestamp = sig.Timestamp.UTC() // API contract: ISO-8601 UTC
	if len(md) > 0 {
		_ = json.Unmarshal(md, &sig.Metadata)
	}
	return sig, nil
}
