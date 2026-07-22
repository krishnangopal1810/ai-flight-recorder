package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func (r *LocalRepository) StageLog(ctx context.Context, staged StagedLog) error {
	data, err := json.Marshal(staged)
	if err != nil {
		return types.NewError(types.ErrValidation, "cannot serialize staged log", "emit JSON-compatible OTLP log values", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot begin log staging transaction", "retry after checking database health", err)
	}
	defer func() { _ = tx.Rollback() }()
	var present int
	lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM capsules WHERE project_id=? AND trace_id=? LIMIT 1`, staged.ProjectID, staged.TraceID).Scan(&present)
	if lookupErr == nil {
		return ErrTraceLogsSealed
	}
	if !errors.Is(lookupErr, sql.ErrNoRows) {
		return types.NewError(types.ErrStorage, "cannot inspect trace log state", "check database health", lookupErr)
	}
	sealErr := tx.QueryRowContext(ctx, `SELECT 1 FROM trace_log_seals WHERE project_id=? AND trace_id=?`, staged.ProjectID, staged.TraceID).Scan(&present)
	if sealErr == nil {
		return ErrTraceLogsSealed
	}
	if !errors.Is(sealErr, sql.ErrNoRows) {
		return types.NewError(types.ErrStorage, "cannot inspect trace log seal", "check database health", sealErr)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO staged_trace_logs(project_id,trace_id,log_id,data,expires_at) VALUES(?,?,?,?,?) ON CONFLICT(project_id,trace_id,log_id) DO NOTHING`, staged.ProjectID, staged.TraceID, staged.Record.ID, data, staged.ExpiresAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return types.NewError(types.ErrStorage, "cannot stage trace log", "retry after checking database health", err)
	}
	if err := tx.Commit(); err != nil {
		return types.NewError(types.ErrStorage, "cannot commit trace log", "retry after checking database health", err)
	}
	return nil
}

func (r *LocalRepository) SealLogs(ctx context.Context, projectID, traceID string, expiresAt time.Time) ([]StagedLog, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot begin trace log seal", "retry after checking database health", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO trace_log_seals(project_id,trace_id,expires_at) VALUES(?,?,?) ON CONFLICT(project_id,trace_id) DO UPDATE SET expires_at=excluded.expires_at`, projectID, traceID, expiresAt.UTC().Add(time.Hour).Format(time.RFC3339Nano)); err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot seal trace logs", "retry after checking database health", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT data FROM staged_trace_logs WHERE project_id=? AND trace_id=? ORDER BY log_id`, projectID, traceID)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot load staged trace logs", "retry after checking database health", err)
	}
	defer rows.Close()
	logs := []StagedLog{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, types.NewError(types.ErrStorage, "cannot read staged trace log", "check database health", err)
		}
		var staged StagedLog
		if err := json.Unmarshal(data, &staged); err != nil {
			return nil, types.NewError(types.ErrStorage, "staged trace log is invalid", "remove the damaged staging record", err)
		}
		logs = append(logs, staged)
	}
	if err := rows.Err(); err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot iterate staged trace logs", "check database health", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot commit trace log seal", "retry after checking database health", err)
	}
	return logs, nil
}

func (r *LocalRepository) ReleaseLogs(ctx context.Context, projectID, traceID string) error {
	var present int
	if err := r.db.QueryRowContext(ctx, `SELECT 1 FROM capsules WHERE project_id=? AND trace_id=? LIMIT 1`, projectID, traceID).Scan(&present); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return types.NewError(types.ErrStorage, "cannot inspect trace before releasing logs", "check database health", err)
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM trace_log_seals WHERE project_id=? AND trace_id=?`, projectID, traceID)
	return err
}

func (r *LocalRepository) CompleteLogs(ctx context.Context, projectID, traceID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM staged_trace_logs WHERE project_id=? AND trace_id=?`, projectID, traceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM trace_log_seals WHERE project_id=? AND trace_id=?`, projectID, traceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *LocalRepository) DeleteExpiredLogs(ctx context.Context, now time.Time) (int, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM staged_trace_logs WHERE expires_at<=?`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, types.NewError(types.ErrStorage, "cannot expire orphaned trace logs", "check database health", err)
	}
	count, _ := result.RowsAffected()
	_, _ = r.db.ExecContext(ctx, `DELETE FROM trace_log_seals WHERE expires_at<=?`, now.UTC().Format(time.RFC3339Nano))
	return int(count), nil
}
