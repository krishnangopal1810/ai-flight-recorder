//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
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
	claimed.LeaseExpiresAt = &now
	claimed.UpdatedAt = now
	if err := repo.UpdateJob(context.Background(), claimed); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := repo.ClaimJob(context.Background(), prefix+"-recovery", now.Add(time.Minute))
	if err != nil || reclaimed == nil || reclaimed.ID != claimed.ID {
		t.Fatalf("expired lease recovery: %#v %v", reclaimed, err)
	}
}
