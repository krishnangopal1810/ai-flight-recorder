package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
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
	capsule = capsuleWithUnknownContractFields(t, capsule)
	for range 2 {
		if err := repo.PutCapsule(ctx, capsule, map[string][]byte{digest: payload}); err != nil {
			t.Fatalf("idempotent capsule write: %v", err)
		}
	}
	loaded, err := repo.GetCapsule(ctx, capsule.CapsuleID)
	if err != nil || loaded.TraceID != capsule.TraceID {
		t.Fatalf("capsule round trip: %#v %v", loaded, err)
	}
	loadedManifest, err := json.Marshal(loaded)
	if err != nil || !bytes.Contains(loadedManifest, []byte(`"future_contract":"kept"`)) || !bytes.Contains(loadedManifest, []byte(`"future_provenance":"kept"`)) {
		t.Fatalf("compatible unknown fields did not survive repository round trip: %s %v", loadedManifest, err)
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
	exerciseRetentionContract(t, repo, prefix)
	exerciseConcurrentRetentionContract(t, repo, prefix)
}

func exerciseRetentionContract(t *testing.T, repo Repository, prefix string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	sharedPayload := []byte(prefix + " shared retention evidence")
	uniquePayload := []byte(prefix + " unique expired evidence")
	sharedDigest, uniqueDigest := digestBytes(sharedPayload), digestBytes(uniquePayload)
	put := func(id, project string, expiry *time.Time, payloads map[string][]byte) {
		t.Helper()
		capsule := types.RunCapsule{SchemaVersion: types.SchemaVersion, CapsuleID: id, TraceID: id, ProjectID: project, ServiceName: "retention-agent", StartTime: now.Add(-2 * time.Hour), EndTime: now.Add(-time.Hour), ExpiresAt: expiry, TerminalStatus: "ok", Completeness: map[string]types.Completeness{}}
		if err := repo.PutCapsule(ctx, capsule, payloads); err != nil {
			t.Fatalf("put retention capsule %s: %v", id, err)
		}
	}
	put(prefix+"-expired-shared", prefix+"-short", &past, map[string][]byte{sharedDigest: sharedPayload})
	put(prefix+"-expired-unique", prefix+"-short", &past, map[string][]byte{uniqueDigest: uniquePayload})
	put(prefix+"-live-shared", prefix+"-long", &future, map[string][]byte{sharedDigest: sharedPayload})
	auditBefore, err := repo.ListAuditEvents(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := repo.DeleteExpired(ctx, now)
	if err != nil || deleted != 2 {
		t.Fatalf("retention deletion = %d, %v", deleted, err)
	}
	for _, id := range []string{prefix + "-expired-shared", prefix + "-expired-unique"} {
		if _, err := repo.GetCapsule(ctx, id); !isNotFound(err) {
			t.Fatalf("expired capsule %s remains: %v", id, err)
		}
	}
	if _, err := repo.GetCapsule(ctx, prefix+"-live-shared"); err != nil {
		t.Fatalf("live capsule was deleted: %v", err)
	}
	if loaded, err := repo.LoadPayload(ctx, sharedDigest); err != nil || !bytes.Equal(loaded, sharedPayload) {
		t.Fatalf("shared payload was deleted: %q, %v", loaded, err)
	}
	if _, err := repo.LoadPayload(ctx, uniqueDigest); !isNotFound(err) {
		t.Fatalf("unreferenced payload remains: %v", err)
	}
	auditAfter, err := repo.ListAuditEvents(ctx, 0)
	if err != nil || len(auditAfter) != len(auditBefore)+1 {
		t.Fatalf("retention audit count = %d -> %d, %v", len(auditBefore), len(auditAfter), err)
	}
	event := auditAfter[len(auditAfter)-1]
	encoded, _ := json.Marshal(event.Safe)
	if event.Kind != "retention_delete" || event.Safe["capsule_count"] != float64(2) || !strings.Contains(string(encoded), prefix+"-short") {
		t.Fatalf("retention audit = %#v", event)
	}
	for _, forbidden := range []string{string(sharedPayload), string(uniquePayload), sharedDigest, uniqueDigest, prefix + "-expired-shared", prefix + "-expired-unique"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("retention audit retained deleted evidence %q: %s", forbidden, encoded)
		}
	}
	if deleted, err := repo.DeleteExpired(ctx, now); err != nil || deleted != 0 {
		t.Fatalf("empty retention pass = %d, %v", deleted, err)
	}
	auditUnchanged, _ := repo.ListAuditEvents(ctx, 0)
	if len(auditUnchanged) != len(auditAfter) {
		t.Fatalf("empty retention pass created an audit event: %d -> %d", len(auditAfter), len(auditUnchanged))
	}
	if deleted, err := repo.DeleteExpired(ctx, future.Add(time.Second)); err != nil || deleted != 1 {
		t.Fatalf("final retention = %d, %v", deleted, err)
	}
	if _, err := repo.LoadPayload(ctx, sharedDigest); !isNotFound(err) {
		t.Fatalf("final unreferenced payload remains: %v", err)
	}
}

