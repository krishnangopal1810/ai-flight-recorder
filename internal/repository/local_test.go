package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestLocalRepositoryRoundTripAndJobLease(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	exerciseRepositoryContract(t, repo, "local")
	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	crashed := types.Job{ID: "crashed", Kind: "replay", State: types.JobRunning, Request: []byte(`{}`), LeaseOwner: "dead-worker", LeaseExpiresAt: &expired, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	if err := repo.PutJob(ctx, crashed); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repo.ClaimJob(ctx, "recovery-worker", now.Add(time.Minute))
	if err != nil || reclaimed == nil || reclaimed.ID != "crashed" || reclaimed.LeaseOwner != "recovery-worker" {
		t.Fatalf("reclaim: %#v %v", reclaimed, err)
	}
}

func TestLocalCASRejectsCorruptExistingObject(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	payload := []byte("evidence")
	digest := "sha256:ee8250fb76e094b34b471f13a73dbbe51d1ae142e9df59d7c0d31ec20f0a0a8e"
	if err := repo.writeBlob(digest, payload); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repo.blobFilename(digest), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	var flightErr *types.FlightError
	if err := repo.writeBlob(digest, payload); !errors.As(err, &flightErr) || flightErr.Code != types.ErrStorage {
		t.Fatalf("corrupt existing object was trusted: %v", err)
	}
}

func TestAFRSEC003LocalStorageUsesRestrictivePermissions(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath, blobPath := filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs")
	if err := os.Mkdir(blobPath, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := OpenLocal(ctx, databasePath, blobPath)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	payload := []byte("restricted evidence")
	digest := digestBytes(payload)
	if err := repo.writeBlob(digest, payload); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		databasePath:              0o600,
		blobPath:                  0o700,
		repo.blobFilename(digest): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("permissions for %s = %04o, want %04o", path, got, want)
		}
	}
}

func TestRetentionKeepsSharedBlobUntilFinalReferenceExpires(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	payload := []byte("evidence")
	digest := "sha256:ee8250fb76e094b34b471f13a73dbbe51d1ae142e9df59d7c0d31ec20f0a0a8e"
	past, future := time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour)
	for id, expiry := range map[string]*time.Time{"afr_old": &past, "afr_live": &future} {
		capsule := types.RunCapsule{SchemaVersion: types.SchemaVersion, CapsuleID: id, TraceID: id, ProjectID: "project", ServiceName: "agent", StartTime: time.Unix(1, 0).UTC(), ExpiresAt: expiry, TerminalStatus: "ok"}
		if err := repo.PutCapsule(ctx, capsule, map[string][]byte{digest: payload}); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := repo.DeleteExpired(ctx, time.Now().UTC())
	if err != nil || deleted != 1 {
		t.Fatalf("first retention: %d %v", deleted, err)
	}
	if _, err := repo.LoadPayload(ctx, digest); err != nil {
		t.Fatalf("shared blob was deleted: %v", err)
	}
	deleted, err = repo.DeleteExpired(ctx, future.Add(time.Second))
	if err != nil || deleted != 1 {
		t.Fatalf("second retention: %d %v", deleted, err)
	}
	_, err = repo.LoadPayload(ctx, digest)
	var flightErr *types.FlightError
	if !errors.As(err, &flightErr) || flightErr.Code != types.ErrNotFound {
		t.Fatalf("unreferenced blob remains: %v", err)
	}
}

func TestAFRSEC004UnsafeReferenceAccountingRollsBackWithoutAudit(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	payload := []byte("accounting must remain conservative")
	digest := digestBytes(payload)
	past := time.Now().UTC().Add(-time.Hour)
	capsule := types.RunCapsule{SchemaVersion: types.SchemaVersion, CapsuleID: "afr_accounting", TraceID: "accounting", ProjectID: "retention", ServiceName: "agent", StartTime: past, EndTime: past, ExpiresAt: &past, TerminalStatus: "ok"}
	if err := repo.PutCapsule(ctx, capsule, map[string][]byte{digest: payload}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.ExecContext(ctx, `UPDATE payloads SET ref_count=0 WHERE digest=?`, digest); err != nil {
		t.Fatal(err)
	}
	auditBefore, _ := repo.ListAuditEvents(ctx, 0)
	deleted, err := repo.DeleteExpired(ctx, time.Now().UTC())
	if err == nil || deleted != 0 {
		t.Fatalf("unsafe accounting deletion = %d, %v", deleted, err)
	}
	if _, err := repo.GetCapsule(ctx, capsule.CapsuleID); err != nil {
		t.Fatalf("capsule changed despite failed accounting: %v", err)
	}
	if loaded, err := repo.LoadPayload(ctx, digest); err != nil || string(loaded) != string(payload) {
		t.Fatalf("payload changed despite failed accounting: %q, %v", loaded, err)
	}
	auditAfter, _ := repo.ListAuditEvents(ctx, 0)
	if len(auditAfter) != len(auditBefore) {
		t.Fatalf("failed accounting was audited as deletion: %d -> %d", len(auditBefore), len(auditAfter))
	}
}

func TestAFRSEC004CleanupFailureRetainsBlobAndReturnsOperationalError(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	payload := []byte("retryable retention payload")
	digest := digestBytes(payload)
	past := time.Now().UTC().Add(-time.Hour)
	capsule := types.RunCapsule{SchemaVersion: types.SchemaVersion, CapsuleID: "afr_cleanup_failure", TraceID: "cleanup", ProjectID: "retention", ServiceName: "agent", StartTime: past, EndTime: past, ExpiresAt: &past, TerminalStatus: "ok"}
	if err := repo.PutCapsule(ctx, capsule, map[string][]byte{digest: payload}); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected blob outage")
	repo.removeBlob = func(string) error { return injected }
	deleted, err := repo.DeleteExpired(ctx, time.Now().UTC())
	if !errors.Is(err, injected) || deleted != 1 {
		t.Fatalf("cleanup failure = %d, %v", deleted, err)
	}
	if loaded, err := repo.LoadPayload(ctx, digest); err != nil || string(loaded) != string(payload) {
		t.Fatalf("failed cleanup removed payload: %q, %v", loaded, err)
	}
	var references int
	if err := repo.db.QueryRowContext(ctx, `SELECT ref_count FROM payloads WHERE digest=?`, digest).Scan(&references); err != nil || references != 0 {
		t.Fatalf("retryable payload metadata = %d, %v", references, err)
	}
	repo.removeBlob = os.Remove
	if deleted, err := repo.DeleteExpired(ctx, time.Now().UTC()); err != nil || deleted != 0 {
		t.Fatalf("cleanup retry = %d, %v", deleted, err)
	}
	if _, err := repo.LoadPayload(ctx, digest); !isNotFound(err) {
		t.Fatalf("cleanup retry left payload: %v", err)
	}
}
