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

func (s *Store) StartCoverage(ctx context.Context, provider, symbol string) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO quote_coverage (provider, symbol)
		VALUES ($1,$2) RETURNING id`, provider, symbol).Scan(&id)
	return id, err
}

func (s *Store) HeartbeatCoverage(ctx context.Context, id int64, sample repository.CoverageSample) error {
	_, err := s.db.Exec(ctx, `UPDATE quote_coverage SET
		last_heartbeat_at=now(), last_obs_at=COALESCE($2,last_obs_at),
		obs_count=$3, dropped_batches=$8,
		last_drop_at=COALESCE($4,last_drop_at),
		last_feed_degraded_at=COALESCE($5,last_feed_degraded_at),
		last_heartbeat_gap_at=COALESCE($6,last_heartbeat_gap_at),
		feed_state=COALESCE(NULLIF($7,''),feed_state) WHERE id=$1`,
		id, sample.LastObsAt, sample.ObsCount, sample.LastDropAt,
		sample.LastFeedDegradedAt, sample.LastHeartbeatGapAt, sample.FeedState,
		sample.DroppedBatches)
	return err
}

func (s *Store) EndCoverage(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `UPDATE quote_coverage SET ended_at=now() WHERE id=$1`, id)
	return err
}

// CoverageComplete is true only when a recorder interval PROVES [from,to)
// was observable under the execution policy. Required evidence:
//
//   - recorder interval started before the window (started_at <= from);
//   - heartbeat persisted past the window end (last_heartbeat_at >= to) —
//     proving aliveness AND a working write path through it;
//   - no heartbeat-continuity gap inside/spanning the window
//     (last_heartbeat_gap_at < from or absent) — a stalled process can't
//     certify the interval it slept through;
//   - no persistence failure inside the window (last_drop_at < from);
//   - no upstream-feed degradation observed inside the window
//     (last_feed_degraded_at < from) — a live recorder over a dead feed
//     does not establish observability.
//
// Degradations wholly before `from` do not taint the window; anything
// unproven returns false — never silently treated as "no quotes existed".
func (s *Store) CoverageComplete(ctx context.Context, provider, symbol string, from, to time.Time) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM quote_coverage
		WHERE provider=$1 AND symbol=$2
		  AND started_at <= $3
		  AND last_heartbeat_at >= $4
		  AND COALESCE(ended_at, last_heartbeat_at) >= $4
		  AND (last_drop_at IS NULL OR last_drop_at < $3)
		  AND (last_feed_degraded_at IS NULL OR last_feed_degraded_at < $3)
		  AND (last_heartbeat_gap_at IS NULL OR last_heartbeat_gap_at < $3)
		  -- Transition-event overlap: a degraded interval [COALESCE(since,at),
		  -- next-recovery) overlapping the window means the feed cannot be
		  -- proven healthy throughout — point containment alone misses
		  -- outages that SPAN a window between its events.
		  AND NOT EXISTS (
		      SELECT 1 FROM feed_events d
		      WHERE d.coverage_id = quote_coverage.id
		        AND (
		            (d.kind = 'persist_fail' AND d.at >= $3 AND d.at < $4)
		            OR
		            (d.kind IN ('disconnect','degraded')
		             AND COALESCE(d.since, d.at) < $4
		             AND COALESCE((
		                 SELECT min(r.at) FROM feed_events r
		                 WHERE r.coverage_id = quote_coverage.id
		                   AND r.kind = 'recovered' AND r.at > d.at
		             ), 'infinity'::timestamptz) > $3)
		        )))`,
		provider, symbol, from, to).Scan(&ok)
	return ok, err
}

func (s *Store) OpenCoverage(ctx context.Context, provider, symbol string) (*repository.QuoteCoverageRow, error) {
	var r repository.QuoteCoverageRow
	err := s.db.QueryRow(ctx, `SELECT id, provider, symbol, started_at, last_heartbeat_at,
		last_obs_at, obs_count, dropped_batches, ended_at,
		last_feed_degraded_at, last_heartbeat_gap_at, last_drop_at, feed_state
		FROM quote_coverage
		WHERE provider=$1 AND symbol=$2 AND ended_at IS NULL
		ORDER BY id DESC LIMIT 1`, provider, symbol).
		Scan(&r.ID, &r.Provider, &r.Symbol, &r.StartedAt, &r.LastHeartbeatAt,
			&r.LastObsAt, &r.ObsCount, &r.DroppedBatches, &r.EndedAt,
			&r.LastFeedDegradedAt, &r.LastHeartbeatGapAt, &r.LastDropAt, &r.FeedState)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (s *Store) InsertFeedEvent(ctx context.Context, ev repository.FeedEventRow) error {
	_, err := s.db.Exec(ctx, `INSERT INTO feed_events
		(coverage_id, provider, symbol, kind, at, since, detail)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		ev.CoverageID, ev.Provider, ev.Symbol, ev.Kind, ev.At, ev.Since, ev.Detail)
	return err
}
