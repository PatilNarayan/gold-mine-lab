package postgres

import (
	"bytes"
	"context"
	"encoding/json"
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

func (s *Store) GetPendingOrder(ctx context.Context, sessionID int64, strategyID, version, symbol string, tf market.Timeframe) (*repository.ShadowOrderRow, error) {
	row := s.db.QueryRow(ctx, `SELECT id, order_key, session_id, strategy_id, strategy_version, symbol,
		timeframe, side, signal_bar_time, decision_at, status, created_at
		FROM shadow_orders
		WHERE (($5::bigint)=0 AND session_id IS NULL OR session_id=$5)
		  AND strategy_id=$1 AND strategy_version=$2 AND symbol=$3 AND timeframe=$4 AND status='pending'
		ORDER BY id DESC LIMIT 1`, strategyID, version, symbol, string(tf), sessionID)
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
		ON CONFLICT (session_id, strategy_id, strategy_version, symbol, timeframe) DO UPDATE SET
			side=$6, quantity=$7, entry_price=$8, entry_at=$9, entry_order_id=$10,
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

// CommitBar persists one candle's complete shadow transition in a SINGLE
// transaction: order insert (order_key dedupe) → optional fill or terminal
// resolution on that order → ledger decision (unique-key dedupe) →
// position/checkpoint upsert. A rollback leaves every element unwritten —
// the durable checkpoint never half-advances. Fill/Resolve apply only when
// this transaction created the order row: an existing row already carries
// its committed outcome and is never rewritten by a replay.
func (s *Store) CommitBar(ctx context.Context, c repository.ShadowBarCommit) (repository.ShadowBarResult, error) {
	var res repository.ShadowBarResult
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	if c.NewOrder != nil {
		o := c.NewOrder
		err := tx.QueryRow(ctx, `INSERT INTO shadow_orders
			(order_key, session_id, strategy_id, strategy_version, symbol, timeframe, side,
			 signal_bar_time, decision_at, status)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending')
			ON CONFLICT (order_key) DO NOTHING
			RETURNING id`,
			o.OrderKey, o.SessionID, o.StrategyID, o.StrategyVersion, o.Symbol, o.Timeframe,
			o.Side, o.SignalBarTime, o.DecisionAt).Scan(&res.OrderID)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if err := tx.QueryRow(ctx, `SELECT id FROM shadow_orders WHERE order_key=$1`,
				o.OrderKey).Scan(&res.OrderID); err != nil {
				return res, fmt.Errorf("fetch existing order %q: %w", o.OrderKey, err)
			}
		case err != nil:
			return res, err
		default:
			res.OrderCreated = true
		}
	}

	// Terminal outcomes apply ONLY to a row this transaction created — a
	// pre-existing order already carries its committed status.
	if res.OrderCreated && c.Fill != nil {
		f := c.Fill
		if _, err := tx.Exec(ctx, `UPDATE shadow_orders SET
			status='filled', quote_provider_time=$2, quote_received_at=$3,
			quote_bid=$4, quote_ask=$5, quote_mid=$6,
			fill_price=$7, filled_at=$8, quantity=$9, commission=$10
			WHERE id=$1`,
			res.OrderID, f.QuoteProviderTime, f.QuoteReceivedAt, f.QuoteBid, f.QuoteAsk,
			f.QuoteMid, f.FillPrice, f.FilledAt, f.Quantity, f.Commission); err != nil {
			return res, err
		}
	}
	if res.OrderCreated && c.Resolve != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE shadow_orders SET status=$2, reject_reason=$3 WHERE id=$1`,
			res.OrderID, c.Resolve.Status, c.Resolve.Reason); err != nil {
			return res, err
		}
	}

	if c.Decision != nil {
		d := *c.Decision
		if d.OrderID == nil && res.OrderID != 0 {
			d.OrderID = &res.OrderID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO shadow_decisions
			(session_id, order_id, strategy_id, strategy_version, symbol, timeframe,
			 bar_time, decided_at, source, action, reason, fast_ema, slow_ema,
			 actionable, blocked_reason)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
			ON CONFLICT (session_id, strategy_id, strategy_version, symbol, timeframe, bar_time)
			DO NOTHING`,
			d.SessionID, d.OrderID, d.StrategyID, d.StrategyVersion, d.Symbol, d.Timeframe,
			d.BarTime, d.DecidedAt, d.Source, d.Action, d.Reason, d.FastEMA, d.SlowEMA,
			d.Actionable, d.BlockedReason); err != nil {
			return res, err
		}
	}

	if err := upsertShadowPosition(ctx, tx, c.Position); err != nil {
		return res, err
	}
	return res, tx.Commit(ctx)
}

