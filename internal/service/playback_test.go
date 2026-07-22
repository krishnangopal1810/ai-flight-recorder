package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRPLY001PlaybackKeepsVerifiedEvidenceInspectableWithDamagedPayloads(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	blobRoot := filepath.Join(directory, "blobs")
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), blobRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	available := []byte(`{"prompt":"policy"}`)
	missing := []byte(`{"answer":"missing"}`)
	corrupt := []byte(`{"artifact":"corrupt"}`)
	availableDigest, missingDigest, corruptDigest := DigestBytes(available), DigestBytes(missing), DigestBytes(corrupt)
	omitted := &types.PayloadRef{Size: 128, State: types.PayloadStateOmitted, Reason: types.OmissionPolicyExclusion}
	capsule := types.RunCapsule{
		SchemaVersion:  types.SchemaVersion,
		TraceID:        "playback-damaged",
		ProjectID:      "incident",
		ServiceName:    "policy-agent",
		StartTime:      time.Unix(1, 0).UTC(),
		EndTime:        time.Unix(2, 0).UTC(),
		TerminalStatus: "error",
		Completeness:   map[string]types.Completeness{"playback": {State: types.CompletenessIncomplete}},
		Provenance:     types.Provenance{},
		Privacy:        types.PrivacySummary{Mode: "strict"},
		Operations: []types.Operation{{
			SpanID: "root", Kind: "agent", Name: "run", StartTime: time.Unix(1, 0).UTC(), EndTime: time.Unix(2, 0).UTC(), Status: "error",
			Input:  &types.PayloadRef{Digest: availableDigest, MediaType: "application/json", Size: 999, State: types.PayloadStateCaptured},
			Output: &types.PayloadRef{Digest: missingDigest, MediaType: "application/json", Size: int64(len(missing)), State: types.PayloadStateCaptured},
		}},
		Artifacts: []types.Artifact{
			{ID: "corrupt-artifact", Type: "transcript", LogicalName: "transcript", State: types.PayloadStateCaptured, Payload: &types.PayloadRef{Digest: corruptDigest, MediaType: "application/json", Size: int64(len(corrupt)), State: types.PayloadStateCaptured}},
			{ID: "omitted-artifact", Type: "screenshot", LogicalName: "screen", State: types.PayloadStateOmitted, Payload: omitted},
		},
		Boundaries:  []types.Boundary{{ID: "api", OperationSpanID: "root", LogicalKey: "api.policy", Class: "api", Response: &types.PayloadRef{Digest: availableDigest, MediaType: "application/json", Size: int64(len(available)), State: types.PayloadStateCaptured}, SideEffect: "read_only", ReplayPolicy: "substitute", MatchingRule: types.MatchingRule{Mode: "exact"}}},
		Evaluations: []types.Evaluation{{ID: "policy", Version: "1", Evidence: &types.PayloadRef{Digest: corruptDigest, MediaType: "application/json", Size: int64(len(corrupt)), State: types.PayloadStateCaptured}}},
	}
	payloads := map[string][]byte{availableDigest: available, missingDigest: missing, corruptDigest: corrupt}
	if err := FinalizeCapsule(&capsule, payloads); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutCapsule(ctx, capsule, payloads); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(localBlobFilename(blobRoot, missingDigest)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localBlobFilename(blobRoot, corruptDigest), []byte("damaged"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewCapsuleService(repo, mustPolicy(t))
	if _, err := service.Get(ctx, capsule.CapsuleID, true); err == nil {
		t.Fatal("strict capsule verification accepted damaged payloads")
	}
	playback, err := service.Playback(ctx, capsule.CapsuleID)
	if err != nil {
		t.Fatal(err)
	}
	if playback.Executed || playback.ApplicationProcessesStarted != 0 || playback.OutboundRequests != 0 || !playback.ManifestVerified || playback.Capsule.CapsuleID != capsule.CapsuleID {
		t.Fatalf("playback safety projection = %#v", playback)
	}
	if playback.Completeness.Playback.State != types.CompletenessIncomplete || playback.Completeness.Diff.State != types.CompletenessIncomplete || playback.Completeness.Replay.State != types.CompletenessBlocked || !slices.ContainsFunc(playback.Completeness.Playback.Missing, func(item types.MissingItem) bool { return item.Code == "payload_missing" }) || !slices.ContainsFunc(playback.Completeness.Playback.Missing, func(item types.MissingItem) bool { return item.Code == "payload_corrupt" }) {
		t.Fatalf("dynamic playback completeness = %#v", playback.Completeness)
	}
	report, err := service.Completeness(ctx, capsule.CapsuleID)
	if err != nil || report.Playback.State != types.CompletenessIncomplete || !slices.Equal(report.Playback.Missing, playback.Completeness.Playback.Missing) {
		t.Fatalf("completeness endpoint projection = %#v, %v", report, err)
	}
	byDigest := map[string]types.PlaybackPayload{}
	for _, payload := range playback.Payloads {
		byDigest[payload.Digest] = payload
	}
	if got := byDigest[availableDigest]; got.Availability != types.PlaybackAvailable || got.Size != int64(len(available)) || got.Preview != string(available) || got.PreviewEncoding != "json" || !slices.Equal(got.References, []string{"boundaries[0].response", "operations[0].input"}) {
		t.Fatalf("available payload = %#v", got)
	}
	if got := byDigest[missingDigest]; got.Availability != types.PlaybackMissing || !slices.Equal(got.References, []string{"operations[0].output"}) {
		t.Fatalf("missing payload = %#v", got)
	}
	if got := byDigest[corruptDigest]; got.Availability != types.PlaybackCorrupt || !slices.Equal(got.References, []string{"artifacts[0].payload", "evaluations[0].evidence"}) {
		t.Fatalf("corrupt payload = %#v", got)
	}
	if len(playback.Warnings) != 2 || playback.Warnings[0].Digest > playback.Warnings[1].Digest {
		t.Fatalf("deterministic warnings = %#v", playback.Warnings)
	}
	for _, payload := range playback.Payloads {
		if payload.Digest == "" || payload.Preview == "damaged" {
			t.Fatalf("unsafe payload projection = %#v", payload)
		}
	}
}

func TestMissingLogPayloadDegradesPlaybackOnly(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	blobRoot := filepath.Join(directory, "blobs")
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), blobRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	body := []byte(`{"message":"supporting evidence"}`)
	digest := DigestBytes(body)
	ready := map[string]types.Completeness{
		"playback": {State: types.CompletenessReady}, "diff": {State: types.CompletenessReady},
		"replay": {State: types.CompletenessReady}, "bisect": {State: types.CompletenessReady},
	}
	capsule := types.RunCapsule{
		SchemaVersion: types.SchemaVersion, TraceID: "log-playback", ProjectID: "logs", ServiceName: "agent",
		StartTime: time.Unix(1, 0).UTC(), EndTime: time.Unix(2, 0).UTC(), TerminalStatus: "ok", Completeness: ready,
		Logs: []types.LogRecord{{ID: "log-1", Timestamp: time.Unix(1, 0).UTC(), Body: &types.PayloadRef{Digest: digest, MediaType: "application/json", Size: int64(len(body)), State: types.PayloadStateCaptured}}},
	}
	if err := FinalizeCapsule(&capsule, map[string][]byte{digest: body}); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutCapsule(ctx, capsule, map[string][]byte{digest: body}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(localBlobFilename(blobRoot, digest)); err != nil {
		t.Fatal(err)
	}
	capsules := NewCapsuleService(repo, mustPolicy(t))
	if _, err := capsules.Get(ctx, capsule.CapsuleID, true); err == nil {
		t.Fatal("full bundle verification accepted a missing log payload")
	}
	if _, err := capsules.GetWithoutLogs(ctx, capsule.CapsuleID); err != nil {
		t.Fatalf("replay-relevant verification was blocked by log evidence: %v", err)
	}
	playback, err := capsules.Playback(ctx, capsule.CapsuleID)
	if err != nil {
		t.Fatal(err)
	}
	if playback.Completeness.Playback.State != types.CompletenessIncomplete || playback.Completeness.Diff.State != types.CompletenessReady || playback.Completeness.Replay.State != types.CompletenessReady || playback.Completeness.Bisect.State != types.CompletenessReady {
		t.Fatalf("log-only degradation leaked into replay modes: %#v", playback.Completeness)
	}
}

func TestAFRPLY001PlaybackNeverExecutesEntrypointOrUsesOutboundTransport(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	sentinel := filepath.Join(directory, "application-started")
	entrypoint := filepath.Join(directory, "hostile-entrypoint")
	script := "#!/bin/sh\nprintf started > '" + sentinel + "'\n"
	if err := os.WriteFile(entrypoint, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var outbound atomic.Int64
	previousTransport := http.DefaultTransport
	http.DefaultTransport = roundTripCounter{count: &outbound}
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	capsule := types.RunCapsule{
		SchemaVersion: types.SchemaVersion, TraceID: "offline", ProjectID: "incident", ServiceName: "agent",
		StartTime: time.Unix(1, 0).UTC(), EndTime: time.Unix(2, 0).UTC(), TerminalStatus: "ok",
		Completeness: map[string]types.Completeness{"playback": {State: types.CompletenessReady}}, Privacy: types.PrivacySummary{Mode: "strict"},
		Provenance: types.Provenance{
			Entrypoint:       &types.Entrypoint{Command: []string{entrypoint}, InputMode: "stdin_json", OutputMode: "stdout_json"},
			EnvironmentFacts: map[string]string{"hostile_url": "https://external.invalid/must-not-run"},
		},
		Model:      types.ModelConfiguration{Parameters: map[string]any{"base_url": "https://model.invalid"}},
		Operations: []types.Operation{{SpanID: "root", Kind: "agent", Name: "run", StartTime: time.Unix(1, 0).UTC(), EndTime: time.Unix(2, 0).UTC(), Status: "ok", Attributes: map[string]string{"url": "https://tool.invalid"}}},
	}
	if err := FinalizeCapsule(&capsule, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutCapsule(ctx, capsule, nil); err != nil {
		t.Fatal(err)
	}
	result, err := NewCapsuleService(repo, mustPolicy(t)).Playback(ctx, capsule.CapsuleID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Executed || result.ApplicationProcessesStarted != 0 || result.OutboundRequests != 0 || outbound.Load() != 0 {
		t.Fatalf("playback performed active work: result=%#v outbound=%d", result, outbound.Load())
	}
	if _, err := os.Stat(sentinel); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recorded application entrypoint ran during playback: %v", err)
	}
}

func TestPlaybackPreviewIsBoundedAndBinarySafe(t *testing.T) {
	binary := bytes.Repeat([]byte{0xff}, playbackPreviewBytes+1)
	preview, encoding, truncated := playbackPreview(binary, "application/octet-stream")
	decoded, err := base64.StdEncoding.DecodeString(preview)
	if err != nil || encoding != "base64" || !truncated || len(decoded) != playbackPreviewBytes {
		t.Fatalf("binary preview: bytes=%d encoding=%q truncated=%t err=%v", len(decoded), encoding, truncated, err)
	}
}

type roundTripCounter struct{ count *atomic.Int64 }

func (r roundTripCounter) RoundTrip(*http.Request) (*http.Response, error) {
	r.count.Add(1)
	return nil, errors.New("outbound network is disabled during playback")
}

func mustPolicy(t *testing.T) *PolicyEngine {
	t.Helper()
	policy, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func localBlobFilename(root, digest string) string {
	hex := strings.TrimPrefix(digest, "sha256:")
	return filepath.Join(root, "sha256", hex[:2], hex[2:])
}
