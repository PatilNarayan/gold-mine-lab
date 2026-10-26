package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/jackc/pgx/v5"
)

// InsertQuoteObservations batch-inserts deduplicated quote events — one row
// per distinct provider observation; identical re-deliveries never arrive
// here (the recorder dedupes upstream).
func (s *Store) InsertQuoteObservations(ctx context.Context, rows []repository.QuoteObservationRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, r := range rows {
		// event_key is computed caller-side (canonical format — see
		// QuoteEventKey); ON CONFLICT on the partial unique index makes
		// redelivery idempotent at the DB level — restart-safe, race-safe.
		batch.Queue(`INSERT INTO quote_observations
			(provider, symbol, provider_time, bid, ask, mid, spread_price, spread_bp, stale, received_at, event_key)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (event_key) WHERE event_key IS NOT NULL DO NOTHING`,
			r.Provider, r.Symbol, r.ProviderTime, r.Bid, r.Ask, r.Mid,
			r.SpreadPrice, r.SpreadBp, r.Stale, r.ReceivedAt,
			QuoteEventKey(r.Provider, r.Symbol, r.ProviderTime, r.Bid, r.Ask, r.Mid))
	}
	res := s.db.SendBatch(ctx, batch)
	defer res.Close()
	for i := 0; i < batch.Len(); i++ {
		if _, err := res.Exec(); err != nil {
			return fmt.Errorf("insert quote observation %d: %w", i, err)
		}
	}
	return nil
}

func (s *Store) CountQuoteObservations(ctx context.Context, symbol string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM quote_observations WHERE symbol = $1`, symbol).Scan(&n)
	return n, err
}

func (s *Store) LatestQuoteObservation(ctx context.Context, symbol string) (*repository.QuoteObservationRow, error) {
	row := s.db.QueryRow(ctx, `SELECT provider, symbol, provider_time, bid, ask, mid,
		spread_price, spread_bp, stale, received_at
		FROM quote_observations WHERE symbol = $1 ORDER BY seq DESC LIMIT 1`, symbol)
	var r repository.QuoteObservationRow
	err := row.Scan(&r.Provider, &r.Symbol, &r.ProviderTime, &r.Bid, &r.Ask, &r.Mid,
		&r.SpreadPrice, &r.SpreadBp, &r.Stale, &r.ReceivedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// QuoteEventKey builds the canonical dedupe fingerprint for one quote
// event. NULL provider_time → nil (identity unprovable — the row is stored
// but not deduplicated). Format: provider|symbol|unixMillis|bid|ask|mid
// with shortest-round-trip float formatting.
func QuoteEventKey(provider, symbol string, ptime *time.Time, bid, ask, mid *float64) *string {
	if ptime == nil {
		return nil
	}
	f := func(v *float64) string {
		if v == nil {
			return "null"
		}
		return strconv.FormatFloat(*v, 'g', -1, 64)
	}
	k := provider + "|" + symbol + "|" + strconv.FormatInt(ptime.UTC().UnixMilli(), 10) +
		"|" + f(bid) + "|" + f(ask) + "|" + f(mid)
	return &k
}

// FindQuoteInWindow returns the EARLIEST persisted observation in
// [from, to) that satisfies the live fill policy — valid uncrossed legs,
// provider timestamp present, not stale-flagged. Used for downtime
// recovery: it can only see events that were actually recorded while the
// engine was offline; it never fabricates quotes.
func (s *Store) FindQuoteInWindow(ctx context.Context, provider, symbol string, from, to time.Time) (*repository.QuoteObservationRow, error) {
	row := s.db.QueryRow(ctx, `SELECT seq, provider, symbol, provider_time, bid, ask, mid,
		spread_price, spread_bp, stale, received_at
		FROM quote_observations
		WHERE provider = $1 AND symbol = $2
		  AND provider_time >= $3 AND provider_time < $4
		  AND bid IS NOT NULL AND ask IS NOT NULL AND ask >= bid AND bid > 0
		  AND stale = false
		ORDER BY provider_time ASC LIMIT 1`, provider, symbol, from, to)
	var r repository.QuoteObservationRow
	err := row.Scan(&r.Seq, &r.Provider, &r.Symbol, &r.ProviderTime, &r.Bid, &r.Ask,
		&r.Mid, &r.SpreadPrice, &r.SpreadBp, &r.Stale, &r.ReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
