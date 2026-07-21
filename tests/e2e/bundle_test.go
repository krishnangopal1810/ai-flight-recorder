//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/api"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRCAP004BundleRoundTripPreservesIdentityOffline(t *testing.T) {
	ctx := context.Background()
	sourceRoot, targetRoot := t.TempDir(), t.TempDir()
	source, err := repository.OpenLocal(ctx, filepath.Join(sourceRoot, "flight.db"), filepath.Join(sourceRoot, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := repository.OpenLocal(ctx, filepath.Join(targetRoot, "flight.db"), filepath.Join(targetRoot, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	policy, _ := service.NewPolicyEngine(config.CapturePolicy{Version: "test", Mode: "strict", MaxFieldSize: 1 << 20})
	capsules := service.NewCapsuleService(source, policy)
	capsule, err := capsules.Capture(ctx, service.CaptureRequest{TraceID: strings.Repeat("a", 32), ProjectID: "e2e", ServiceName: "reference", StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0), Operations: []service.CaptureOperation{{Operation: types.Operation{SpanID: strings.Repeat("b", 16), Name: "answer", Kind: "agent", Status: "ok"}, Input: []byte(`{"question":"why"}`), Output: []byte(`{"answer":"evidence"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	var bundle bytes.Buffer
	if err := service.NewBundleService(source).Export(ctx, capsule, &bundle); err != nil {
		t.Fatal(err)
	}
	imported, err := service.NewBundleService(target).Import(ctx, bytes.NewReader(bundle.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if imported.CapsuleID != capsule.CapsuleID || imported.Integrity.RootDigest != capsule.Integrity.RootDigest {
		t.Fatalf("identity changed: %s != %s", imported.CapsuleID, capsule.CapsuleID)
	}
	if err := service.VerifyCapsule(imported, func(digest string) ([]byte, error) { return target.LoadPayload(ctx, digest) }); err != nil {
		t.Fatal(err)
	}
	assertOfflinePlayback(t, target, policy, imported)
}

func TestAFRCAP004CompatibleMinorRoundTripPreservesNestedUnknownFields(t *testing.T) {
	ctx := context.Background()
	source, target, sourceRoot, _ := openBundleRepositories(t)
	policy, _ := service.NewPolicyEngine(config.CapturePolicy{Version: "test", Mode: "strict", MaxFieldSize: 1 << 20})
	base := captureBundleCapsule(t, source, policy)
	forward, payloads := forwardCompatibleCapsule(t, source, base, "1.7")
	if err := source.PutCapsule(ctx, forward, payloads); err != nil {
		t.Fatal(err)
	}

	var first bytes.Buffer
	if err := service.NewBundleService(source).Export(ctx, forward, &first); err != nil {
		t.Fatal(err)
	}
	imported, err := service.NewBundleService(target).Import(ctx, bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if imported.SchemaVersion != "1.7" || imported.CapsuleID != forward.CapsuleID || imported.Integrity.RootDigest != forward.Integrity.RootDigest {
		t.Fatalf("compatible minor identity changed: %#v", imported)
	}
	encoded, err := json.Marshal(imported)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range [][]byte{
		[]byte(`"future_contract":{"large":9007199254740991}`),
		[]byte(`"future_integrity":"kept"`),
		[]byte(`"future_provenance":"kept"`),
		[]byte(`"future_operation":"kept"`),
		[]byte(`"future_input":"kept"`),
	} {
		if !bytes.Contains(encoded, fragment) {
			t.Fatalf("compatible unknown field %s was lost: %s", fragment, encoded)
		}
	}
	var second bytes.Buffer
	if err := service.NewBundleService(target).Export(ctx, imported, &second); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("compatible import/export changed deterministic bundle bytes: source=%d target=%d root=%s", len(first.Bytes()), len(second.Bytes()), sourceRoot)
	}
}

func TestAFRCAP004UnsupportedMajorImportIsAtomic(t *testing.T) {
	ctx := context.Background()
	source, target, _, targetRoot := openBundleRepositories(t)
	policy, _ := service.NewPolicyEngine(config.CapturePolicy{Version: "test", Mode: "strict", MaxFieldSize: 1 << 20})
	base := captureBundleCapsule(t, source, policy)
	unsupported, payloads := forwardCompatibleCapsule(t, source, base, "2.0")
	if err := source.PutCapsule(ctx, unsupported, payloads); err != nil {
		t.Fatal(err)
	}
	var bundle bytes.Buffer
	if err := service.NewBundleService(source).Export(ctx, unsupported, &bundle); err != nil {
		t.Fatal(err)
	}

	_, err := service.NewBundleService(target).Import(ctx, bytes.NewReader(bundle.Bytes()))
	var flightErr *types.FlightError
	if !errors.As(err, &flightErr) || flightErr.Code != types.ErrValidation || flightErr.Message != "unsupported capsule schema major version" {
		t.Fatalf("unsupported major error = %#v", err)
	}
	items, listErr := target.ListCapsules(ctx, "", 10)
	if listErr != nil || len(items) != 0 {
		t.Fatalf("unsupported import persisted capsule metadata: %#v %v", items, listErr)
	}
	entries, readErr := os.ReadDir(filepath.Join(targetRoot, "blobs"))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("unsupported import persisted blob data: %#v %v", entries, readErr)
	}
}

func openBundleRepositories(t *testing.T) (*repository.LocalRepository, *repository.LocalRepository, string, string) {
	t.Helper()
	ctx := context.Background()
	sourceRoot, targetRoot := t.TempDir(), t.TempDir()
	source, err := repository.OpenLocal(ctx, filepath.Join(sourceRoot, "flight.db"), filepath.Join(sourceRoot, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	target, err := repository.OpenLocal(ctx, filepath.Join(targetRoot, "flight.db"), filepath.Join(targetRoot, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	return source, target, sourceRoot, targetRoot
}

func captureBundleCapsule(t *testing.T, repo repository.Repository, policy *service.PolicyEngine) types.RunCapsule {
	t.Helper()
	capsule, err := service.NewCapsuleService(repo, policy).Capture(context.Background(), service.CaptureRequest{
		TraceID: strings.Repeat("c", 32), ProjectID: "compatibility", ServiceName: "reference",
		StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0),
		Operations: []service.CaptureOperation{{
			Operation: types.Operation{SpanID: strings.Repeat("d", 16), LogicalKey: "agent.answer", Name: "answer", Kind: "agent", Status: "ok"},
			Input:     []byte(`{"question":"why"}`), Output: []byte(`{"answer":"evidence"}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return capsule
}

func forwardCompatibleCapsule(t *testing.T, repo repository.Repository, capsule types.RunCapsule, version string) (types.RunCapsule, map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	payloads := map[string][]byte{}
	for _, digest := range capsule.Integrity.PayloadDigests {
		data, err := repo.LoadPayload(ctx, digest)
		if err != nil {
			t.Fatal(err)
		}
		payloads[digest] = data
	}
	raw, err := json.Marshal(capsule)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["schema_version"] = json.RawMessage(`"` + version + `"`)
	document["future_contract"] = json.RawMessage(`{"large":9007199254740991}`)
	addUnknownObjectField(t, document, "integrity", "future_integrity", `"kept"`)
	addUnknownObjectField(t, document, "provenance", "future_provenance", `"kept"`)
	var operations []map[string]json.RawMessage
	if err := json.Unmarshal(document["operations"], &operations); err != nil {
		t.Fatal(err)
	}
	operations[0]["future_operation"] = json.RawMessage(`"kept"`)
	var input map[string]json.RawMessage
	if err := json.Unmarshal(operations[0]["input"], &input); err != nil {
		t.Fatal(err)
	}
	input["future_input"] = json.RawMessage(`"kept"`)
	operations[0]["input"], err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	document["operations"], err = json.Marshal(operations)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var forward types.RunCapsule
	if err := json.Unmarshal(raw, &forward); err != nil {
		t.Fatal(err)
	}
	if err := service.FinalizeCapsule(&forward, payloads); err != nil {
		t.Fatal(err)
	}
	return forward, payloads
}

func addUnknownObjectField(t *testing.T, document map[string]json.RawMessage, field, key, value string) {
	t.Helper()
	object := map[string]json.RawMessage{}
	if err := json.Unmarshal(document[field], &object); err != nil {
		t.Fatal(err)
	}
	object[key] = json.RawMessage(value)
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	document[field] = encoded
}

func assertOfflinePlayback(t *testing.T, repo repository.Repository, policy *service.PolicyEngine, capsule types.RunCapsule) {
	t.Helper()
	cfg := config.Default(t.TempDir())
	cfg.Auth.LocalBypass = true
	cfg.Server.WebDir = filepath.Join(t.TempDir(), "missing")
	capsules := service.NewCapsuleService(repo, policy)
	server := api.NewServer(cfg, capsules, nil, nil, nil, nil, nil, nil, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/capsules/"+capsule.CapsuleID+"/playback", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("offline playback status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Capsule                     types.RunCapsule          `json:"capsule"`
		Completeness                types.CapsuleCompleteness `json:"completeness"`
		Payloads                    []types.PlaybackPayload   `json:"payloads"`
		Warnings                    []types.PlaybackWarning   `json:"warnings"`
		Executed                    bool                      `json:"executed"`
		ApplicationProcessesStarted int                       `json:"application_processes_started"`
		OutboundRequests            int                       `json:"outbound_requests"`
		ManifestVerified            bool                      `json:"manifest_verified"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Executed || result.ApplicationProcessesStarted != 0 || result.OutboundRequests != 0 || !result.ManifestVerified || result.Capsule.CapsuleID != capsule.CapsuleID || len(result.Warnings) != 0 || len(result.Payloads) != len(capsule.Integrity.PayloadDigests) || result.Completeness.Playback.State == types.CompletenessBlocked {
		t.Fatalf("offline playback executed code or changed identity: %#v", result)
	}
}
