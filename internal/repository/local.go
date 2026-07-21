package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
	_ "modernc.org/sqlite"
)

type LocalRepository struct {
	db          *sql.DB
	blobPath    string
	retentionMu sync.Mutex
	removeBlob  func(string) error
}

func OpenLocal(ctx context.Context, databasePath, blobPath string) (*LocalRepository, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot create database directory", "check the configured data directory permissions", err)
	}
	if err := os.MkdirAll(blobPath, 0o700); err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot create blob directory", "check the configured blob path permissions", err)
	}
	if err := prepareLocalStorage(databasePath, blobPath); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot open SQLite database", "check the database path", err)
	}
	db.SetMaxOpenConns(1)
	repository := &LocalRepository{db: db, blobPath: blobPath, removeBlob: os.Remove}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return nil, types.NewError(types.ErrStorage, "cannot configure SQLite database", "verify the SQLite runtime and data directory", err)
	}
	if err := repository.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := secureLocalStorage(databasePath, blobPath); err != nil {
		_ = db.Close()
		return nil, err
	}
	return repository, nil
}

func (r *LocalRepository) Close() error { return r.db.Close() }

func (r *LocalRepository) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS capsules (id TEXT PRIMARY KEY, trace_id TEXT NOT NULL, project_id TEXT NOT NULL, service_name TEXT NOT NULL, start_time TEXT NOT NULL, terminal_status TEXT NOT NULL, manifest BLOB NOT NULL, expires_at TEXT, created_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS capsules_project_time ON capsules(project_id, start_time DESC)`,
		`CREATE TABLE IF NOT EXISTS payloads (digest TEXT PRIMARY KEY, size INTEGER NOT NULL, ref_count INTEGER NOT NULL CHECK(ref_count >= 0), created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS capsule_payloads (capsule_id TEXT NOT NULL REFERENCES capsules(id) ON DELETE CASCADE, digest TEXT NOT NULL REFERENCES payloads(digest), PRIMARY KEY(capsule_id, digest))`,
		`CREATE TABLE IF NOT EXISTS quarantine_records (id TEXT PRIMARY KEY, reason TEXT NOT NULL, safe_metadata BLOB NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, scopes BLOB NOT NULL, created_at TEXT NOT NULL, revoked_at TEXT)`,
		`CREATE TABLE IF NOT EXISTS jobs (id TEXT PRIMARY KEY, kind TEXT NOT NULL, state TEXT NOT NULL, request BLOB NOT NULL, result BLOB, error BLOB, lease_owner TEXT, lease_expires_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS jobs_claim ON jobs(state, lease_expires_at, created_at)`,
		`CREATE TABLE IF NOT EXISTS job_events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, type TEXT NOT NULL, data BLOB, time TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS audit_events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, safe_metadata BLOB NOT NULL, created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS evaluators (capsule_id TEXT NOT NULL REFERENCES capsules(id) ON DELETE CASCADE, evaluator_id TEXT NOT NULL, version TEXT NOT NULL, definition BLOB NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(capsule_id,evaluator_id,version))`,
		`CREATE TABLE IF NOT EXISTS evaluator_runs (id TEXT PRIMARY KEY, definition_id TEXT NOT NULL, definition_version TEXT NOT NULL, baseline_capsule_id TEXT NOT NULL REFERENCES capsules(id), candidate_capsule_id TEXT NOT NULL REFERENCES capsules(id), outcome TEXT NOT NULL, result BLOB NOT NULL, created_at TEXT NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return types.NewError(types.ErrStorage, "cannot migrate SQLite database", "back up the data directory and inspect the migration error", err)
		}
	}
	if _, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(1,?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return types.NewError(types.ErrStorage, "cannot record SQLite schema version", "restore a backup and inspect migration state", err)
	}
	return nil
}

func (r *LocalRepository) PutCapsule(ctx context.Context, capsule types.RunCapsule, payloads map[string][]byte) error {
	r.retentionMu.Lock()
	defer r.retentionMu.Unlock()
	manifest, err := json.Marshal(capsule)
	if err != nil {
		return types.NewError(types.ErrValidation, "cannot serialize capsule", "fix non-JSON capsule fields", err)
	}
	var existing []byte
	lookupErr := r.db.QueryRowContext(ctx, `SELECT manifest FROM capsules WHERE id=?`, capsule.CapsuleID).Scan(&existing)
	if lookupErr == nil {
		if !jsonDocumentsEqual(existing, manifest) {
			return types.NewError(types.ErrConflict, "capsule identity collides with different manifest bytes", "quarantine the store and inspect digest integrity", nil)
		}
		for digest, expected := range payloads {
			loaded, loadErr := r.LoadPayload(ctx, digest)
			if loadErr != nil || !bytes.Equal(loaded, expected) {
				return types.NewError(types.ErrStorage, "existing capsule payload is unavailable or corrupt", "restore a verified bundle", loadErr)
			}
		}
		return nil
	}
	if !errors.Is(lookupErr, sql.ErrNoRows) {
		return types.NewError(types.ErrStorage, "cannot inspect existing capsule", "check database health", lookupErr)
	}
	for digest, data := range payloads {
		if err := r.writeBlob(digest, data); err != nil {
			return err
		}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot begin capsule transaction", "retry after checking database health", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO capsules(id, trace_id, project_id, service_name, start_time, terminal_status, manifest, expires_at, created_at) VALUES(?,?,?,?,?,?,?,?,?)`, capsule.CapsuleID, capsule.TraceID, capsule.ProjectID, capsule.ServiceName, capsule.StartTime.UTC().Format(time.RFC3339Nano), capsule.TerminalStatus, manifest, formatTime(capsule.ExpiresAt), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot persist capsule manifest", "use a unique immutable capsule or verify database health", err)
	}
	for digest, data := range payloads {
		_, err = tx.ExecContext(ctx, `INSERT INTO payloads(digest,size,ref_count,created_at) VALUES(?,?,1,?) ON CONFLICT(digest) DO UPDATE SET ref_count=ref_count+1`, digest, len(data), time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return types.NewError(types.ErrStorage, "cannot persist payload reference", "retry after checking database health", err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO capsule_payloads(capsule_id,digest) VALUES(?,?)`, capsule.CapsuleID, digest); err != nil {
			return types.NewError(types.ErrStorage, "cannot link capsule payload", "retry after checking database health", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return types.NewError(types.ErrStorage, "cannot commit capsule", "retry after checking database health", err)
	}
	return nil
}

func (r *LocalRepository) GetCapsule(ctx context.Context, id string) (types.RunCapsule, error) {
	var manifest []byte
	if err := r.db.QueryRowContext(ctx, `SELECT manifest FROM capsules WHERE id=?`, id).Scan(&manifest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return types.RunCapsule{}, types.NewError(types.ErrNotFound, "capsule not found", "check the capsule ID or import its bundle", err)
		}
		return types.RunCapsule{}, types.NewError(types.ErrStorage, "cannot load capsule", "check database health", err)
	}
	var capsule types.RunCapsule
	if err := json.Unmarshal(manifest, &capsule); err != nil {
		return types.RunCapsule{}, types.NewError(types.ErrStorage, "stored capsule is invalid", "restore a verified backup", err)
	}
	return capsule, nil
}

func (r *LocalRepository) ListCapsules(ctx context.Context, project string, limit int) ([]types.CapsuleSummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT manifest FROM capsules`
	args := []any{}
	if project != "" {
		query += ` WHERE project_id=?`
		args = append(args, project)
	}
	query += ` ORDER BY start_time DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot list capsules", "check database health", err)
	}
	defer rows.Close()
	result := []types.CapsuleSummary{}
	for rows.Next() {
		var manifest []byte
		if err := rows.Scan(&manifest); err != nil {
			return nil, types.NewError(types.ErrStorage, "cannot read capsule row", "check database health", err)
		}
		var capsule types.RunCapsule
		if err := json.Unmarshal(manifest, &capsule); err != nil {
			return nil, types.NewError(types.ErrStorage, "stored capsule is invalid", "restore a verified backup", err)
		}
		result = append(result, types.CapsuleSummary{CapsuleID: capsule.CapsuleID, TraceID: capsule.TraceID, ProjectID: capsule.ProjectID, ServiceName: capsule.ServiceName, StartTime: capsule.StartTime, TerminalStatus: capsule.TerminalStatus, Completeness: capsule.Completeness})
	}
	return result, rows.Err()
}

func (r *LocalRepository) LoadPayload(_ context.Context, digest string) ([]byte, error) {
	data, err := os.ReadFile(r.blobFilename(digest))
	if err != nil {
		return nil, types.NewError(types.ErrNotFound, "payload not found", "restore the referenced payload or import its bundle", err)
	}
	if digestBytes(data) != digest {
		return nil, types.NewError(types.ErrStorage, "stored payload digest mismatch", "quarantine the local store and restore a verified bundle", nil)
	}
	return data, nil
}

func (r *LocalRepository) writeBlob(digest string, data []byte) error {
	if !strings.HasPrefix(digest, "sha256:") || digestBytes(data) != digest {
		return types.NewError(types.ErrValidation, "payload does not match its digest", "derive the sha256 digest after capture policy", nil)
	}
	filename := r.blobFilename(digest)
	if existing, err := os.ReadFile(filename); err == nil {
		if digestBytes(existing) != digest {
			return types.NewError(types.ErrStorage, "existing payload digest mismatch", "quarantine the local store and restore a verified bundle", nil)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return types.NewError(types.ErrStorage, "cannot inspect payload", "check blob directory permissions", err)
	}
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return types.NewError(types.ErrStorage, "cannot create payload directory", "check blob directory permissions", err)
	}
	if err := os.Chmod(filepath.Dir(filename), 0o700); err != nil {
		return types.NewError(types.ErrStorage, "cannot restrict payload directory permissions", "grant the service ownership of the configured blob path", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(filename), ".flight-blob-*")
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot stage payload", "check blob directory permissions", err)
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return types.NewError(types.ErrStorage, "cannot write payload", "check disk capacity and permissions", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return types.NewError(types.ErrStorage, "cannot sync payload", "check storage health", err)
	}
	if err := temp.Close(); err != nil {
		return types.NewError(types.ErrStorage, "cannot close payload", "check storage health", err)
	}
	if err := os.Rename(tempName, filename); err != nil {
		return types.NewError(types.ErrStorage, "cannot commit payload", "check blob directory permissions", err)
	}
	return nil
}

func (r *LocalRepository) blobFilename(digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	if len(hex) < 2 {
		return filepath.Join(r.blobPath, "invalid")
	}
	return filepath.Join(r.blobPath, "sha256", hex[:2], hex[2:])
}

func secureLocalStorage(databasePath, blobPath string) error {
	if err := os.Chmod(blobPath, 0o700); err != nil {
		return types.NewError(types.ErrStorage, "cannot restrict blob directory permissions", "grant the service ownership of the configured blob path", err)
	}
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return types.NewError(types.ErrStorage, "cannot restrict database file permissions", "grant the service ownership of the configured SQLite files", err)
		}
	}
	return nil
}

func prepareLocalStorage(databasePath, blobPath string) error {
	if err := os.Chmod(blobPath, 0o700); err != nil {
		return types.NewError(types.ErrStorage, "cannot restrict blob directory permissions", "grant the service ownership of the configured blob path", err)
	}
	database, err := os.OpenFile(databasePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot create restricted database file", "grant the service ownership of the configured database path", err)
	}
	closeErr := database.Close()
	if chmodErr := os.Chmod(databasePath, 0o600); chmodErr != nil {
		return types.NewError(types.ErrStorage, "cannot restrict database file permissions", "grant the service ownership of the configured SQLite file", chmodErr)
	}
	if closeErr != nil {
		return types.NewError(types.ErrStorage, "cannot close database file during permission setup", "check the configured database filesystem", closeErr)
	}
	return nil
}

func (r *LocalRepository) PutQuarantine(ctx context.Context, record QuarantineRecord) error {
	safe, err := json.Marshal(record.Safe)
	if err != nil {
		return types.NewError(types.ErrValidation, "quarantine metadata must be JSON", "store only typed policy-safe metadata", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO quarantine_records(id,reason,safe_metadata,created_at) VALUES(?,?,?,?)`, record.ID, record.Reason, safe, record.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot persist quarantine record", "check database health", err)
	}
	return nil
}

func (r *LocalRepository) CreateToken(ctx context.Context, token Token) error {
	scopes, _ := json.Marshal(token.Scopes)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot create API token", "check database health", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO tokens(id,name,token_hash,scopes,created_at) VALUES(?,?,?,?,?)`, token.ID, token.Name, token.Hash, scopes, token.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot create API token", "use a unique token name and retry", err)
	}
	safe, _ := json.Marshal(map[string]any{"token_id": token.ID, "name": token.Name, "scopes": token.Scopes})
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(kind,safe_metadata,created_at) VALUES(?,?,?)`, "token_created", safe, token.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return types.NewError(types.ErrStorage, "cannot audit API token creation", "check database health", err)
	}
	return tx.Commit()
}

func (r *LocalRepository) GetTokenByHash(ctx context.Context, hash string) (Token, error) {
	var token Token
	var scopes []byte
	var created string
	var revoked sql.NullString
	if err := r.db.QueryRowContext(ctx, `SELECT id,name,token_hash,scopes,created_at,revoked_at FROM tokens WHERE token_hash=?`, hash).Scan(&token.ID, &token.Name, &token.Hash, &scopes, &created, &revoked); err != nil {
		return Token{}, types.NewError(types.ErrAuthentication, "invalid API token", "create or supply a non-revoked scoped token", err)
	}
	_ = json.Unmarshal(scopes, &token.Scopes)
	token.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	if revoked.Valid {
		parsed, _ := time.Parse(time.RFC3339Nano, revoked.String)
		token.RevokedAt = &parsed
	}
	return token, nil
}

func (r *LocalRepository) RevokeToken(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot revoke API token", "check database health", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE tokens SET revoked_at=? WHERE id=?`, now.Format(time.RFC3339Nano), id)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot revoke API token", "check database health", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return types.NewError(types.ErrNotFound, "API token not found", "check the token ID", nil)
	}
	safe, _ := json.Marshal(map[string]any{"token_id": id})
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(kind,safe_metadata,created_at) VALUES(?,?,?)`, "token_revoked", safe, now.Format(time.RFC3339Nano)); err != nil {
		return types.NewError(types.ErrStorage, "cannot audit API token revocation", "check database health", err)
	}
	return tx.Commit()
}

func (r *LocalRepository) ListAuditEvents(ctx context.Context, after int64) ([]AuditEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT sequence,kind,safe_metadata,created_at FROM audit_events WHERE sequence>? ORDER BY sequence LIMIT 500`, after)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot list audit events", "check database health", err)
	}
	defer rows.Close()
	result := []AuditEvent{}
	for rows.Next() {
		var event AuditEvent
		var safe []byte
		var created string
		if err := rows.Scan(&event.Sequence, &event.Kind, &safe, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(safe, &event.Safe)
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, event)
	}
	return result, rows.Err()
}

