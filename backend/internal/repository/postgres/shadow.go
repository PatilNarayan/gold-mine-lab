package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/gold-mine-lab/backend/internal/market"
	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/jackc/pgx/v5"
)

// CreateOrderIfAbsent inserts a pending order; a duplicate order_key
// (replay/reconnect/restart) returns created=false with the existing id.
func (s *Store) CreateOrderIfAbsent(ctx context.Context, o repository.ShadowOrderRow) (int64, bool, error) {
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO shadow_orders
		(order_key, session_id, strategy_id, strategy_version, symbol, timeframe, side,
		 signal_bar_time, decision_at, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending')
		ON CONFLICT (order_key) DO NOTHING
		RETURNING id`,
		o.OrderKey, o.SessionID, o.StrategyID, o.StrategyVersion, o.Symbol, o.Timeframe,
		o.Side, o.SignalBarTime, o.DecisionAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		var existing int64
		if err := s.db.QueryRow(ctx, `SELECT id FROM shadow_orders WHERE order_key=$1`, o.OrderKey).Scan(&existing); err != nil {
			return 0, false, fmt.Errorf("fetch existing order %q: %w", o.OrderKey, err)
		}
		return existing, false, nil
	}
	return id, err == nil, err
}

func (s *Store) GetPendingOrder(ctx context.Context, strategyID, version, symbol string, tf market.Timeframe) (*repository.ShadowOrderRow, error) {
	row := s.db.QueryRow(ctx, `SELECT id, order_key, session_id, strategy_id, strategy_version, symbol,
		timeframe, side, signal_bar_time, decision_at, status, created_at
		FROM shadow_orders
		WHERE strategy_id=$1 AND strategy_version=$2 AND symbol=$3 AND timeframe=$4 AND status='pending'
		ORDER BY id DESC LIMIT 1`, strategyID, version, symbol, string(tf))
	return scanShadowOrder(row)
}

func scanShadowOrder(row pgx.Row) (*repository.ShadowOrderRow, error) {
	var o repository.ShadowOrderRow
	err := row.Scan(&o.ID, &o.OrderKey, &o.SessionID, &o.StrategyID, &o.StrategyVersion, &o.Symbol,
		&o.Timeframe, &o.Side, &o.SignalBarTime, &o.DecisionAt, &o.Status, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) SetOrderStatus(ctx context.Context, orderID int64, status, reason string) error {
	_, err := s.db.Exec(ctx, `UPDATE shadow_orders SET status=$2, reject_reason=$3 WHERE id=$1`,
		orderID, status, reason)
	return err
}

// FillOrderAndPosition commits the order's fill fields and the position
// transition in ONE transaction — a crash between them can't leave a
// filled order against an un-updated position.
func (s *Store) FillOrderAndPosition(ctx context.Context, orderID int64, f repository.ShadowFillRecord, pos repository.ShadowPositionRow) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE shadow_orders SET
		status='filled', quote_provider_time=$2, quote_received_at=$3,
		quote_bid=$4, quote_ask=$5, quote_mid=$6,
		fill_price=$7, filled_at=$8, quantity=$9, commission=$10
		WHERE id=$1`,
		orderID, f.QuoteProviderTime, f.QuoteReceivedAt, f.QuoteBid, f.QuoteAsk, f.QuoteMid,
		f.FillPrice, f.FilledAt, f.Quantity, f.Commission); err != nil {
		return err
	}
	if err := upsertShadowPosition(ctx, tx, pos); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func upsertShadowPosition(ctx context.Context, db dbtx, p repository.ShadowPositionRow) error {
	_, err := db.Exec(ctx, `INSERT INTO shadow_positions
		(strategy_id, strategy_version, symbol, timeframe, session_id, side, quantity,
		 entry_price, entry_at, entry_order_id, entry_commission,
		 realized_pnl, realized_comm, realized_spread, last_bar_time, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,now())
		ON CONFLICT (strategy_id, strategy_version, symbol, timeframe) DO UPDATE SET
			session_id=$5, side=$6, quantity=$7, entry_price=$8, entry_at=$9, entry_order_id=$10,
			entry_commission=$11, realized_pnl=$12, realized_comm=$13,
			realized_spread=$14, last_bar_time=$15, updated_at=now()`,
		p.StrategyID, p.StrategyVersion, p.Symbol, p.Timeframe, p.SessionID, p.Side, p.Quantity,
		p.EntryPrice, p.EntryAt, p.EntryOrderID, p.EntryCommission,
		p.RealizedPnl, p.RealizedComm, p.RealizedSpread, p.LastBarTime)
	return err
}

func (s *Store) UpsertPosition(ctx context.Context, p repository.ShadowPositionRow) error {
	return upsertShadowPosition(ctx, s.db, p)
}

func (s *Store) GetPosition(ctx context.Context, strategyID, version, symbol string, tf market.Timeframe) (*repository.ShadowPositionRow, error) {
	row := s.db.QueryRow(ctx, `SELECT strategy_id, strategy_version, symbol, timeframe, session_id, side,
		quantity, entry_price, entry_at, entry_order_id, entry_commission,
		realized_pnl, realized_comm, realized_spread, last_bar_time, updated_at
		FROM shadow_positions
		WHERE strategy_id=$1 AND strategy_version=$2 AND symbol=$3 AND timeframe=$4`,
		strategyID, version, symbol, string(tf))
	var p repository.ShadowPositionRow
	err := row.Scan(&p.StrategyID, &p.StrategyVersion, &p.Symbol, &p.Timeframe, &p.SessionID, &p.Side,
		&p.Quantity, &p.EntryPrice, &p.EntryAt, &p.EntryOrderID, &p.EntryCommission,
		&p.RealizedPnl, &p.RealizedComm, &p.RealizedSpread, &p.LastBarTime, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) ListOrders(ctx context.Context, strategyID, symbol string, tf market.Timeframe, limit int) ([]repository.ShadowOrderRow, error) {
	rows, err := s.db.Query(ctx, `SELECT id, order_key, session_id, strategy_id, strategy_version, symbol,
		timeframe, side, signal_bar_time, decision_at, status, reject_reason,
		quote_provider_time, quote_received_at, quote_bid, quote_ask, quote_mid,
		fill_price, filled_at, quantity, commission, created_at
		FROM shadow_orders
		WHERE strategy_id=$1 AND symbol=$2 AND timeframe=$3
		ORDER BY id DESC LIMIT $4`, strategyID, symbol, string(tf), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repository.ShadowOrderRow
	for rows.Next() {
		var o repository.ShadowOrderRow
		if err := rows.Scan(&o.ID, &o.OrderKey, &o.SessionID, &o.StrategyID, &o.StrategyVersion, &o.Symbol,
			&o.Timeframe, &o.Side, &o.SignalBarTime, &o.DecisionAt, &o.Status, &o.RejectReason,
			&o.QuoteProviderTime, &o.QuoteReceivedAt, &o.QuoteBid, &o.QuoteAsk, &o.QuoteMid,
			&o.FillPrice, &o.FilledAt, &o.Quantity, &o.Commission, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GetOrCreateSession returns the OPEN session for this engine identity —
// a restart resumes the same row (restart_count++); a fresh/config-changed
// engine gets a new session. The unique open-session index guarantees one.
func (s *Store) GetOrCreateSession(ctx context.Context, sess repository.ShadowSessionRow) (*repository.ShadowSessionRow, error) {
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO shadow_sessions
		(strategy_id, strategy_version, symbol, timeframe, provider, config)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (strategy_id, strategy_version, symbol, timeframe, provider)
		WHERE ended_at IS NULL DO NOTHING
		RETURNING id`, sess.StrategyID, sess.StrategyVersion, sess.Symbol,
		sess.Timeframe, sess.Provider, sess.Config).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Session exists — resume it (same session, restart recorded).
		if err := s.db.QueryRow(ctx, `UPDATE shadow_sessions
			SET restart_count = restart_count + 1, last_active_at = now()
			WHERE strategy_id=$1 AND strategy_version=$2 AND symbol=$3
			  AND timeframe=$4 AND provider=$5 AND ended_at IS NULL
			RETURNING id, restart_count`,
			sess.StrategyID, sess.StrategyVersion, sess.Symbol, sess.Timeframe,
			sess.Provider).Scan(&id, &sess.RestartCount); err != nil {
			return nil, err
		}
	}
	sess.ID = id
	return &sess, nil
}

func (s *Store) TouchSession(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `UPDATE shadow_sessions SET last_active_at=now() WHERE id=$1`, id)
	return err
}

// GetOpenSession returns the open session for an engine identity (nil = none).
func (s *Store) GetOpenSession(ctx context.Context, strategyID, version, symbol, tf, provider string) (*repository.ShadowSessionRow, error) {
	var row repository.ShadowSessionRow
	err := s.db.QueryRow(ctx, `SELECT id, strategy_id, strategy_version, symbol, timeframe,
		provider, config, started_at, last_active_at, restart_count, ended_at
		FROM shadow_sessions
		WHERE strategy_id=$1 AND strategy_version=$2 AND symbol=$3 AND timeframe=$4
		  AND provider=$5 AND ended_at IS NULL`,
		strategyID, version, symbol, tf, provider).
		Scan(&row.ID, &row.StrategyID, &row.StrategyVersion, &row.Symbol, &row.Timeframe,
			&row.Provider, &row.Config, &row.StartedAt, &row.LastActiveAt, &row.RestartCount, &row.EndedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}
