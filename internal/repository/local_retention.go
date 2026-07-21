package repository

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func (r *LocalRepository) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	r.retentionMu.Lock()
	defer r.retentionMu.Unlock()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, types.NewError(types.ErrStorage, "cannot start retention transaction", "retain data and check database health", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id,project_id FROM capsules WHERE expires_at IS NOT NULL AND expires_at < ?`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, types.NewError(types.ErrStorage, "cannot evaluate retention", "retain data and check database health", err)
	}
	ids := []string{}
	projectCounts := map[string]int{}
	for rows.Next() {
		var id, project string
		if err := rows.Scan(&id, &project); err != nil {
			_ = rows.Close()
			return 0, types.NewError(types.ErrStorage, "cannot read retention candidates", "retain data and check database health", err)
		}
		ids = append(ids, id)
		projectCounts[project]++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, types.NewError(types.ErrStorage, "cannot enumerate retention candidates", "retain data and check database health", err)
	}
	_ = rows.Close()
	referenceCount := int64(0)
	for _, id := range ids {
		result, err := tx.ExecContext(ctx, `UPDATE payloads SET ref_count=ref_count-1 WHERE digest IN (SELECT digest FROM capsule_payloads WHERE capsule_id=?)`, id)
		if err != nil {
			return 0, types.NewError(types.ErrStorage, "cannot update retention references", "retain data and check database health", err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return 0, types.NewError(types.ErrStorage, "cannot verify retention reference accounting", "retain data and check database health", err)
		}
		referenceCount += updated
		if _, err := tx.ExecContext(ctx, `DELETE FROM capsules WHERE id=?`, id); err != nil {
			return 0, types.NewError(types.ErrStorage, "cannot expire capsule", "retain data and check database health", err)
		}
	}
	if len(ids) > 0 {
		audit, marshalErr := json.Marshal(map[string]any{"capsule_count": len(ids), "payload_reference_count": referenceCount, "project_counts": projectCounts})
		if marshalErr != nil {
			return 0, types.NewError(types.ErrStorage, "cannot encode retention audit", "retain data and inspect safe audit metadata", marshalErr)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(kind,safe_metadata,created_at) VALUES(?,?,?)`, "retention_delete", audit, now.UTC().Format(time.RFC3339Nano)); err != nil {
			return 0, types.NewError(types.ErrStorage, "cannot record retention audit", "retain data and check database health", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, types.NewError(types.ErrStorage, "cannot commit retention", "retain data and check database health", err)
	}
	zeroRows, queryErr := r.db.QueryContext(ctx, `SELECT digest FROM payloads WHERE ref_count=0`)
	if queryErr != nil {
		return len(ids), types.NewError(types.ErrStorage, "cannot evaluate unreferenced payload cleanup", "unreferenced payloads were retained; check database health", queryErr)
	}
	zeros := []string{}
	for zeroRows.Next() {
		var digest string
		if err := zeroRows.Scan(&digest); err != nil {
			_ = zeroRows.Close()
			return len(ids), types.NewError(types.ErrStorage, "cannot read unreferenced payload", "unreferenced payloads were retained; check database health", err)
		}
		zeros = append(zeros, digest)
	}
	if err := zeroRows.Err(); err != nil {
		_ = zeroRows.Close()
		return len(ids), types.NewError(types.ErrStorage, "cannot enumerate unreferenced payloads", "unreferenced payloads were retained; check database health", err)
	}
	_ = zeroRows.Close()
	for _, digest := range zeros {
		removeErr := r.removeBlob(r.blobFilename(digest))
		if removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			return len(ids), types.NewError(types.ErrStorage, "cannot remove unreferenced payload", "the payload was retained; check blob storage health and retry retention", removeErr)
		}
		result, err := r.db.ExecContext(ctx, `DELETE FROM payloads WHERE digest=? AND ref_count=0`, digest)
		if err != nil {
			return len(ids), types.NewError(types.ErrStorage, "cannot finalize unreferenced payload cleanup", "retry retention after checking database health", err)
		}
		if deleted, err := result.RowsAffected(); err != nil || deleted != 1 {
			return len(ids), types.NewError(types.ErrStorage, "unreferenced payload changed during cleanup", "retain metadata and inspect reference accounting", err)
		}
	}
	return len(ids), nil
}