func (r *LocalRepository) PutJob(ctx context.Context, job types.Job) error {
	errorJSON, _ := json.Marshal(job.Error)
	_, err := r.db.ExecContext(ctx, `INSERT INTO jobs(id,kind,state,request,result,error,lease_owner,lease_expires_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, job.ID, job.Kind, job.State, []byte(job.Request), []byte(job.Result), errorJSON, nullable(job.LeaseOwner), formatTime(job.LeaseExpiresAt), job.CreatedAt.UTC().Format(time.RFC3339Nano), job.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot create job", "retry after checking database health", err)
	}
	return nil
}

func (r *LocalRepository) GetJob(ctx context.Context, id string) (types.Job, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,kind,state,request,result,error,lease_owner,lease_expires_at,created_at,updated_at FROM jobs WHERE id=?`, id)
	return scanJob(row)
}

func (r *LocalRepository) ClaimJob(ctx context.Context, owner string, until time.Time) (*types.Job, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	row := tx.QueryRowContext(ctx, `SELECT id,kind,state,request,result,error,lease_owner,lease_expires_at,created_at,updated_at FROM jobs WHERE state='queued' OR (state='running' AND lease_expires_at < ?) ORDER BY created_at LIMIT 1`, time.Now().UTC().Format(time.RFC3339Nano))
	job, err := scanJob(row)
	if err != nil {
		if typed, ok := err.(*types.FlightError); ok && errors.Is(typed.Cause, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	job.State, job.LeaseOwner, job.LeaseExpiresAt, job.UpdatedAt = types.JobRunning, owner, &until, time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?,lease_owner=?,lease_expires_at=?,updated_at=? WHERE id=? AND (state='queued' OR (state='running' AND lease_expires_at < ?))`, job.State, owner, until.UTC().Format(time.RFC3339Nano), job.UpdatedAt.Format(time.RFC3339Nano), job.ID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *LocalRepository) RenewJobLease(ctx context.Context, id, owner string, until time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE jobs SET lease_expires_at=?,updated_at=? WHERE id=? AND state='running' AND lease_owner=?`, until.UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), id, owner)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot renew job lease", "check database health", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return types.NewError(types.ErrConflict, "job lease is no longer owned by this worker", "stop execution and inspect worker health", nil)
	}
	return nil
}