func isNotFound(err error) bool {
	var flightErr *types.FlightError
	return errors.As(err, &flightErr) && flightErr.Code == types.ErrNotFound
}

func exerciseConcurrentRetentionContract(t *testing.T, repo Repository, prefix string) {
	t.Helper()
	ctx := context.Background()
	for index := range 8 {
		now := time.Now().UTC()
		past, future := now.Add(-time.Hour), now.Add(time.Hour)
		payload := []byte(prefix + fmt.Sprintf(" concurrent retention evidence %d", index))
		digest := digestBytes(payload)
		expired := types.RunCapsule{SchemaVersion: types.SchemaVersion, CapsuleID: fmt.Sprintf("%s-race-expired-%d", prefix, index), TraceID: fmt.Sprintf("%s-race-expired-%d", prefix, index), ProjectID: prefix + "-race", ServiceName: "retention-agent", StartTime: past, EndTime: past, ExpiresAt: &past, TerminalStatus: "ok"}
		live := types.RunCapsule{SchemaVersion: types.SchemaVersion, CapsuleID: fmt.Sprintf("%s-race-live-%d", prefix, index), TraceID: fmt.Sprintf("%s-race-live-%d", prefix, index), ProjectID: prefix + "-race", ServiceName: "retention-agent", StartTime: now, EndTime: now, ExpiresAt: &future, TerminalStatus: "ok"}
		if err := repo.PutCapsule(ctx, expired, map[string][]byte{digest: payload}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errorsCh := make(chan error, 2)
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			<-start
			_, err := repo.DeleteExpired(ctx, now)
			errorsCh <- err
		}()
		go func() {
			defer wait.Done()
			<-start
			errorsCh <- repo.PutCapsule(ctx, live, map[string][]byte{digest: payload})
		}()
		close(start)
		wait.Wait()
		close(errorsCh)
		for err := range errorsCh {
			if err != nil {
				t.Fatalf("concurrent retention operation: %v", err)
			}
		}
		if _, err := repo.GetCapsule(ctx, live.CapsuleID); err != nil {
			t.Fatalf("concurrent live capsule is missing: %v", err)
		}
		if loaded, err := repo.LoadPayload(ctx, digest); err != nil || !bytes.Equal(loaded, payload) {
			t.Fatalf("concurrent live payload = %q, %v", loaded, err)
		}
		if _, err := repo.DeleteExpired(ctx, future.Add(time.Second)); err != nil {
			t.Fatalf("concurrent retention cleanup: %v", err)
		}
	}
}

func capsuleWithUnknownContractFields(t *testing.T, capsule types.RunCapsule) types.RunCapsule {
	t.Helper()
	manifest, err := json.Marshal(capsule)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(manifest, &document); err != nil {
		t.Fatal(err)
	}
	document["future_contract"] = json.RawMessage(`"kept"`)
	var provenance map[string]json.RawMessage
	if err := json.Unmarshal(document["provenance"], &provenance); err != nil {
		t.Fatal(err)
	}
	provenance["future_provenance"] = json.RawMessage(`"kept"`)
	document["provenance"], err = json.Marshal(provenance)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifest, &capsule); err != nil {
		t.Fatal(err)
	}
	return capsule
}
