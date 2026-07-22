package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/oci"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRT01ThroughT03DiffSurfacesCausalInputDrift(t *testing.T) {
	tests := []struct {
		name       string
		logicalKey string
		kind       string
		before     string
		after      string
	}{
		{name: "AFR-T01 prompt regression", logicalKey: "llm.plan", kind: "llm", before: "sha256:prompt-old", after: "sha256:prompt-new"},
		{name: "AFR-T02 retrieval drift", logicalKey: "retrieval.policy", kind: "retriever", before: "sha256:document-old", after: "sha256:document-new"},
		{name: "AFR-T03 tool schema drift", logicalKey: "tool.payment", kind: "tool", before: "sha256:schema-v1", after: "sha256:schema-v2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := types.RunCapsule{CapsuleID: "base", Operations: []types.Operation{
				{SpanID: "root-base", LogicalKey: "agent", Kind: "agent", Name: "run", Status: "ok", Sequence: 0},
				{SpanID: "cause-base", ParentSpanID: "root-base", LogicalKey: test.logicalKey, Kind: test.kind, Name: test.logicalKey, Status: "ok", Sequence: 1, Input: &types.PayloadRef{Digest: test.before}},
				{SpanID: "tool-base", ParentSpanID: "root-base", LogicalKey: "tool.choice", Kind: "tool", Name: "policy.lookup", Status: "ok", Sequence: 2},
			}}
			candidate := types.RunCapsule{CapsuleID: "candidate", Operations: []types.Operation{
				{SpanID: "root-candidate", LogicalKey: "agent", Kind: "agent", Name: "run", Status: "ok", Sequence: 0},
				{SpanID: "cause-candidate", ParentSpanID: "root-candidate", LogicalKey: test.logicalKey, Kind: test.kind, Name: test.logicalKey, Status: "ok", Sequence: 1, Input: &types.PayloadRef{Digest: test.after}},
				{SpanID: "tool-candidate", ParentSpanID: "root-candidate", LogicalKey: "tool.choice", Kind: "tool", Name: "email.send", Status: "error", Sequence: 2},
			}}
			result := NewDiffService().Compare(baseline, candidate)
			if result.FirstDivergence == nil || result.FirstDivergence.Path != "operations.cause-base.input_digest" {
				t.Fatalf("causal input was not first: %#v", result.FirstDivergence)
			}
		})
	}
}

