package repository

import (
	"context"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

// exerciseRepositoryContract is intentionally backend-neutral. Every storage
// implementation must pass the same durability, idempotency, lookup, auth, and
// lease behavior rather than growing backend-specific semantics.
func exerciseRepositoryContract(t *testing.T, repo Repository, prefix string) {
	t.Helper()
	ctx := context.Background()
	payload := []byte("evidence")
	digest := "sha256:ee8250fb76e094b34b471f13a73dbbe51d1ae142e9df59d7c0d31ec20f0a0a8e"
	now := time.Now().UTC()
	capsule := types.RunCapsule{
		SchemaVersion:  types.SchemaVersion,
		CapsuleID:      prefix + "-capsule",
		TraceID:        prefix + "-trace",
		ProjectID:      prefix + "-project",
		ServiceName:    "agent",
		StartTime:      time.Unix(1, 0).UTC(),
		TerminalStatus: "ok",
		Completeness:   map[string]types.Completeness{},
	}
	for range 2 {
		if err := repo.PutCapsule(ctx, capsule, map[string][]byte{digest: payload}); err != nil {
			t.Fatalf("idempotent capsule write: %v", err)
		}
	}
	loaded, err := repo.GetCapsule(ctx, capsule.CapsuleID)
	if err != nil || loaded.TraceID != capsule.TraceID {
		t.Fatalf("capsule round trip: %#v %v", loaded, err)
	}
	loadedPayload, err := repo.LoadPayload(ctx, digest)
	if err != nil || string(loadedPayload) != "evidence" {
		t.Fatalf("payload round trip: %q %v", loadedPayload, err)
	}
	listed, err := repo.ListCapsules(ctx, capsule.ProjectID, 10)
	if err != nil || len(listed) != 1 || listed[0].CapsuleID != capsule.CapsuleID {
		t.Fatalf("capsule list: %#v %v", listed, err)
	}
	token := Token{ID: prefix + "-token", Name: "contract", Hash: prefix + "-hash", Scopes: []string{"read"}, CreatedAt: now}
	if err := repo.CreateToken(ctx, token); err != nil {
		t.Fatalf("create token: %v", err)
	}
	loadedToken, err := repo.GetTokenByHash(ctx, token.Hash)
	if err != nil || loadedToken.ID != token.ID || len(loadedToken.Scopes) != 1 {
		t.Fatalf("token round trip: %#v %v", loadedToken, err)
	}
	if err := repo.RevokeToken(ctx, token.ID); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	audit, err := repo.ListAuditEvents(ctx, 0)
	if err != nil || len(audit) < 2 || audit[len(audit)-2].Kind != "token_created" || audit[len(audit)-1].Kind != "token_revoked" {
		t.Fatalf("token audit trail: %#v %v", audit, err)
	}
	job := types.Job{ID: prefix + "-job", Kind: "replay", State: types.JobQueued, Request: []byte(`{}`), CreatedAt: now, UpdatedAt: now}
	if err := repo.PutJob(ctx, job); err != nil {
		t.Fatalf("put job: %v", err)
	}
	claimed, err := repo.ClaimJob(ctx, prefix+"-worker", now.Add(time.Minute))
	if err != nil || claimed == nil || claimed.ID != job.ID || claimed.State != types.JobRunning {
		t.Fatalf("claim job: %#v %v", claimed, err)
	}
	if err := repo.RenewJobLease(ctx, job.ID, prefix+"-worker", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("renew job: %v", err)
	}
	second, err := repo.ClaimJob(ctx, prefix+"-other", now.Add(time.Minute))
	if err != nil || second != nil {
		t.Fatalf("duplicate claim: %#v %v", second, err)
	}
}
