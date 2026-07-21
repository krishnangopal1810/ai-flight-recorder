package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

// HostedRepository keeps searchable metadata and references in PostgreSQL and
// immutable payload bytes in an S3-compatible object store.
type HostedRepository struct {
	db      *pgxpool.Pool
	objects *minio.Client
	bucket  string
}

func OpenHosted(ctx context.Context, storage config.StorageConfig) (*HostedRepository, error) {
	if storage.PostgresURL == "" || storage.S3Endpoint == "" || storage.S3Bucket == "" {
		return nil, types.NewError(types.ErrValidation, "hosted storage configuration is incomplete", "set postgres_url, s3_endpoint, and s3_bucket", nil)
	}
	pool, err := pgxpool.New(ctx, storage.PostgresURL)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot configure PostgreSQL", "check the hosted database URL", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, types.NewError(types.ErrStorage, "cannot reach PostgreSQL", "check database health and network policy", err)
	}
	endpoint, secure, err := objectEndpoint(storage.S3Endpoint, storage.S3UseTLS)
	if err != nil {
		pool.Close()
		return nil, err
	}
	objects, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(storage.S3AccessKey, storage.S3SecretKey, ""), Secure: secure, Region: storage.S3Region})
	if err != nil {
		pool.Close()
		return nil, types.NewError(types.ErrStorage, "cannot configure object storage", "check the S3-compatible endpoint", err)
	}
	repository := &HostedRepository{db: pool, objects: objects, bucket: storage.S3Bucket}
	if err := repository.ensureBucket(ctx, storage.S3Region); err != nil {
		pool.Close()
		return nil, err
	}
	if err := repository.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return repository, nil
}

func (r *HostedRepository) Close() error { r.db.Close(); return nil }

func (r *HostedRepository) ensureBucket(ctx context.Context, region string) error {
	exists, err := r.objects.BucketExists(ctx, r.bucket)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot inspect object-storage bucket", "check S3 credentials and endpoint health", err)
	}
	if exists {
		return nil
	}
	if err := r.objects.MakeBucket(ctx, r.bucket, minio.MakeBucketOptions{Region: region}); err != nil {
		return types.NewError(types.ErrStorage, "cannot create object-storage bucket", "pre-create the configured bucket or grant bucket creation", err)
	}
	return nil
}

