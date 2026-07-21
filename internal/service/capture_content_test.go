package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRCAP003TypedOmissionsRetainSafeEvidenceAndBlockAffectedBoundary(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	oversized := []byte(`{"content":"` + strings.Repeat("x", 1500) + `"}`)
	toolDefinitions := []byte(`[{"type":"function","name":"` + strings.Repeat("t", 1500) + `"}]`)
	spanID := strings.Repeat("1", 16)
	capsule, err := NewCapsuleService(repo, policy).Capture(ctx, CaptureRequest{
		TraceID: "11111111111111111111111111111111", ProjectID: "content", ServiceName: "agent", StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0),
		Provenance:      types.Provenance{CommitSHA: strings.Repeat("a", 40), Entrypoint: &types.Entrypoint{Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64), InputMode: "stdin_json", OutputMode: "stdout_json"}},
		ToolDefinitions: []CapturePayload{{Data: toolDefinitions, MediaType: "application/json"}},
		Operations: []CaptureOperation{{
			Operation: types.Operation{SpanID: spanID, LogicalKey: "api.policy", Kind: "api", Name: "policy", StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0), Status: "ok"},
			Input:     []byte(`{"broken":`), InputType: "application/json", Output: oversized, OutputType: "application/json",
			Boundary: &types.Boundary{LogicalKey: "api.policy", Class: "api", RequestDigest: "sha256:" + strings.Repeat("b", 64), SideEffect: "read_only", ReplayPolicy: "substitute", MatchingRule: types.MatchingRule{Mode: "exact"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := capsule.Operations[0]
	if operation.Input == nil || operation.Input.State != types.PayloadStateOmitted || operation.Input.Reason != types.OmissionSerialization || operation.Input.Size != int64(len(`{"broken":`)) || operation.Input.Digest != "" {
		t.Fatalf("serialization omission = %#v", operation.Input)
	}
	if operation.Output == nil || operation.Output.State != types.PayloadStateOmitted || operation.Output.Reason != types.OmissionPayloadLimit || operation.Output.Size != int64(len(oversized)) || operation.Output.Digest != "" {
		t.Fatalf("limit omission = %#v", operation.Output)
	}
	if len(capsule.Model.ToolDefinitions) != 1 || capsule.Model.ToolDefinitions[0].State != types.PayloadStateOmitted || capsule.Model.ToolDefinitions[0].Reason != types.OmissionPayloadLimit || capsule.Model.ToolDefinitions[0].Size != int64(len(toolDefinitions)) {
		t.Fatalf("tool definition omission = %#v", capsule.Model.ToolDefinitions)
	}
	if len(capsule.Integrity.PayloadDigests) != 0 {
		t.Fatalf("omitted raw content was hashed as durable payloads: %#v", capsule.Integrity.PayloadDigests)
	}
	if capsule.Completeness["playback"].State != types.CompletenessIncomplete || capsule.Completeness["diff"].State != types.CompletenessIncomplete || capsule.Completeness["replay"].State != types.CompletenessBlocked {
		t.Fatalf("omission completeness = %#v", capsule.Completeness)
	}
	playbackCodes := []string{}
	for _, item := range capsule.Completeness["playback"].Missing {
		playbackCodes = append(playbackCodes, item.Code)
	}
	if !slices.Equal(playbackCodes, []string{"operation_input_omitted", "operation_output_omitted", "tool_definition_omitted"}) {
		t.Fatalf("nondeterministic playback omissions: %v", playbackCodes)
	}
	codes := []string{}
	for _, item := range capsule.Completeness["replay"].Missing {
		codes = append(codes, item.Code)
	}
	for _, code := range []string{"tool_definition_omitted", "boundary_request_omitted", "boundary_response_missing"} {
		if !slices.Contains(codes, code) {
			t.Fatalf("replay omissions %v do not include %s", codes, code)
		}
	}
}

func TestAFRSEC004ClassLimitsPersistTypedPartialEvidenceAndCompleteness(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, err := NewPolicyEngine(config.CapturePolicy{
		Version: "1", Mode: "strict", MaxFieldSize: 1024,
		PayloadLimits: []config.PayloadLimit{
			{Name: "truncate-input", Field: "operations.input", MaxBytes: 96, Action: "truncate"},
			{Name: "keep-output", Field: "operations.output", Action: "keep"},
			{Name: "omit-tools", Field: "model_configuration.tool_definitions", MaxBytes: 32, Action: "omit"},
			{Name: "truncate-transcript", Field: "artifacts", ArtifactClass: "transcript", MaxBytes: 96, Action: "truncate"},
			{Name: "omit-other-artifacts", Field: "artifacts", MaxBytes: 8, Action: "omit"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := []byte(`{"prompt":"` + strings.Repeat("p", 200) + `"}`)
	output := []byte(`{"answer":"kept"}`)
	tools := []byte(`[{"type":"function","name":"` + strings.Repeat("t", 80) + `"}]`)
	transcript := []byte(`{"turn":"` + strings.Repeat("x", 200) + `"}`)
	screenshot := []byte("0123456789abcdef")
	capsule, err := NewCapsuleService(repo, policy).Capture(ctx, CaptureRequest{
		TraceID: "44444444444444444444444444444444", ProjectID: "retention", ServiceName: "agent", StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0),
		Provenance:      types.Provenance{CommitSHA: strings.Repeat("a", 40), Entrypoint: &types.Entrypoint{Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64), InputMode: "stdin_json", OutputMode: "stdout_json"}},
		ToolDefinitions: []CapturePayload{{Data: tools, MediaType: "application/json"}},
		Operations: []CaptureOperation{{
			Operation: types.Operation{SpanID: "4444444444444444", LogicalKey: "api.retention", Kind: "api", Name: "retention", StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0), Status: "ok"},
			Input:     input, InputType: "application/json", Output: output, OutputType: "application/json",
			Boundary: &types.Boundary{LogicalKey: "api.retention", Class: "api", SideEffect: "read_only", ReplayPolicy: "substitute", MatchingRule: types.MatchingRule{Mode: "exact"}},
		}},
		Artifacts: []struct {
			Artifact types.Artifact
			Data     []byte
		}{
			{Artifact: types.Artifact{ID: "transcript", Type: "transcript", LogicalName: "session", MediaType: "application/json"}, Data: transcript},
			{Artifact: types.Artifact{ID: "screenshot", Type: "screenshot", LogicalName: "screen", MediaType: "image/png"}, Data: screenshot},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	operation := capsule.Operations[0]
	if !payloadTruncated(operation.Input) || operation.Input.OriginalSize != int64(len(input)) || operation.Input.Size > 96 {
		t.Fatalf("truncated input = %#v", operation.Input)
	}
	if operation.Output == nil || operation.Output.State != types.PayloadStateCaptured || operation.Output.Reason != "" {
		t.Fatalf("kept output = %#v", operation.Output)
	}
	if len(capsule.Model.ToolDefinitions) != 1 || !payloadOmitted(&capsule.Model.ToolDefinitions[0]) {
		t.Fatalf("omitted tool definition = %#v", capsule.Model.ToolDefinitions)
	}
	if len(capsule.Artifacts) != 2 || !payloadTruncated(capsule.Artifacts[0].Payload) || !payloadOmitted(capsule.Artifacts[1].Payload) {
		t.Fatalf("artifact limits = %#v", capsule.Artifacts)
	}
	if capsule.Privacy.Truncations["truncate-input"] != 1 || capsule.Privacy.Truncations["truncate-transcript"] != 1 {
		t.Fatalf("truncation summary = %#v", capsule.Privacy.Truncations)
	}
	if len(capsule.Integrity.PayloadDigests) != 3 {
		t.Fatalf("payload digests = %#v", capsule.Integrity.PayloadDigests)
	}
	stored, err := repo.LoadPayload(ctx, operation.Input.Digest)
	if err != nil || !json.Valid(stored) || !strings.Contains(string(stored), "_flight_truncated_prefix_base64") {
		t.Fatalf("stored truncated evidence = %s, %v", stored, err)
	}
	missing := capsule.Completeness["replay"].Missing
	for _, code := range []string{"root_input_truncated", "boundary_request_truncated", "tool_definition_omitted"} {
		if !slices.ContainsFunc(missing, func(item types.MissingItem) bool { return item.Code == code }) {
			t.Fatalf("replay completeness %#v does not include %s", missing, code)
		}
	}
	if capsule.Completeness["replay"].State != types.CompletenessBlocked || capsule.Completeness["playback"].State != types.CompletenessIncomplete {
		t.Fatalf("class-limit completeness = %#v", capsule.Completeness)
	}
}

func TestAFRSEC004CapturePersistsProjectSpecificExpiry(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, err := NewPolicyEngine(config.CapturePolicy{
		Version:          "1",
		Mode:             "strict",
		MaxFieldSize:     1024,
		DefaultRetention: 30 * 24 * time.Hour,
		ProjectRetention: map[string]time.Duration{"short-lived": time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	ended := time.Date(2026, time.July, 21, 4, 0, 0, 0, time.UTC)
	capsule, err := NewCapsuleService(repo, policy).Capture(ctx, CaptureRequest{
		TraceID:     "55555555555555555555555555555555",
		ProjectID:   "short-lived",
		ServiceName: "agent",
		StartTime:   ended.Add(-time.Minute),
		EndTime:     ended,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ended.Add(time.Hour)
	if capsule.ExpiresAt == nil || !capsule.ExpiresAt.Equal(want) {
		t.Fatalf("capture expiry = %v, want %s", capsule.ExpiresAt, want)
	}
	stored, err := repo.GetCapsule(ctx, capsule.CapsuleID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ExpiresAt == nil || !stored.ExpiresAt.Equal(want) {
		t.Fatalf("stored expiry = %v, want %s", stored.ExpiresAt, want)
	}
}

func TestAFRCAP003ProducerOmissionMetadataIsNormalized(t *testing.T) {
	service := &CapsuleService{}
	ref, data, _, err := service.captureContent("operations.input", CapturePayload{Omission: &types.PayloadRef{Digest: "sha256:" + strings.Repeat("A", 64), Size: -1, Reason: "producer_guess"}})
	if err != nil || data != nil || ref == nil || ref.State != types.PayloadStateOmitted || ref.Reason != types.OmissionInvalidMetadata || ref.Size != 0 || ref.Digest != "" {
		t.Fatalf("normalized omission = %#v data=%q err=%v", ref, data, err)
	}
	encoded, err := json.Marshal(ref)
	if err != nil || strings.Contains(string(encoded), `"digest"`) {
		t.Fatalf("omission JSON must not emit an empty schema-invalid digest: %s (err=%v)", encoded, err)
	}
}

func TestPayloadValidationRecognizesStructuredJSONSuffixes(t *testing.T) {
	policy, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	service := &CapsuleService{policy: policy}
	ref, data, _, err := service.capturePayload("operations.output", "", []byte(`{"broken":`), "application/problem+json; charset=utf-8")
	if err != nil || data != nil || ref.State != types.PayloadStateOmitted || ref.Reason != types.OmissionSerialization {
		t.Fatalf("structured JSON validation = %#v data=%q err=%v", ref, data, err)
	}
}

func TestAFRCAP003ReplayInputComesFromRootInsteadOfSequencePosition(t *testing.T) {
	child := &types.PayloadRef{Digest: "sha256:" + strings.Repeat("c", 64), State: types.PayloadStateCaptured}
	root := &types.PayloadRef{Digest: "sha256:" + strings.Repeat("d", 64), State: types.PayloadStateCaptured}
	operations := []types.Operation{
		{SpanID: "child", ParentSpanID: "root", Input: child, Sequence: 0},
		{SpanID: "root", Input: root, Sequence: 1},
	}
	if actual := replayRootInput(operations); actual != root {
		t.Fatalf("replay input = %#v, want root %#v", actual, root)
	}
}

func TestAFRSEC001BoundaryDigestComesFromPostPolicyCanonicalRequest(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1024, Rules: []config.PolicyRule{{Name: "secret", Path: "*", Pattern: `AFR_SECRET_[A-Z0-9]+`, Action: "redact", Replacement: "[REDACTED]"}}})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"z":"AFR_SECRET_123","a":1}`)
	capsule, err := NewCapsuleService(repo, policy).Capture(ctx, CaptureRequest{
		TraceID: "33333333333333333333333333333333", ProjectID: "privacy", ServiceName: "agent",
		Operations: []CaptureOperation{{Operation: types.Operation{SpanID: "3333333333333333", Kind: "tool"}, Input: raw, InputType: "application/json", Output: []byte(`{"ok":true}`), OutputType: "application/json", Boundary: &types.Boundary{LogicalKey: "tool", Class: "tool", RequestDigest: DigestBytes(raw), ReplayPolicy: "substitute", MatchingRule: types.MatchingRule{Mode: "json_pointer", IgnorePaths: []string{"/a"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalJSON(map[string]any{"z": "[REDACTED]"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := capsule.Boundaries[0].RequestDigest, DigestBytes(canonical); got != want || got == DigestBytes(raw) {
		t.Fatalf("post-policy request digest = %q, want %q and not raw %q", got, want, DigestBytes(raw))
	}
}

func TestBoundaryIgnorePathsUseJSONPointerEscapesAndArrayRemoval(t *testing.T) {
	value := map[string]any{"a/b": map[string]any{"~secret": "drop", "keep": true}, "items": []any{"zero", "one", "two"}}
	updated, err := applyBoundaryIgnores(value, []string{"/a~1b/~0secret", "/items/1"})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalJSON(updated)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(canonical), `{"a/b":{"keep":true},"items":["zero","two"]}`; got != want {
		t.Fatalf("ignored request = %s, want %s", got, want)
	}
	if _, err := applyBoundaryIgnores(map[string]any{}, []string{"not-a-pointer"}); err == nil {
		t.Fatal("invalid JSON pointer was accepted")
	}
}

func TestAFRCAP003ReferenceMetadataCannotOrphanPolicySafePayload(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1 << 20, Rules: []config.PolicyRule{
		{Name: "operation-ref", Path: "operations.metadata.input.digest", Action: "exclude"},
		{Name: "boundary-ref", Path: "boundaries.metadata.response.digest", Action: "exclude"},
		{Name: "model-ref", Path: "model_configuration.tool_definitions[0].digest", Action: "exclude"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := NewCapsuleService(repo, policy).Capture(ctx, CaptureRequest{
		TraceID: "22222222222222222222222222222222", ProjectID: "content", ServiceName: "agent",
		Model:           types.ModelConfiguration{ToolDefinitions: []types.PayloadRef{{Digest: "sha256:" + strings.Repeat("f", 64), Size: 1, State: types.PayloadStateCaptured}}},
		ToolDefinitions: []CapturePayload{{Data: []byte(`[{"type":"function","name":"lookup"}]`), MediaType: "application/json"}},
		Artifacts: []struct {
			Artifact types.Artifact
			Data     []byte
		}{{Artifact: types.Artifact{ID: "invalid-ref", Type: "fixture", LogicalName: "invalid-ref", MediaType: "application/json", State: types.PayloadStateCaptured, Payload: &types.PayloadRef{Digest: "sha256:" + strings.Repeat("e", 64), Size: 1, State: types.PayloadStateCaptured}}}},
		Operations: []CaptureOperation{{
			Operation: types.Operation{SpanID: "2222222222222222", Kind: "api", Name: "lookup", Status: "ok"},
			Input:     []byte(`{"query":"policy"}`), InputType: "application/json", Output: []byte(`{"rule":"recorded"}`), OutputType: "application/json",
			Boundary: &types.Boundary{LogicalKey: "api.policy", Class: "api", RequestDigest: "sha256:" + strings.Repeat("a", 64), SideEffect: "read_only", ReplayPolicy: "substitute", MatchingRule: types.MatchingRule{Mode: "exact"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if capsule.Operations[0].Input == nil || capsule.Operations[0].Output == nil || capsule.Boundaries[0].Response == nil || len(capsule.Model.ToolDefinitions) != 1 || len(capsule.Integrity.PayloadDigests) != 3 {
		t.Fatalf("policy-safe references were orphaned: %#v", capsule)
	}
	if capsule.Artifacts[0].Payload == nil || capsule.Artifacts[0].Payload.State != types.PayloadStateOmitted || capsule.Artifacts[0].Payload.Reason != types.OmissionInvalidMetadata || slices.Contains(capsule.Integrity.PayloadDigests, "sha256:"+strings.Repeat("e", 64)) {
		t.Fatalf("unverified artifact reference bypassed typed capture: %#v", capsule.Artifacts[0])
	}
}