func (r *LocalRepository) UpdateJob(ctx context.Context, job types.Job) error {
	errorJSON, _ := json.Marshal(job.Error)
	_, err := r.db.ExecContext(ctx, `UPDATE jobs SET state=?,result=?,error=?,lease_owner=?,lease_expires_at=?,updated_at=? WHERE id=?`, job.State, []byte(job.Result), errorJSON, nullable(job.LeaseOwner), formatTime(job.LeaseExpiresAt), job.UpdatedAt.UTC().Format(time.RFC3339Nano), job.ID)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot update job", "retry after checking database health", err)
	}
	return nil
}

func (r *LocalRepository) AppendJobEvent(ctx context.Context, event types.JobEvent) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO job_events(job_id,type,data,time) VALUES(?,?,?,?)`, event.JobID, event.Type, []byte(event.Data), event.Time.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot append job event", "retry after checking database health", err)
	}
	return nil
}

func (r *LocalRepository) ListJobEvents(ctx context.Context, jobID string, after int64) ([]types.JobEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT sequence,job_id,type,data,time FROM job_events WHERE job_id=? AND sequence>? ORDER BY sequence`, jobID, after)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot load job events", "check database health", err)
	}
	defer rows.Close()
	result := []types.JobEvent{}
	for rows.Next() {
		var event types.JobEvent
		var data []byte
		var at string
		if err := rows.Scan(&event.Sequence, &event.JobID, &event.Type, &data, &at); err != nil {
			return nil, err
		}
		event.Data = data
		event.Time, _ = time.Parse(time.RFC3339Nano, at)
		result = append(result, event)
	}
	return result, rows.Err()
}

