package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gold-mine-lab/backend/internal/repository"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const studyColumns = `id::text,status,name,description,hypothesis,asset_id,timeframe,
	dataset_fingerprint,dev_start::text,dev_end::text,holdout_start::text,holdout_end::text,
	spec,spec_hash,research_counts,executions,last_error,created_at,updated_at,frozen_at,
	exploratory_completed_at,holdout_consumed_at`

func (s *Store) CreateStudy(ctx context.Context, r repository.ResearchStudyRow) (repository.ResearchStudyRow, error) {
	q := `INSERT INTO research_studies
		(name,description,hypothesis,asset_id,timeframe,dataset_fingerprint,dev_start,dev_end,
		holdout_start,holdout_end,spec,spec_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING ` + studyColumns
	return scanStudy(s.db.QueryRow(ctx, q, r.Name, r.Description, r.Hypothesis, r.AssetID,
		r.Timeframe, r.DatasetFingerprint, r.DevStart, r.DevEnd, r.HoldoutStart, r.HoldoutEnd,
		r.Spec, r.SpecHash))
}

func (s *Store) UpdateStudyDraft(ctx context.Context, r repository.ResearchStudyRow) (repository.ResearchStudyRow, error) {
	q := `UPDATE research_studies SET
		name=$2,description=$3,hypothesis=$4,asset_id=$5,timeframe=$6,dataset_fingerprint=$7,
		dev_start=$8,dev_end=$9,holdout_start=$10,holdout_end=$11,spec=$12,spec_hash=$13,
		updated_at=now()
		WHERE id=$1 AND status='draft' RETURNING ` + studyColumns
	updated, err := scanStudy(s.db.QueryRow(ctx, q, r.ID, r.Name, r.Description, r.Hypothesis, r.AssetID,
		r.Timeframe, r.DatasetFingerprint, r.DevStart, r.DevEnd, r.HoldoutStart, r.HoldoutEnd,
		r.Spec, r.SpecHash))
	if err == nil {
		return updated, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return repository.ResearchStudyRow{}, err
	}
	if _, getErr := s.GetStudy(ctx, r.ID); getErr != nil {
		return repository.ResearchStudyRow{}, getErr
	}
	return repository.ResearchStudyRow{}, repository.ErrConflict
}

func (s *Store) GetStudy(ctx context.Context, id string) (repository.ResearchStudyRow, error) {
	r, err := scanStudy(s.db.QueryRow(ctx, `SELECT `+studyColumns+` FROM research_studies WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return r, repository.ErrNotFound
	}
	if isInvalidUUID(err) {
		return r, repository.ErrNotFound
	}
	return r, err
}

