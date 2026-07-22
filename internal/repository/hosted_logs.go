package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func (r *HostedRepository) StageLog(ctx context.Context, staged StagedLog) error {
	data, err := json.Marshal(staged)
	if err != nil {
		return types.NewError(types.ErrValidation, "cannot serialize staged log", "emit JSON-compatible OTLP log values", err)
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot begin log staging transaction", "retry after checking PostgreSQL health", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,hashtextextended($2,0)))`, staged.ProjectID, staged.TraceID); err != nil {
		return types.NewError(types.ErrStorage, "cannot lock trace log staging", "retry after checking PostgreSQL health", err)
	}
	var present int
	lookupErr := tx.QueryRow(ctx, `SELECT 1 FROM capsules WHERE project_id=$1 AND trace_id=$2 LIMIT 1`, staged.ProjectID, staged.TraceID).Scan(&present)
	if lookupErr == nil {
		return ErrTraceLogsSealed
	}
	if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return types.NewError(types.ErrStorage, "cannot inspect trace log state", "check PostgreSQL health", lookupErr)
	}
	sealErr := tx.QueryRow(ctx, `SELECT 1 FROM trace_log_seals WHERE project_id=$1 AND trace_id=$2`, staged.ProjectID, staged.TraceID).Scan(&present)
	if sealErr == nil {
		return ErrTraceLogsSealed
	}
	if !errors.Is(sealErr, pgx.ErrNoRows) {
		return types.NewError(types.ErrStorage, "cannot inspect trace log seal", "check PostgreSQL health", sealErr)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO staged_trace_logs(project_id,trace_id,log_id,data,expires_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(project_id,trace_id,log_id) DO NOTHING`, staged.ProjectID, staged.TraceID, staged.Record.ID, data, staged.ExpiresAt); err != nil {
		return types.NewError(types.ErrStorage, "cannot stage trace log", "retry after checking PostgreSQL health", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return types.NewError(types.ErrStorage, "cannot commit trace log", "retry after checking PostgreSQL health", err)
	}
	return nil
}

func (r *HostedRepository) SealLogs(ctx context.Context, projectID, traceID string, expiresAt time.Time) ([]StagedLog, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot begin trace log seal", "retry after checking PostgreSQL health", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,hashtextextended($2,0)))`, projectID, traceID); err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot lock trace log seal", "retry after checking PostgreSQL health", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO trace_log_seals(project_id,trace_id,expires_at) VALUES($1,$2,$3) ON CONFLICT(project_id,trace_id) DO UPDATE SET expires_at=EXCLUDED.expires_at`, projectID, traceID, expiresAt.Add(time.Hour)); err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot seal trace logs", "retry after checking PostgreSQL health", err)
	}
	rows, err := tx.Query(ctx, `SELECT data FROM staged_trace_logs WHERE project_id=$1 AND trace_id=$2 ORDER BY log_id`, projectID, traceID)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot load staged trace logs", "retry after checking PostgreSQL health", err)
	}
	defer rows.Close()
	logs := []StagedLog{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, types.NewError(types.ErrStorage, "cannot read staged trace log", "check PostgreSQL health", err)
		}
		var staged StagedLog
		if err := json.Unmarshal(data, &staged); err != nil {
			return nil, types.NewError(types.ErrStorage, "staged trace log is invalid", "remove the damaged staging record", err)
		}
		logs = append(logs, staged)
	}
	if err := rows.Err(); err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot iterate staged trace logs", "check PostgreSQL health", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot commit trace log seal", "retry after checking PostgreSQL health", err)
	}
	return logs, nil
}

func (r *HostedRepository) ReleaseLogs(ctx context.Context, projectID, traceID string) error {
	var present int
	if err := r.db.QueryRow(ctx, `SELECT 1 FROM capsules WHERE project_id=$1 AND trace_id=$2 LIMIT 1`, projectID, traceID).Scan(&present); err == nil {
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return types.NewError(types.ErrStorage, "cannot inspect trace before releasing logs", "check PostgreSQL health", err)
	}
	_, err := r.db.Exec(ctx, `DELETE FROM trace_log_seals WHERE project_id=$1 AND trace_id=$2`, projectID, traceID)
	return err
}

func (r *HostedRepository) CompleteLogs(ctx context.Context, projectID, traceID string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM staged_trace_logs WHERE project_id=$1 AND trace_id=$2`, projectID, traceID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM trace_log_seals WHERE project_id=$1 AND trace_id=$2`, projectID, traceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *HostedRepository) DeleteExpiredLogs(ctx context.Context, now time.Time) (int, error) {
	command, err := r.db.Exec(ctx, `DELETE FROM staged_trace_logs WHERE expires_at<=$1`, now)
	if err != nil {
		return 0, types.NewError(types.ErrStorage, "cannot expire orphaned trace logs", "check PostgreSQL health", err)
	}
	_, _ = r.db.Exec(ctx, `DELETE FROM trace_log_seals WHERE expires_at<=$1`, now)
	return int(command.RowsAffected()), nil
}