func (r *HostedRepository) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS capsules (id TEXT PRIMARY KEY, trace_id TEXT NOT NULL, project_id TEXT NOT NULL, service_name TEXT NOT NULL, start_time TIMESTAMPTZ NOT NULL, terminal_status TEXT NOT NULL, manifest JSONB NOT NULL, expires_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS capsules_project_time ON capsules(project_id, start_time DESC)`,
		`CREATE TABLE IF NOT EXISTS payloads (digest TEXT PRIMARY KEY, size BIGINT NOT NULL, ref_count BIGINT NOT NULL CHECK(ref_count >= 0), created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS capsule_payloads (capsule_id TEXT NOT NULL REFERENCES capsules(id) ON DELETE CASCADE, digest TEXT NOT NULL REFERENCES payloads(digest), PRIMARY KEY(capsule_id, digest))`,
		`CREATE TABLE IF NOT EXISTS quarantine_records (id TEXT PRIMARY KEY, reason TEXT NOT NULL, safe_metadata JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, scopes JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL, revoked_at TIMESTAMPTZ)`,
		`CREATE TABLE IF NOT EXISTS jobs (id TEXT PRIMARY KEY, kind TEXT NOT NULL, state TEXT NOT NULL, request JSONB NOT NULL, result JSONB, error JSONB, lease_owner TEXT, lease_expires_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS jobs_claim ON jobs(state, lease_expires_at, created_at)`,
		`CREATE TABLE IF NOT EXISTS job_events (sequence BIGSERIAL PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE, type TEXT NOT NULL, data JSONB, time TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS audit_events (sequence BIGSERIAL PRIMARY KEY, kind TEXT NOT NULL, safe_metadata JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS evaluators (capsule_id TEXT NOT NULL REFERENCES capsules(id) ON DELETE CASCADE, evaluator_id TEXT NOT NULL, version TEXT NOT NULL, definition JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY(capsule_id,evaluator_id,version))`,
		`CREATE TABLE IF NOT EXISTS evaluator_runs (id TEXT PRIMARY KEY, definition_id TEXT NOT NULL, definition_version TEXT NOT NULL, baseline_capsule_id TEXT NOT NULL REFERENCES capsules(id), candidate_capsule_id TEXT NOT NULL REFERENCES capsules(id), outcome TEXT NOT NULL, result JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := r.db.Exec(ctx, statement); err != nil {
			return types.NewError(types.ErrStorage, "cannot migrate PostgreSQL", "restore a backup or correct the migration before accepting traffic", err)
		}
	}
	if _, err := r.db.Exec(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(1,$1) ON CONFLICT(version) DO NOTHING`, time.Now().UTC()); err != nil {
		return types.NewError(types.ErrStorage, "cannot record PostgreSQL schema version", "restore a backup and inspect migration state", err)
	}
	return nil
}

func (r *HostedRepository) PutCapsule(ctx context.Context, capsule types.RunCapsule, payloads map[string][]byte) error {
	manifest, err := json.Marshal(capsule)
	if err != nil {
		return types.NewError(types.ErrValidation, "cannot serialize capsule", "fix non-JSON capsule fields", err)
	}
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot begin capsule transaction", "retry after checking PostgreSQL health", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockPayloadDigests(ctx, tx, payloads); err != nil {
		return err
	}
	var existing []byte
	lookupErr := tx.QueryRow(ctx, `SELECT manifest FROM capsules WHERE id=$1`, capsule.CapsuleID).Scan(&existing)
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
	if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return types.NewError(types.ErrStorage, "cannot inspect existing capsule", "check PostgreSQL health", lookupErr)
	}
	for digest, data := range payloads {
		if err := r.putBlob(ctx, digest, data); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO capsules(id,trace_id,project_id,service_name,start_time,terminal_status,manifest,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, capsule.CapsuleID, capsule.TraceID, capsule.ProjectID, capsule.ServiceName, capsule.StartTime, capsule.TerminalStatus, manifest, capsule.ExpiresAt, time.Now().UTC()); err != nil {
		return types.NewError(types.ErrStorage, "cannot persist capsule manifest", "use a unique immutable capsule or verify PostgreSQL health", err)
	}
	for digest, data := range payloads {
		if _, err := tx.Exec(ctx, `INSERT INTO payloads(digest,size,ref_count,created_at) VALUES($1,$2,1,$3) ON CONFLICT(digest) DO UPDATE SET ref_count=payloads.ref_count+1`, digest, len(data), time.Now().UTC()); err != nil {
			return types.NewError(types.ErrStorage, "cannot persist payload reference", "retry after checking PostgreSQL health", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO capsule_payloads(capsule_id,digest) VALUES($1,$2)`, capsule.CapsuleID, digest); err != nil {
			return types.NewError(types.ErrStorage, "cannot link capsule payload", "retry after checking PostgreSQL health", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return types.NewError(types.ErrStorage, "cannot commit capsule", "retry after checking PostgreSQL health", err)
	}
	return nil
}

func lockPayloadDigests(ctx context.Context, tx pgx.Tx, payloads map[string][]byte) error {
	digests := make([]string, 0, len(payloads))
	for digest := range payloads {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	for _, digest := range digests {
		if err := lockPayloadDigest(ctx, tx, digest); err != nil {
			return err
		}
	}
	return nil
}

func lockPayloadDigest(ctx context.Context, tx pgx.Tx, digest string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 407641923))`, digest); err != nil {
		return types.NewError(types.ErrStorage, "cannot lock payload reference", "retry after checking PostgreSQL coordination", err)
	}
	return nil
}

func (r *HostedRepository) GetCapsule(ctx context.Context, id string) (types.RunCapsule, error) {
	var manifest []byte
	if err := r.db.QueryRow(ctx, `SELECT manifest FROM capsules WHERE id=$1`, id).Scan(&manifest); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return types.RunCapsule{}, types.NewError(types.ErrNotFound, "capsule not found", "check the capsule ID or import its bundle", err)
		}
		return types.RunCapsule{}, types.NewError(types.ErrStorage, "cannot load capsule", "check PostgreSQL health", err)
	}
	var capsule types.RunCapsule
	if err := json.Unmarshal(manifest, &capsule); err != nil {
		return types.RunCapsule{}, types.NewError(types.ErrStorage, "stored capsule is invalid", "restore a verified backup", err)
	}
	return capsule, nil
}

func (r *HostedRepository) ListCapsules(ctx context.Context, project string, limit int) ([]types.CapsuleSummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query, arguments := `SELECT manifest FROM capsules`, []any{}
	if project != "" {
		query, arguments = query+` WHERE project_id=$1`, append(arguments, project)
	}
	arguments = append(arguments, limit)
	query += fmt.Sprintf(` ORDER BY start_time DESC LIMIT $%d`, len(arguments))
	rows, err := r.db.Query(ctx, query, arguments...)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot list capsules", "check PostgreSQL health", err)
	}
	defer rows.Close()
	result := []types.CapsuleSummary{}
	for rows.Next() {
		var manifest []byte
		var capsule types.RunCapsule
		if err := rows.Scan(&manifest); err != nil {
			return nil, types.NewError(types.ErrStorage, "stored capsule is invalid", "restore a verified backup", err)
		}
		if err := json.Unmarshal(manifest, &capsule); err != nil {
			return nil, types.NewError(types.ErrStorage, "stored capsule is invalid", "restore a verified backup", err)
		}
		result = append(result, types.CapsuleSummary{CapsuleID: capsule.CapsuleID, TraceID: capsule.TraceID, ProjectID: capsule.ProjectID, ServiceName: capsule.ServiceName, StartTime: capsule.StartTime, TerminalStatus: capsule.TerminalStatus, Completeness: capsule.Completeness})
	}
	return result, rows.Err()
}

func (r *HostedRepository) LoadPayload(ctx context.Context, digest string) ([]byte, error) {
	object, err := r.objects.GetObject(ctx, r.bucket, objectKey(digest), minio.GetObjectOptions{})
	if err != nil {
		return nil, types.NewError(types.ErrNotFound, "payload not found", "restore the referenced object or import its bundle", err)
	}
	defer object.Close()
	data, err := io.ReadAll(object)
	if err != nil {
		return nil, types.NewError(types.ErrNotFound, "payload not found", "restore the referenced object or import its bundle", err)
	}
	if digestBytes(data) != digest {
		return nil, types.NewError(types.ErrStorage, "stored payload digest mismatch", "quarantine the object store and restore a verified backup", ErrPayloadCorrupt)
	}
	return data, nil
}

func (r *HostedRepository) putBlob(ctx context.Context, digest string, data []byte) error {
	if digestBytes(data) != digest {
		return types.NewError(types.ErrValidation, "payload does not match its digest", "derive the sha256 digest after capture policy", nil)
	}
	_, err := r.objects.PutObject(ctx, r.bucket, objectKey(digest), bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: "application/octet-stream", SendContentMd5: true})
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot persist payload object", "check S3 capacity, credentials, and health", err)
	}
	return nil
}

func (r *HostedRepository) PutQuarantine(ctx context.Context, record QuarantineRecord) error {
	safe, err := json.Marshal(record.Safe)
	if err != nil {
		return types.NewError(types.ErrValidation, "quarantine metadata must be JSON", "store only typed policy-safe metadata", err)
	}
	_, err = r.db.Exec(ctx, `INSERT INTO quarantine_records(id,reason,safe_metadata,created_at) VALUES($1,$2,$3,$4)`, record.ID, record.Reason, safe, record.CreatedAt)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot persist quarantine record", "check PostgreSQL health", err)
	}
	return nil
}

func (r *HostedRepository) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, types.NewError(types.ErrStorage, "cannot start retention transaction", "retain data and check PostgreSQL health", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id,project_id FROM capsules WHERE expires_at IS NOT NULL AND expires_at < $1 FOR UPDATE`, now)
	if err != nil {
		return 0, types.NewError(types.ErrStorage, "cannot evaluate retention", "retain data and check PostgreSQL health", err)
	}
	ids := []string{}
	projectCounts := map[string]int{}
	for rows.Next() {
		var id, project string
		if err := rows.Scan(&id, &project); err != nil {
			rows.Close()
			return 0, types.NewError(types.ErrStorage, "cannot read retention candidates", "retain data and check PostgreSQL health", err)
		}
		ids = append(ids, id)
		projectCounts[project]++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, types.NewError(types.ErrStorage, "cannot enumerate retention candidates", "retain data and check PostgreSQL health", err)
	}
	rows.Close()
	referenceCount := int64(0)
	for _, id := range ids {
		updated, err := tx.Exec(ctx, `UPDATE payloads SET ref_count=ref_count-1 WHERE digest IN (SELECT digest FROM capsule_payloads WHERE capsule_id=$1)`, id)
		if err != nil {
			return 0, types.NewError(types.ErrStorage, "cannot update retention references", "retain data and check PostgreSQL health", err)
		}
		referenceCount += updated.RowsAffected()
		if _, err := tx.Exec(ctx, `DELETE FROM capsules WHERE id=$1`, id); err != nil {
			return 0, types.NewError(types.ErrStorage, "cannot expire capsule", "retain data and check PostgreSQL health", err)
		}
	}
	if len(ids) > 0 {
		audit, marshalErr := json.Marshal(map[string]any{"capsule_count": len(ids), "payload_reference_count": referenceCount, "project_counts": projectCounts})
		if marshalErr != nil {
			return 0, types.NewError(types.ErrStorage, "cannot encode retention audit", "retain data and inspect safe audit metadata", marshalErr)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_events(kind,safe_metadata,created_at) VALUES('retention_delete',$1,$2)`, audit, now); err != nil {
			return 0, types.NewError(types.ErrStorage, "cannot record retention audit", "retain data and check PostgreSQL health", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, types.NewError(types.ErrStorage, "cannot commit retention", "retain data and check PostgreSQL health", err)
	}
	// Phase two deletes only objects whose committed reference count reached zero.
	zeroRows, err := r.db.Query(ctx, `SELECT digest FROM payloads WHERE ref_count=0`)
	if err != nil {
		return len(ids), types.NewError(types.ErrStorage, "cannot evaluate unreferenced payload cleanup", "unreferenced payloads were retained; check PostgreSQL health", err)
	}
	zeros := []string{}
	for zeroRows.Next() {
		var digest string
		if err := zeroRows.Scan(&digest); err != nil {
			zeroRows.Close()
			return len(ids), types.NewError(types.ErrStorage, "cannot read unreferenced payload", "unreferenced payloads were retained; check PostgreSQL health", err)
		}
		zeros = append(zeros, digest)
	}
	if err := zeroRows.Err(); err != nil {
		zeroRows.Close()
		return len(ids), types.NewError(types.ErrStorage, "cannot enumerate unreferenced payloads", "unreferenced payloads were retained; check PostgreSQL health", err)
	}
	zeroRows.Close()
	for _, digest := range zeros {
		cleanup, err := r.db.Begin(ctx)
		if err != nil {
			return len(ids), types.NewError(types.ErrStorage, "cannot begin unreferenced payload cleanup", "the payload was retained; check PostgreSQL health", err)
		}
		if err := lockPayloadDigest(ctx, cleanup, digest); err != nil {
			_ = cleanup.Rollback(ctx)
			return len(ids), err
		}
		var references int64
		if err := cleanup.QueryRow(ctx, `SELECT ref_count FROM payloads WHERE digest=$1 FOR UPDATE`, digest).Scan(&references); err != nil {
			_ = cleanup.Rollback(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return len(ids), types.NewError(types.ErrStorage, "cannot recheck unreferenced payload", "the payload was retained; check PostgreSQL health", err)
		}
		if references != 0 {
			_ = cleanup.Rollback(ctx)
			continue
		}
		if err := r.objects.RemoveObject(ctx, r.bucket, objectKey(digest), minio.RemoveObjectOptions{}); err != nil {
			_ = cleanup.Rollback(ctx)
			return len(ids), types.NewError(types.ErrStorage, "cannot remove unreferenced payload", "the payload was retained; check object storage health and retry retention", err)
		}
		deleted, err := cleanup.Exec(ctx, `DELETE FROM payloads WHERE digest=$1 AND ref_count=0`, digest)
		if err != nil || deleted.RowsAffected() != 1 {
			_ = cleanup.Rollback(ctx)
			return len(ids), types.NewError(types.ErrStorage, "cannot finalize unreferenced payload cleanup", "retry retention after checking reference accounting", err)
		}
		if err := cleanup.Commit(ctx); err != nil {
			return len(ids), types.NewError(types.ErrStorage, "cannot commit unreferenced payload cleanup", "retry retention after checking PostgreSQL health", err)
		}
	}
	return len(ids), nil
}

func (r *HostedRepository) CreateToken(ctx context.Context, token Token) error {
	scopes, _ := json.Marshal(token.Scopes)
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot create API token", "check PostgreSQL health", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO tokens(id,name,token_hash,scopes,created_at) VALUES($1,$2,$3,$4,$5)`, token.ID, token.Name, token.Hash, scopes, token.CreatedAt)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot create API token", "use a unique token name and retry", err)
	}
	safe, _ := json.Marshal(map[string]any{"token_id": token.ID, "name": token.Name, "scopes": token.Scopes})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(kind,safe_metadata,created_at) VALUES($1,$2,$3)`, "token_created", safe, token.CreatedAt); err != nil {
		return types.NewError(types.ErrStorage, "cannot audit API token creation", "check PostgreSQL health", err)
	}
	return tx.Commit(ctx)
}

func (r *HostedRepository) GetTokenByHash(ctx context.Context, hash string) (Token, error) {
	var token Token
	var scopes []byte
	if err := r.db.QueryRow(ctx, `SELECT id,name,token_hash,scopes,created_at,revoked_at FROM tokens WHERE token_hash=$1`, hash).Scan(&token.ID, &token.Name, &token.Hash, &scopes, &token.CreatedAt, &token.RevokedAt); err != nil {
		return Token{}, types.NewError(types.ErrAuthentication, "invalid API token", "create or supply a non-revoked scoped token", err)
	}
	_ = json.Unmarshal(scopes, &token.Scopes)
	return token, nil
}

func (r *HostedRepository) RevokeToken(ctx context.Context, id string) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot revoke API token", "check PostgreSQL health", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC()
	result, err := tx.Exec(ctx, `UPDATE tokens SET revoked_at=$1 WHERE id=$2`, now, id)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot revoke API token", "check PostgreSQL health", err)
	}
	if result.RowsAffected() == 0 {
		return types.NewError(types.ErrNotFound, "API token not found", "check the token ID", nil)
	}
	safe, _ := json.Marshal(map[string]any{"token_id": id})
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events(kind,safe_metadata,created_at) VALUES($1,$2,$3)`, "token_revoked", safe, now); err != nil {
		return types.NewError(types.ErrStorage, "cannot audit API token revocation", "check PostgreSQL health", err)
	}
	return tx.Commit(ctx)
}

func (r *HostedRepository) ListAuditEvents(ctx context.Context, after int64) ([]AuditEvent, error) {
	rows, err := r.db.Query(ctx, `SELECT sequence,kind,safe_metadata,created_at FROM audit_events WHERE sequence>$1 ORDER BY sequence LIMIT 500`, after)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot list audit events", "check PostgreSQL health", err)
	}
	defer rows.Close()
	result := []AuditEvent{}
	for rows.Next() {
		var event AuditEvent
		var safe []byte
		if err := rows.Scan(&event.Sequence, &event.Kind, &safe, &event.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(safe, &event.Safe)
		result = append(result, event)
	}
	return result, rows.Err()
}

func (r *HostedRepository) PutJob(ctx context.Context, job types.Job) error {
	errorJSON, _ := json.Marshal(job.Error)
	_, err := r.db.Exec(ctx, `INSERT INTO jobs(id,kind,state,request,result,error,lease_owner,lease_expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, job.ID, job.Kind, job.State, job.Request, nullJSON(job.Result), errorJSON, nullable(job.LeaseOwner), job.LeaseExpiresAt, job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot create job", "retry after checking PostgreSQL health", err)
	}
	return nil
}

func (r *HostedRepository) GetJob(ctx context.Context, id string) (types.Job, error) {
	return scanHostedJob(r.db.QueryRow(ctx, `SELECT id,kind,state,request,result,error,lease_owner,lease_expires_at,created_at,updated_at FROM jobs WHERE id=$1`, id))
}

func (r *HostedRepository) ClaimJob(ctx context.Context, owner string, until time.Time) (*types.Job, error) {
	row := r.db.QueryRow(ctx, `WITH candidate AS (
		SELECT id FROM jobs WHERE state='queued' OR (state='running' AND lease_expires_at < now()) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE jobs SET state='running',lease_owner=$1,lease_expires_at=$2,updated_at=now() WHERE id=(SELECT id FROM candidate)
	RETURNING id,kind,state,request,result,error,lease_owner,lease_expires_at,created_at,updated_at`, owner, until)
	job, err := scanHostedJob(row)
	if err != nil {
		if typed, ok := err.(*types.FlightError); ok && errors.Is(typed.Cause, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *HostedRepository) RenewJobLease(ctx context.Context, id, owner string, until time.Time) error {
	result, err := r.db.Exec(ctx, `UPDATE jobs SET lease_expires_at=$1,updated_at=now() WHERE id=$2 AND state='running' AND lease_owner=$3`, until, id, owner)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot renew job lease", "check PostgreSQL health", err)
	}
	if result.RowsAffected() != 1 {
		return types.NewError(types.ErrConflict, "job lease is no longer owned by this worker", "stop execution and inspect worker health", nil)
	}
	return nil
}

func (r *HostedRepository) UpdateJob(ctx context.Context, job types.Job) error {
	errorJSON, _ := json.Marshal(job.Error)
	_, err := r.db.Exec(ctx, `UPDATE jobs SET state=$1,result=$2,error=$3,lease_owner=$4,lease_expires_at=$5,updated_at=$6 WHERE id=$7`, job.State, nullJSON(job.Result), errorJSON, nullable(job.LeaseOwner), job.LeaseExpiresAt, job.UpdatedAt, job.ID)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot update job", "retry after checking PostgreSQL health", err)
	}
	return nil
}

func (r *HostedRepository) AppendJobEvent(ctx context.Context, event types.JobEvent) error {
	_, err := r.db.Exec(ctx, `INSERT INTO job_events(job_id,type,data,time) VALUES($1,$2,$3,$4)`, event.JobID, event.Type, nullJSON(event.Data), event.Time)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot append job event", "retry after checking PostgreSQL health", err)
	}
	return nil
}

func (r *HostedRepository) ListJobEvents(ctx context.Context, jobID string, after int64) ([]types.JobEvent, error) {
	rows, err := r.db.Query(ctx, `SELECT sequence,job_id,type,data,time FROM job_events WHERE job_id=$1 AND sequence>$2 ORDER BY sequence LIMIT 500`, jobID, after)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot list job events", "retry after checking PostgreSQL health", err)
	}
	defer rows.Close()
	result := []types.JobEvent{}
	for rows.Next() {
		var event types.JobEvent
		if err := rows.Scan(&event.Sequence, &event.JobID, &event.Type, &event.Data, &event.Time); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}

func (r *HostedRepository) PutEvaluator(ctx context.Context, capsuleID string, definition types.EvaluatorDefinition) error {
	data, err := json.Marshal(definition)
	if err != nil {
		return types.NewError(types.ErrValidation, "cannot serialize evaluator", "fix evaluator JSON fields", err)
	}
	_, err = r.db.Exec(ctx, `INSERT INTO evaluators(capsule_id,evaluator_id,version,definition,created_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(capsule_id,evaluator_id,version) DO UPDATE SET definition=excluded.definition`, capsuleID, definition.ID, definition.Version, data, time.Now().UTC())
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot attach evaluator", "check the capsule ID and PostgreSQL health", err)
	}
	return nil
}

func (r *HostedRepository) GetEvaluator(ctx context.Context, id, version string) (types.EvaluatorDefinition, error) {
	query, arguments := `SELECT definition FROM evaluators WHERE evaluator_id=$1`, []any{id}
	if version != "" {
		query, arguments = query+` AND version=$2`, append(arguments, version)
	}
	query += ` ORDER BY created_at DESC LIMIT 1`
	var data []byte
	if err := r.db.QueryRow(ctx, query, arguments...).Scan(&data); err != nil {
		return types.EvaluatorDefinition{}, types.NewError(types.ErrNotFound, "evaluator not found", "attach or import the requested evaluator version", err)
	}
	var definition types.EvaluatorDefinition
	if err := json.Unmarshal(data, &definition); err != nil {
		return definition, types.NewError(types.ErrStorage, "stored evaluator is invalid", "restore a verified backup", err)
	}
	return definition, nil
}

func (r *HostedRepository) PutEvaluatorRun(ctx context.Context, run types.EvaluatorRun) error {
	data, err := json.Marshal(run.Output)
	if err != nil {
		return types.NewError(types.ErrValidation, "cannot serialize evaluator output", "return JSON evaluator evidence", err)
	}
	_, err = r.db.Exec(ctx, `INSERT INTO evaluator_runs(id,definition_id,definition_version,baseline_capsule_id,candidate_capsule_id,outcome,result,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, run.ID, run.DefinitionID, run.DefinitionVersion, run.BaselineCapsuleID, run.CandidateCapsuleID, run.Outcome, data, run.CreatedAt)
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot persist evaluator result", "check capsule references and PostgreSQL health", err)
	}
	return nil
}

func (r *HostedRepository) ListEvaluatorRuns(ctx context.Context, capsuleID string) ([]types.EvaluatorRun, error) {
	rows, err := r.db.Query(ctx, `SELECT id,definition_id,definition_version,baseline_capsule_id,candidate_capsule_id,outcome,result,created_at FROM evaluator_runs WHERE baseline_capsule_id=$1 ORDER BY created_at DESC`, capsuleID)
	if err != nil {
		return nil, types.NewError(types.ErrStorage, "cannot list evaluator results", "check PostgreSQL health", err)
	}
	defer rows.Close()
	result := []types.EvaluatorRun{}
	for rows.Next() {
		var run types.EvaluatorRun
		var output []byte
		if err := rows.Scan(&run.ID, &run.DefinitionID, &run.DefinitionVersion, &run.BaselineCapsuleID, &run.CandidateCapsuleID, &run.Outcome, &output, &run.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(output, &run.Output); err != nil {
			return nil, types.NewError(types.ErrStorage, "stored evaluator result is invalid", "restore a verified backup", err)
		}
		result = append(result, run)
	}
	return result, rows.Err()
}

type hostedRow interface{ Scan(...any) error }

func scanHostedJob(row hostedRow) (types.Job, error) {
	var job types.Job
	var result, errorJSON []byte
	var leaseOwner *string
	if err := row.Scan(&job.ID, &job.Kind, &job.State, &job.Request, &result, &errorJSON, &leaseOwner, &job.LeaseExpiresAt, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return types.Job{}, types.NewError(types.ErrNotFound, "job not found", "check the job ID", err)
	}
	job.Result = result
	if leaseOwner != nil {
		job.LeaseOwner = *leaseOwner
	}
	if len(errorJSON) > 0 && string(errorJSON) != "null" {
		_ = json.Unmarshal(errorJSON, &job.Error)
	}
	return job, nil
}

func objectEndpoint(value string, defaultSecure bool) (string, bool, error) {
	if !strings.Contains(value, "://") {
		return strings.TrimRight(value, "/"), defaultSecure, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false, types.NewError(types.ErrValidation, "invalid S3 endpoint", "use host:port or an http/https URL", err)
	}
	return parsed.Host, parsed.Scheme == "https", nil
}

func objectKey(digest string) string { return "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:") }
func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func nullJSON(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
