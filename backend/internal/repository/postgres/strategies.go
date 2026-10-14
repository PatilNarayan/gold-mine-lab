package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/gold-mine-lab/backend/internal/strategy"
)

func (s *Store) UpsertStrategy(ctx context.Context, d strategy.Definition) error {
	assets, err := json.Marshal(d.SupportedAssets)
	if err != nil {
		return fmt.Errorf("marshal supported_assets: %w", err)
	}
	params, err := json.Marshal(d.Parameters)
	if err != nil {
		return fmt.Errorf("marshal parameters: %w", err)
	}
	tfs, err := json.Marshal(d.SupportedTimeframes)
	if err != nil {
		return fmt.Errorf("marshal supported_timeframes: %w", err)
	}
	const q = `
		INSERT INTO strategies (id, name, version, description, supported_assets, timeframe, supported_timeframes, parameters, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE
		SET name = EXCLUDED.name, version = EXCLUDED.version, description = EXCLUDED.description,
		    supported_assets = EXCLUDED.supported_assets, timeframe = EXCLUDED.timeframe,
		    supported_timeframes = EXCLUDED.supported_timeframes,
		    parameters = EXCLUDED.parameters, status = EXCLUDED.status, updated_at = now()`
	_, err = s.db.Exec(ctx, q, d.ID, d.Name, d.Version, d.Description, assets, d.Timeframe, tfs, params, d.Status)
	if err != nil {
		return fmt.Errorf("upsert strategy %s: %w", d.ID, err)
	}
	return nil
}

const strategyCols = `id, name, version, description, supported_assets, timeframe, supported_timeframes, parameters, status`

func (s *Store) ListStrategies(ctx context.Context) ([]strategy.Definition, error) {
	rows, err := s.db.Query(ctx, `SELECT `+strategyCols+` FROM strategies ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list strategies: %w", err)
	}
	defer rows.Close()

	out := []strategy.Definition{}
	for rows.Next() {
		d, err := scanStrategy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) GetStrategy(ctx context.Context, id string) (strategy.Definition, error) {
	row := s.db.QueryRow(ctx, `SELECT `+strategyCols+` FROM strategies WHERE id = $1`, id)
	d, err := scanStrategy(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, repository.ErrNotFound
	}
	return d, err
}

func scanStrategy(row pgx.Row) (strategy.Definition, error) {
	var d strategy.Definition
	var assets, tfs, params []byte
	var tf string
	err := row.Scan(&d.ID, &d.Name, &d.Version, &d.Description, &assets, &tf, &tfs, &params, &d.Status)
	if err != nil {
		return d, err
	}
	d.Timeframe = market.Timeframe(tf)
	if err := json.Unmarshal(assets, &d.SupportedAssets); err != nil {
		return d, fmt.Errorf("decode supported_assets: %w", err)
	}
	if len(tfs) > 0 {
		if err := json.Unmarshal(tfs, &d.SupportedTimeframes); err != nil {
			return d, fmt.Errorf("decode supported_timeframes: %w", err)
		}
	}
	if err := json.Unmarshal(params, &d.Parameters); err != nil {
		return d, fmt.Errorf("decode parameters: %w", err)
	}
	return d, nil
}