func TestAFRT04RecordedExternalResponseIsSubstitutedOffline(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(root, "flight.db"), filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	response := []byte(`{"status":"approved"}`)
	digest := DigestBytes(response)
	source := types.RunCapsule{Boundaries: []types.Boundary{{LogicalKey: "api.policy", Class: "http", Occurrence: 0, RequestDigest: "sha256:request", ReplayPolicy: "substitute", Response: &types.PayloadRef{Digest: digest, State: "captured"}}}}
	if err := repo.PutCapsule(ctx, types.RunCapsule{CapsuleID: "response-owner", TraceID: "trace", StartTime: time.Unix(1, 0).UTC(), TerminalStatus: "ok"}, map[string][]byte{digest: response}); err != nil {
		t.Fatal(err)
	}
	replays := NewReplayService(repo, nil, nil, acceptanceRunner{}, NewDiffService(), nil, "")
	envelope, substitutions, live, hosts, err := replays.envelope(ctx, source, types.ReplayPlan{SchemaVersion: types.SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	if len(substitutions) != 1 || substitutions[0] != "api.policy" || len(live) != 0 || len(hosts) != 0 {
		t.Fatalf("unsafe routing: substitutions=%v live=%v hosts=%v", substitutions, live, hosts)
	}
	if !strings.Contains(string(envelope), `"response":{"status":"approved"}`) {
		t.Fatalf("recorded response missing from envelope: %s", envelope)
	}
}

func TestAFRT04ASelectedToolOutputOverridesOnlyThatRecordedResponse(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(root, "flight.db"), filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	recorded := []byte(`{"eligible":false}`)
	digest := DigestBytes(recorded)
	if err := repo.PutCapsule(ctx, types.RunCapsule{CapsuleID: "response-owner", TraceID: "trace", StartTime: time.Unix(1, 0).UTC(), TerminalStatus: "ok"}, map[string][]byte{digest: recorded}); err != nil {
		t.Fatal(err)
	}
	source := types.RunCapsule{Boundaries: []types.Boundary{{ID: "eligibility", LogicalKey: "tool.eligibility", Class: "tool", Occurrence: 0, RequestDigest: "sha256:request", ReplayPolicy: "substitute", Response: &types.PayloadRef{Digest: digest, State: "captured"}}}}
	plan := types.ReplayPlan{SchemaVersion: types.SchemaVersion, Overrides: []types.ReplayOverride{{BoundaryID: "eligibility", Target: "response", Value: json.RawMessage(`{"eligible":true}`)}}}
	replays := NewReplayService(repo, nil, nil, acceptanceRunner{}, NewDiffService(), nil, "")
	envelope, substitutions, live, _, err := replays.envelope(ctx, source, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(substitutions) != 1 || substitutions[0] != "tool.eligibility" || len(live) != 0 {
		t.Fatalf("override routing = substitutions %v live %v", substitutions, live)
	}
	if !strings.Contains(string(envelope), `"override_target":"response"`) || !strings.Contains(string(envelope), `"response":{"eligible":true}`) || strings.Contains(string(envelope), `"response":{"eligible":false}`) {
		t.Fatalf("tool output override missing from envelope: %s", envelope)
	}
}

func TestAFRT04BSelectedModelInputRunsLiveWithTheEditedRequest(t *testing.T) {
	source := types.RunCapsule{Boundaries: []types.Boundary{{ID: "decision", LogicalKey: "llm.decision", Class: "llm", Occurrence: 0, RequestDigest: "sha256:old", ReplayPolicy: "live", SideEffect: "read_only"}}}
	plan := types.ReplayPlan{SchemaVersion: types.SchemaVersion, Entrypoint: types.Entrypoint{ModelHosts: []string{"model.internal"}}, Overrides: []types.ReplayOverride{{BoundaryID: "decision", Target: "request", Value: json.RawMessage(`{"prompt":"approve eligible refunds"}`)}}}
	replays := NewReplayService(nil, nil, nil, acceptanceRunner{}, NewDiffService(), nil, "")
	envelope, substitutions, live, hosts, err := replays.envelope(context.Background(), source, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(substitutions) != 0 || len(live) != 1 || live[0] != "llm.decision" || len(hosts) != 1 || hosts[0] != "model.internal" {
		t.Fatalf("model input routing = substitutions %v live %v hosts %v", substitutions, live, hosts)
	}
	if !strings.Contains(string(envelope), `"override_target":"request"`) || !strings.Contains(string(envelope), `"request_override":{"prompt":"approve eligible refunds"}`) || strings.Contains(string(envelope), `"request_digest":"sha256:old"`) {
		t.Fatalf("model input override missing from envelope: %s", envelope)
	}
}

func TestAFRT04CReplayProvesTheEditedValueWasActuallyApplied(t *testing.T) {
	override := types.ReplayOverride{BoundaryID: "decision", Target: "response", Value: json.RawMessage(`{"decision":"approve"}`)}
	canonical, err := CanonicalJSON(map[string]any{"decision": "approve"})
	if err != nil {
		t.Fatal(err)
	}
	source := types.RunCapsule{Boundaries: []types.Boundary{{ID: "decision", LogicalKey: "llm.decision", Class: "llm", ParentLogicalKey: "agent", Occurrence: 0}}}
	candidate := types.RunCapsule{Boundaries: []types.Boundary{{LogicalKey: "llm.decision", Class: "llm", ParentLogicalKey: "agent", Occurrence: 0, Response: &types.PayloadRef{Digest: DigestBytes(canonical)}}}}
	results := verifyReplayOverrides(source, candidate, []types.ReplayOverride{override})
	if len(results) != 1 || !results[0].Applied {
		t.Fatalf("applied override was not proven: %#v", results)
	}
	candidate.Boundaries[0].Response.Digest = DigestBytes([]byte(`""`))
	results = verifyReplayOverrides(source, candidate, []types.ReplayOverride{override})
	if len(results) != 1 || results[0].Applied {
		t.Fatalf("missing override was falsely reported as applied: %#v", results)
	}
}

func TestModelOverrideMustBeObservedInTheCandidate(t *testing.T) {
	candidate := types.RunCapsule{Model: types.ModelConfiguration{RequestedModel: "model-v1"}}
	if modelOverrideApplied("model-v2", candidate) {
		t.Fatal("candidate using the recorded model was accepted as an applied override")
	}
	candidate.Model.RequestedModel = "model-v2"
	if !modelOverrideApplied("model-v2", candidate) {
		t.Fatal("candidate using the requested model was rejected")
	}
	if !modelOverrideApplied("", types.RunCapsule{}) {
		t.Fatal("replay without a model experiment required model metadata")
	}
}

func TestAFRT05RepeatedTrialsNeverClaimFalseDeterminism(t *testing.T) {
	for _, test := range []struct {
		exact, total int
		threshold    float64
		want         string
	}{{3, 3, 1, "exact"}, {2, 3, 0.6, "equivalent"}, {1, 3, 0.8, "inconclusive"}, {0, 3, 0.5, "divergent"}} {
		if got := classifyReplayOutcome(test.exact, test.total, test.threshold); got != test.want {
			t.Fatalf("classifyReplayOutcome(%d,%d,%v)=%q want %q", test.exact, test.total, test.threshold, got, test.want)
		}
	}
}

func TestAFRT06PlaybackSurvivesMissingReplayEvidence(t *testing.T) {
	request := &types.PayloadRef{Digest: "sha256:" + strings.Repeat("b", 64), State: types.PayloadStateCaptured}
	capsule := types.RunCapsule{
		Operations: []types.Operation{{SpanID: "root", Kind: "agent", Name: "run", Input: request}},
		Provenance: types.Provenance{Entrypoint: &types.Entrypoint{Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64)}},
		Boundaries: []types.Boundary{{ID: "missing-response", OperationSpanID: "root", LogicalKey: "tool.lookup"}},
	}
	ComputeCompleteness(&capsule)
	if capsule.Completeness["playback"].State != types.CompletenessReady || capsule.Completeness["replay"].State != types.CompletenessBlocked {
		t.Fatalf("unexpected completeness: %#v", capsule.Completeness)
	}
	missing := capsule.Completeness["replay"].Missing
	if len(missing) != 1 || missing[0].BoundaryID != "missing-response" {
		t.Fatalf("missing prerequisite is not actionable: %#v", missing)
	}
}

func TestAFRT06ReplayCompletenessRequiresRootInputAndEveryExternalBoundary(t *testing.T) {
	capsule := types.RunCapsule{
		Operations: []types.Operation{
			{SpanID: "root", Kind: "agent", Name: "run"},
			{SpanID: "model", ParentSpanID: "root", Kind: "llm", Name: "answer", Input: &types.PayloadRef{Digest: "sha256:request"}, Output: &types.PayloadRef{Digest: "sha256:response"}},
		},
		Provenance: types.Provenance{Entrypoint: &types.Entrypoint{Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64)}},
	}
	ComputeCompleteness(&capsule)
	missing := capsule.Completeness["replay"].Missing
	if !slices.ContainsFunc(missing, func(item types.MissingItem) bool { return item.Code == "root_input_missing" }) || !slices.ContainsFunc(missing, func(item types.MissingItem) bool { return item.Code == "boundary_declaration_missing" }) {
		t.Fatalf("replay completeness = %#v", capsule.Completeness["replay"])
	}
}

func TestAFRT10SandboxedMutationRequiresDeclarationAndFlag(t *testing.T) {
	image := "sha256:" + strings.Repeat("a", 64)
	source := types.RunCapsule{
		Completeness: map[string]types.Completeness{"replay": {State: types.CompletenessReady}},
		Provenance:   types.Provenance{Entrypoint: &types.Entrypoint{Command: []string{"/agent"}, Image: image, Sandboxes: map[string]string{"email-test": "https://mail.test.invalid"}}},
		Boundaries:   []types.Boundary{{LogicalKey: "email.send", ReplayPolicy: "live", SideEffect: "mutating", Sandbox: "email-test"}},
	}
	replays := NewReplayService(nil, nil, nil, acceptanceRunner{}, nil, nil, "")
	err := replays.Preflight(context.Background(), source, types.CreateReplayRequest{SubstituteLLM: true})
	var flightErr *types.FlightError
	if !errors.As(err, &flightErr) || flightErr.Code != types.ErrUnsafeReplay {
		t.Fatalf("mutation was not blocked without explicit flag: %v", err)
	}
	if err := replays.Preflight(context.Background(), source, types.CreateReplayRequest{SubstituteLLM: true, AllowSandboxedSideEffects: true}); err != nil {
		t.Fatalf("declared sandbox was not accepted with explicit flag: %v", err)
	}
}

func TestLiveModelModeRequiresARecordedLiveLLMBoundary(t *testing.T) {
	source := types.RunCapsule{
		Completeness: map[string]types.Completeness{"replay": {State: types.CompletenessReady}},
		Provenance: types.Provenance{Entrypoint: &types.Entrypoint{
			Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64),
			ModelHosts: []string{"model.test.invalid"}, Environment: []string{"MODEL_TOKEN"},
		}},
	}
	replays := NewReplayService(nil, nil, nil, acceptanceRunner{}, nil, map[string]string{"MODEL_TOKEN": "worker-only"}, "")
	err := replays.Preflight(context.Background(), source, types.CreateReplayRequest{})
	var flightErr *types.FlightError
	if !errors.As(err, &flightErr) || flightErr.Details["issue_code"] != "live_model_boundary_missing" {
		t.Fatalf("missing live LLM boundary = %v", err)
	}
}

type acceptanceRunner struct{}

func (acceptanceRunner) Available(context.Context) error { return nil }
func (acceptanceRunner) Run(context.Context, oci.RunSpec) (oci.RunResult, error) {
	return oci.RunResult{}, nil
}