func (s *Store) ListStudies(ctx context.Context) ([]repository.ResearchStudyRow, error) {
	rows, err := s.db.Query(ctx, `SELECT `+studyColumns+` FROM research_studies ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list research studies: %w", err)
	}
	defer rows.Close()
	out := make([]repository.ResearchStudyRow, 0)
	for rows.Next() {
		r, err := scanStudy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) FreezeStudy(ctx context.Context, id string, counts []byte) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var asset, tf string
	if err := tx.QueryRow(ctx, `SELECT asset_id,timeframe FROM research_studies WHERE id=$1`, id).Scan(&asset, &tf); err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return "", repository.ErrNotFound
		}
		return "", err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, asset+"|"+tf); err != nil {
		return "", err
	}
	r, err := scanStudy(tx.QueryRow(ctx, `SELECT `+studyColumns+` FROM research_studies WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", repository.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if r.Status != "draft" {
		return "", repository.ErrConflict
	}
	var conflict string
	err = tx.QueryRow(ctx, `SELECT id::text FROM research_studies
		WHERE asset_id=$1 AND timeframe=$2 AND status IN ('frozen','exploratory_complete')
		AND holdout_start <= $4::date AND holdout_end >= $3::date AND id <> $5
		ORDER BY holdout_start LIMIT 1`, asset, tf, r.HoldoutStart, r.HoldoutEnd, id).Scan(&conflict)
	if err == nil {
		return conflict, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE research_studies SET status='frozen',research_counts=$2,
		frozen_at=now(),updated_at=now() WHERE id=$1`, id, counts)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return "", nil
}

func (s *Store) AppendStudyExecution(ctx context.Context, id, newStatus string, executionJSON []byte, errMsg *string) (repository.ResearchStudyRow, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return repository.ResearchStudyRow{}, err
	}
	defer tx.Rollback(ctx)
	r, err := scanStudy(tx.QueryRow(ctx, `SELECT `+studyColumns+` FROM research_studies WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
		return repository.ResearchStudyRow{}, repository.ErrNotFound
	}
	if err != nil {
		return repository.ResearchStudyRow{}, err
	}
	var execution struct {
		Kind   string `json:"kind"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(executionJSON, &execution); err != nil {
		return repository.ResearchStudyRow{}, err
	}
	allowed := (execution.Kind == "exploratory" &&
		((execution.Status == "completed" && newStatus == "exploratory_complete" && (r.Status == "frozen" || r.Status == "exploratory_complete")) ||
			(execution.Status == "failed" && newStatus == r.Status && (r.Status == "frozen" || r.Status == "exploratory_complete")))) ||
		(execution.Kind == "confirmatory" &&
			((execution.Status == "completed" && newStatus == "holdout_consumed" && r.Status == "exploratory_complete") ||
				(execution.Status == "failed" && newStatus == "exploratory_complete" && r.Status == "exploratory_complete")))
	if !allowed {
		return repository.ResearchStudyRow{}, repository.ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE research_studies SET
		executions=executions || jsonb_build_array($2::jsonb),status=$3,last_error=$4,
		exploratory_completed_at=CASE WHEN $3='exploratory_complete' AND $2::jsonb->>'kind'='exploratory'
			THEN COALESCE(exploratory_completed_at,now()) ELSE exploratory_completed_at END,
		holdout_consumed_at=CASE WHEN $3='holdout_consumed' THEN now() ELSE holdout_consumed_at END,
		updated_at=now()
		WHERE id=$1`, id, executionJSON, newStatus, errMsg)
	if err != nil {
		return repository.ResearchStudyRow{}, err
	}
	r, err = scanStudy(tx.QueryRow(ctx, `SELECT `+studyColumns+` FROM research_studies WHERE id=$1`, id))
	if err != nil {
		return repository.ResearchStudyRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return repository.ResearchStudyRow{}, err
	}
	return r, nil
}

func (s *Store) ActiveReservations(ctx context.Context, assetID, timeframe string) ([]repository.ResearchStudyRow, error) {
	rows, err := s.db.Query(ctx, `SELECT `+studyColumns+` FROM research_studies
		WHERE asset_id=$1 AND timeframe=$2 AND status IN ('frozen','exploratory_complete')
		ORDER BY holdout_start`, assetID, timeframe)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]repository.ResearchStudyRow, 0)
	for rows.Next() {
		r, err := scanStudy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type studyScanner interface{ Scan(...any) error }

func scanStudy(row studyScanner) (repository.ResearchStudyRow, error) {
	var r repository.ResearchStudyRow
	err := row.Scan(&r.ID, &r.Status, &r.Name, &r.Description, &r.Hypothesis, &r.AssetID,
		&r.Timeframe, &r.DatasetFingerprint, &r.DevStart, &r.DevEnd, &r.HoldoutStart,
		&r.HoldoutEnd, &r.Spec, &r.SpecHash, &r.ResearchCounts, &r.Executions, &r.LastError,
		&r.CreatedAt, &r.UpdatedAt, &r.FrozenAt, &r.ExploratoryCompletedAt, &r.HoldoutConsumedAt)
	return r, err
}

func isInvalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}