func (s *Store) GetPosition(ctx context.Context, sessionID int64, strategyID, version, symbol string, tf market.Timeframe) (*repository.ShadowPositionRow, error) {
	row := s.db.QueryRow(ctx, `SELECT strategy_id, strategy_version, symbol, timeframe, session_id, side,
		quantity, entry_price, entry_at, entry_order_id, entry_commission,
		realized_pnl, realized_comm, realized_spread, last_bar_time, updated_at
		FROM shadow_positions
		WHERE (($5::bigint)=0 AND session_id IS NULL OR session_id=$5)
		  AND strategy_id=$1 AND strategy_version=$2 AND symbol=$3 AND timeframe=$4`,
		strategyID, version, symbol, string(tf), sessionID)
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
		// Open session exists — but its CONFIG must match too. The strategy
		// identity alone doesn't cover the engine-policy fields; a changed
		// fingerprint is a genuinely different configuration → retire the
		// old session and open a distinctly identified new one rather than
		// silently absorbing a config change.
		var stored []byte
		err := s.db.QueryRow(ctx, `SELECT config, id FROM shadow_sessions
			WHERE strategy_id=$1 AND strategy_version=$2 AND symbol=$3
			  AND timeframe=$4 AND provider=$5 AND ended_at IS NULL`,
			sess.StrategyID, sess.StrategyVersion, sess.Symbol, sess.Timeframe,
			sess.Provider).Scan(&stored, &id)
		if err != nil {
			return nil, err
		}
		if !jsonEqual(stored, sess.Config) {
			if _, err := s.db.Exec(ctx, `UPDATE shadow_sessions SET ended_at=now()
				WHERE id=$1`, id); err != nil {
				return nil, err
			}
			if err := s.db.QueryRow(ctx, `INSERT INTO shadow_sessions
				(strategy_id, strategy_version, symbol, timeframe, provider, config)
				VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
				sess.StrategyID, sess.StrategyVersion, sess.Symbol, sess.Timeframe,
				sess.Provider, sess.Config).Scan(&id); err != nil {
				return nil, err
			}
			sess.ID = id
			return &sess, nil // new session, restart_count=0
		}
		// Same config — genuine resume.
		if err := s.db.QueryRow(ctx, `UPDATE shadow_sessions
			SET restart_count = restart_count + 1, last_active_at = now()
			WHERE id=$1 RETURNING id, restart_count`, id).
			Scan(&id, &sess.RestartCount); err != nil {
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

// jsonEqual compares two config snapshots semantically (key order is not
// stable across serialization runs).
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return bytes.Equal(a, b)
	}
	bx, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return bytes.Equal(bx, by)
}

// RecordDecision appends one decision-ledger row. The unique key
// (identity+bar_time) makes replay/recovery idempotent via DO NOTHING —
// the FIRST recorded evaluation is immutable: a recovery pass re-deciding
// an already-evaluated bar can never overwrite the original decided_at,
// indicator values, or actionability (its own evidence stays appended
// nowhere — the row IS the evaluation record).
func (s *Store) RecordDecision(ctx context.Context, d repository.ShadowDecisionRow) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO shadow_decisions
		(session_id, order_id, strategy_id, strategy_version, symbol, timeframe,
		 bar_time, decided_at, source, action, reason, fast_ema, slow_ema,
		 actionable, blocked_reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		ON CONFLICT (session_id, strategy_id, strategy_version, symbol, timeframe, bar_time)
		DO NOTHING
		RETURNING id`,
		d.SessionID, d.OrderID, d.StrategyID, d.StrategyVersion, d.Symbol, d.Timeframe,
		d.BarTime, d.DecidedAt, d.Source, d.Action, d.Reason, d.FastEMA, d.SlowEMA,
		d.Actionable, d.BlockedReason).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil // already evaluated — original preserved
	}
	return id, err
}

// ListDecisions returns the most recent evaluation rows, newest first.
func (s *Store) ListDecisions(ctx context.Context, strategyID, version, symbol string, tf market.Timeframe, limit int) ([]repository.ShadowDecisionRow, error) {
	rows, err := s.db.Query(ctx, `SELECT id, session_id, order_id, strategy_id, strategy_version,
		symbol, timeframe, bar_time, decided_at, source, action, reason,
		fast_ema, slow_ema, actionable, blocked_reason
		FROM shadow_decisions
		WHERE strategy_id=$1 AND strategy_version=$2 AND symbol=$3 AND timeframe=$4
		ORDER BY bar_time DESC LIMIT $5`, strategyID, version, symbol, string(tf), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []repository.ShadowDecisionRow{}
	for rows.Next() {
		var d repository.ShadowDecisionRow
		if err := rows.Scan(&d.ID, &d.SessionID, &d.OrderID, &d.StrategyID, &d.StrategyVersion,
			&d.Symbol, &d.Timeframe, &d.BarTime, &d.DecidedAt, &d.Source, &d.Action, &d.Reason,
			&d.FastEMA, &d.SlowEMA, &d.Actionable, &d.BlockedReason); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
