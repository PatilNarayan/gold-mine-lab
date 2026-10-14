package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
)

func (s *Store) UpsertAsset(ctx context.Context, a market.Asset) error {
	const q = `
		INSERT INTO assets (id, symbol, name, asset_class, currency, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE
		SET symbol = EXCLUDED.symbol, name = EXCLUDED.name,
		    asset_class = EXCLUDED.asset_class, currency = EXCLUDED.currency,
		    metadata = EXCLUDED.metadata`
	_, err := s.db.Exec(ctx, q, a.ID, a.Symbol, a.Name, a.AssetClass, a.QuoteCurrency, map[string]any{})
	if err != nil {
		return fmt.Errorf("upsert asset %s: %w", a.ID, err)
	}
	return nil
}

func (s *Store) ListAssets(ctx context.Context) ([]market.Asset, error) {
	const q = `SELECT id, symbol, name, asset_class, currency FROM assets ORDER BY symbol`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()

	out := []market.Asset{}
	for rows.Next() {
		var a market.Asset
		if err := rows.Scan(&a.ID, &a.Symbol, &a.Name, &a.AssetClass, &a.QuoteCurrency); err != nil {
			return nil, fmt.Errorf("scan asset: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAsset(ctx context.Context, id string) (market.Asset, error) {
	const q = `SELECT id, symbol, name, asset_class, currency FROM assets WHERE id = $1`
	var a market.Asset
	err := s.db.QueryRow(ctx, q, id).Scan(&a.ID, &a.Symbol, &a.Name, &a.AssetClass, &a.QuoteCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, repository.ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("get asset %s: %w", id, err)
	}
	return a, nil
}
