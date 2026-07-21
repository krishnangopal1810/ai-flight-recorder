//go:build integration

package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestHostedRepositoryContract(t *testing.T) {
	storage := config.HostedContractEnvironment("flight-contract-" + uuid.NewString())
	if storage.PostgresURL == "" || storage.S3Endpoint == "" {
		t.Skip("hosted contract requires FLIGHT_TEST_POSTGRES_URL and FLIGHT_TEST_S3_ENDPOINT")
	}
	prefix := "hosted-" + uuid.NewString()
	repo, err := OpenHosted(context.Background(), storage)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	exerciseRepositoryContract(t, repo, prefix)

	// A crashed worker's expired lease is reclaimable on both backends.
	now := time.Now().UTC()
	claimed, err := repo.GetJob(context.Background(), prefix+"-job")
	if err != nil {
		t.Fatal(err)
	}
	// Keep the lease safely behind the database clock. Using the exact client
	// timestamp made this contract depend on TIMESTAMPTZ rounding at the
	// immediate ClaimJob boundary rather than on lease-recovery behavior.
	expired := now.Add(-time.Minute)
	claimed.LeaseExpiresAt = &expired
	claimed.UpdatedAt = now
	if err := repo.UpdateJob(context.Background(), claimed); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repo.ClaimJob(context.Background(), prefix+"-recovery", now.Add(time.Minute))
	if err != nil || reclaimed == nil || reclaimed.ID != claimed.ID {
		t.Fatalf("expired lease recovery: %#v %v", reclaimed, err)
	}
	reclaimed.State, reclaimed.LeaseOwner, reclaimed.LeaseExpiresAt, reclaimed.UpdatedAt = types.JobCompleted, "", nil, time.Now().UTC()
	if err := repo.UpdateJob(context.Background(), *reclaimed); err != nil {
		t.Fatalf("complete hosted contract job: %v", err)
	}

	payload := []byte("hosted accounting must remain conservative")
	digest := digestBytes(payload)
	past := now.Add(-time.Hour)
	capsule := types.RunCapsule{SchemaVersion: types.SchemaVersion, CapsuleID: prefix + "-unsafe-accounting", TraceID: prefix + "-unsafe-accounting", ProjectID: prefix + "-retention", ServiceName: "agent", StartTime: past, EndTime: past, ExpiresAt: &past, TerminalStatus: "ok"}
	if err := repo.PutCapsule(context.Background(), capsule, map[string][]byte{digest: payload}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(context.Background(), `UPDATE payloads SET ref_count=0 WHERE digest=$1`, digest); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = repo.db.Exec(context.Background(), `UPDATE payloads SET ref_count=1 WHERE digest=$1`, digest)
	}()
	deleted, err := repo.DeleteExpired(context.Background(), now)
	if err == nil || deleted != 0 {
		t.Fatalf("unsafe hosted accounting deletion = %d, %v", deleted, err)
	}
	var flightErr *types.FlightError
	if !errors.As(err, &flightErr) || flightErr.Code != types.ErrStorage {
		t.Fatalf("unsafe hosted accounting error = %v", err)
	}
	if _, err := repo.GetCapsule(context.Background(), capsule.CapsuleID); err != nil {
		t.Fatalf("hosted capsule changed despite failed accounting: %v", err)
	}
	if _, err := repo.db.Exec(context.Background(), `UPDATE payloads SET ref_count=1 WHERE digest=$1`, digest); err != nil {
		t.Fatal(err)
	}
	if deleted, err := repo.DeleteExpired(context.Background(), now); err != nil || deleted != 1 {
		t.Fatalf("hosted accounting fixture cleanup = %d, %v", deleted, err)
	}
}