func (r *LocalRepository) PutEvaluator(ctx context.Context, capsuleID string, definition types.EvaluatorDefinition) error {
	data, err := json.Marshal(definition)
	if err != nil {
		return types.NewError(types.ErrValidation, "cannot serialize evaluator", "fix evaluator JSON fields", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO evaluators(capsule_id,evaluator_id,version,definition,created_at) VALUES(?,?,?,?,?) ON CONFLICT(capsule_id,evaluator_id,version) DO UPDATE SET definition=excluded.definition`, capsuleID, definition.ID, definition.Version, data, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot attach evaluator", "check the capsule ID and database health", err)
	}
	return nil
}

func (r *LocalRepository) GetEvaluator(ctx context.Context, id, version string) (types.EvaluatorDefinition, error) {
	query, arguments := `SELECT definition FROM evaluators WHERE evaluator_id=?`, []any{id}
	if version != "" {
		query, arguments = query+` AND version=?`, append(arguments, version)
	}
	query += ` ORDER BY created_at DESC LIMIT 1`
	var data []byte
	if err := r.db.QueryRowContext(ctx, query, arguments...).Scan(&data); err != nil {
		return types.EvaluatorDefinition{}, types.NewError(types.ErrNotFound, "evaluator not found", "attach or import the requested evaluator version", err)
	}
	var definition types.EvaluatorDefinition
	if err := json.Unmarshal(data, &definition); err != nil {
		return definition, types.NewError(types.ErrStorage, "stored evaluator is invalid", "restore a verified backup", err)
	}
	return definition, nil
}

func (r *LocalRepository) PutEvaluatorRun(ctx context.Context, run types.EvaluatorRun) error {
	data, err := json.Marshal(run.Output)
	if err != nil {
		return types.NewError(types.ErrValidation, "cannot serialize evaluator output", "return JSON evaluator evidence", err)
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO evaluator_runs(id,definition_id,definition_version,baseline_capsule_id,candidate_capsule_id,outcome,result,created_at) VALUES(?,?,?,?,?,?,?,?)`, run.ID, run.DefinitionID, run.DefinitionVersion, run.BaselineCapsuleID, run.CandidateCapsuleID, run.Outcome, data, run.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot persist evaluator result", "check capsule references and database health", err)
	}
	return nil
}

func (r *LocalRepository) ListEvaluatorRuns(ctx context.Context, capsuleID string) ([]types.EvaluatorRun, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,definition_id,definition_version,baseline_capsule_id,candidate_capsule_id,outcome,result,created_at FROM evaluator_runs WHERE baseline_capsule_id=? ORDER BY created_at DESC`, capsuleID)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot list evaluator results", "check database health", err)
	}
	defer rows.Close()
	result := []types.EvaluatorRun{}
	for rows.Next() {
		var run types.EvaluatorRun
		var output []byte
		var created string
		if err := rows.Scan(&run.ID, &run.DefinitionID, &run.DefinitionVersion, &run.BaselineCapsuleID, &run.CandidateCapsuleID, &run.Outcome, &output, &created); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(output, &run.Output); err != nil {
			return nil, types.NewError(types.ErrStorage, "stored evaluator result is invalid", "restore a verified backup", err)
		}
		run.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, run)
	}
	return result, rows.Err()
}

type scanner interface{ Scan(...any) error }

func scanJob(row scanner) (types.Job, error) {
	var job types.Job
	var state string
	var request, result, errorJSON []byte
	var owner, lease sql.NullString
	var created, updated string
	if err := row.Scan(&job.ID, &job.Kind, &state, &request, &result, &errorJSON, &owner, &lease, &created, &updated); err != nil {
		return types.Job{}, types.NewError(types.ErrNotFound, "job not found", "check the job ID", err)
	}
	job.State, job.Request, job.Result = types.JobState(state), request, result
	if len(errorJSON) > 0 && string(errorJSON) != "null" {
		_ = json.Unmarshal(errorJSON, &job.Error)
	}
	if owner.Valid {
		job.LeaseOwner = owner.String
	}
	if lease.Valid {
		parsed, _ := time.Parse(time.RFC3339Nano, lease.String)
		job.LeaseExpiresAt = &parsed
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return job, nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func formatTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (r *LocalRepository) String() string { return fmt.Sprintf("sqlite:%s", r.blobPath) }
