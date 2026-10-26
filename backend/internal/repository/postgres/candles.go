package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gold-mine-lab/backend/internal/market"
)

// CandleStore persists provider-validated candles + sync coverage. Bar times
// are canonical Kyiv civil-time session labels — identical labels always
// denote identical market intervals across sources.
type CandleStore struct {
	db *pgxpool.Pool
}

func NewCandleStore(db *pgxpool.Pool) *CandleStore { return &CandleStore{db: db} }

func (s *CandleStore) UpsertCandles(ctx context.Context, assetID string, tf market.Timeframe, provider string, cs []market.Candle) error {
	batch := &pgx.Batch{}
	const q = `
		INSERT INTO market_candles (asset_id, timeframe, bar_time, open, high, low, close, volume, provider)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (asset_id, timeframe, bar_time) DO UPDATE
		SET open = EXCLUDED.open, high = EXCLUDED.high, low = EXCLUDED.low,
		    close = EXCLUDED.close, volume = EXCLUDED.volume,
		    provider = EXCLUDED.provider, fetched_at = now()`
	for _, c := range cs {
		batch.Queue(q, assetID, string(tf), c.Time, c.Open, c.High, c.Low, c.Close, c.Volume, provider)
	}
	br := s.db.SendBatch(ctx, batch)
	defer br.Close()
	for range cs {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("upsert candles %s %s: %w", assetID, tf, err)
		}
	}
	return nil
}

func (s *CandleStore) LoadCandles(ctx context.Context, assetID string, tf market.Timeframe, from, to time.Time) ([]market.Candle, error) {
	const q = `
		SELECT bar_time, open, high, low, close, volume
		FROM market_candles
		WHERE asset_id = $1 AND timeframe = $2 AND bar_time >= $3 AND bar_time < $4
		ORDER BY bar_time`
	rows, err := s.db.Query(ctx, q, assetID, string(tf), from, to)
	if err != nil {
		return nil, fmt.Errorf("load candles %s %s: %w", assetID, tf, err)
	}
	defer rows.Close()
	out := make([]market.Candle, 0, 512)
	for rows.Next() {
		var c market.Candle
		if err := rows.Scan(&c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); err != nil {
			return nil, fmt.Errorf("scan candle: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *CandleStore) TailCandles(ctx context.Context, assetID string, tf market.Timeframe, n int, before time.Time) ([]market.Candle, error) {
	const q = `
		SELECT bar_time, open, high, low, close, volume
		FROM market_candles
		WHERE asset_id = $1 AND timeframe = $2 AND bar_time <= $3
		ORDER BY bar_time DESC LIMIT $4`
	rows, err := s.db.Query(ctx, q, assetID, string(tf), before, n)
	if err != nil {
		return nil, fmt.Errorf("tail candles %s %s: %w", assetID, tf, err)
	}
	defer rows.Close()
	out := make([]market.Candle, 0, n)
	for rows.Next() {
		var c market.Candle
		if err := rows.Scan(&c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); err != nil {
			return nil, fmt.Errorf("scan candle: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse to ascending.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *CandleStore) Coverage(ctx context.Context, assetID string, tf market.Timeframe) (market.Coverage, error) {
	const q = `
		SELECT provider, first_bar, last_bar,
		       COALESCE((SELECT count(*) FROM market_candles
		         WHERE asset_id = $1 AND timeframe = $2
		           AND bar_time >= first_bar AND bar_time <= last_bar), 0),
		       COALESCE(state, 'complete'), COALESCE(last_error, ''),
		       COALESCE(retry_at, 'epoch'), COALESCE(progress_bar, 'epoch'),
		       COALESCE(progress_dir, '')
		FROM candle_sync WHERE asset_id = $1 AND timeframe = $2`
	var cov market.Coverage
	err := s.db.QueryRow(ctx, q, assetID, string(tf)).Scan(
		&cov.Provider, &cov.First, &cov.Last, &cov.Count,
		&cov.State, &cov.LastError, &cov.RetryAt, &cov.ProgressBar, &cov.ProgressDir)
	if err == pgx.ErrNoRows {
		return market.Coverage{}, nil
	}
	if err != nil {
		return market.Coverage{}, fmt.Errorf("coverage %s %s: %w", assetID, tf, err)
	}
	// 'epoch' sentinels → zero times.
	for _, t := range []*time.Time{&cov.RetryAt, &cov.ProgressBar} {
		if t.Year() <= 1 {
			*t = time.Time{}
		}
	}
	return cov, nil
}

func (s *CandleStore) SetCoverage(ctx context.Context, assetID string, tf market.Timeframe, cov market.Coverage) error {
	const q = `
		INSERT INTO candle_sync (asset_id, timeframe, provider, first_bar, last_bar,
		                         state, last_error, retry_at, progress_bar, progress_dir)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (asset_id, timeframe) DO UPDATE
		SET provider = EXCLUDED.provider, first_bar = EXCLUDED.first_bar,
		    last_bar = EXCLUDED.last_bar, synced_at = now(),
		    state = EXCLUDED.state, last_error = EXCLUDED.last_error,
		    retry_at = EXCLUDED.retry_at, progress_bar = EXCLUDED.progress_bar,
		    progress_dir = EXCLUDED.progress_dir`
	retry := any(cov.RetryAt)
	if cov.RetryAt.IsZero() {
		retry = nil
	}
	prog := any(cov.ProgressBar)
	if cov.ProgressBar.IsZero() {
		prog = nil
	}
	_, err := s.db.Exec(ctx, q, assetID, string(tf), cov.Provider, cov.First, cov.Last,
		cov.State, nilIfEmpty(cov.LastError), retry, prog, nilIfEmpty(cov.ProgressDir))
	if err != nil {
		return fmt.Errorf("set coverage %s %s: %w", assetID, tf, err)
	}
	return nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
