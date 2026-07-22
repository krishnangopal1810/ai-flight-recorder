package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/oci"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestReplayPreflightUsesTheCreationPlanWithoutStartingTheRunner(t *testing.T) {
	ctx, fixture := newReplayPreflightFixture(t, types.Entrypoint{
		Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64),
		InputMode: "stdin_json", OutputMode: "stdout_json",
	})
	report, err := fixture.replays.Check(ctx, types.CreateReplayRequest{SourceCapsuleID: fixture.capsule.CapsuleID, SubstituteLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.State != types.ReplayPreflightReady || len(report.Issues) != 0 || len(report.Effective.Substitutions) != 1 || report.Effective.Substitutions[0] != "llm.answer" {
		t.Fatalf("all-substituted preflight = %#v", report)
	}
	if fixture.runner.runs != 0 {
		t.Fatalf("preflight started %d OCI runs", fixture.runner.runs)
	}
}

func TestBlockedReplayPreflightDoesNotCreateAJobOrReachTheRunner(t *testing.T) {
	ctx, fixture := newReplayPreflightFixture(t, types.Entrypoint{
		Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64),
		InputMode: "stdin_json", OutputMode: "stdout_json",
	})
	request := types.CreateReplayRequest{SourceCapsuleID: fixture.capsule.CapsuleID, Revision: "deadbeef", SubstituteLLM: true}
	report, err := fixture.replays.Check(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != types.ReplayPreflightBlocked || len(report.Issues) != 1 || report.Issues[0].Code != "revision_image_missing" {
		t.Fatalf("revision-only preflight = %#v", report)
	}
	if _, err := fixture.replays.Create(ctx, request); err == nil {
		t.Fatal("blocked replay created a job")
	}
	jobs, err := fixture.repo.ListJobs(ctx, 10)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("blocked replay jobs = %#v, err=%v", jobs, err)
	}
	if fixture.runner.available != 0 || fixture.runner.runs != 0 {
		t.Fatalf("blocked request reached OCI: available=%d runs=%d", fixture.runner.available, fixture.runner.runs)
	}
}

func TestCandidateCommitAlsoRequiresAResolvedImage(t *testing.T) {
	ctx, fixture := newReplayPreflightFixture(t, types.Entrypoint{
		Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64),
		InputMode: "stdin_json", OutputMode: "stdout_json",
	})
	report, err := fixture.replays.Check(ctx, types.CreateReplayRequest{SourceCapsuleID: fixture.capsule.CapsuleID, CandidateCommit: "deadbeef", SubstituteLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.State != types.ReplayPreflightBlocked || len(report.Issues) != 1 || report.Issues[0].Code != "revision_image_missing" {
		t.Fatalf("commit-only preflight = %#v", report)
	}
}

func TestLiveModelPreflightSeparatesHostAndCredentialFailures(t *testing.T) {
	tests := []struct {
		name        string
		entrypoint  types.Entrypoint
		credentials map[string]string
		wantCode    string
	}{
		{name: "host", entrypoint: types.Entrypoint{}, wantCode: "model_host_missing"},
		{name: "credential declaration", entrypoint: types.Entrypoint{ModelHosts: []string{"model.test.invalid"}}, wantCode: "model_credential_missing"},
		{name: "worker credential", entrypoint: types.Entrypoint{ModelHosts: []string{"model.test.invalid"}, Environment: []string{"MODEL_TOKEN"}}, wantCode: "replay_credential_unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.entrypoint.Command = []string{"/agent"}
			test.entrypoint.Image = "sha256:" + strings.Repeat("a", 64)
			test.entrypoint.InputMode, test.entrypoint.OutputMode = "stdin_json", "stdout_json"
			ctx, fixture := newReplayPreflightFixture(t, test.entrypoint)
			fixture.replays.credentials = test.credentials
			report, err := fixture.replays.Check(ctx, types.CreateReplayRequest{SourceCapsuleID: fixture.capsule.CapsuleID})
			if err != nil {
				t.Fatal(err)
			}
			if report.State != types.ReplayPreflightBlocked || len(report.Issues) != 1 || report.Issues[0].Code != test.wantCode {
				t.Fatalf("preflight = %#v, want %s", report, test.wantCode)
			}
		})
	}
}

func TestLiveModelPreflightReturnsNamesAndNeverCredentialValues(t *testing.T) {
	entrypoint := types.Entrypoint{
		Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64), InputMode: "stdin_json", OutputMode: "stdout_json",
		ModelHosts: []string{"model.test.invalid"}, Environment: []string{"MODEL_TOKEN"}, ModelOverride: true,
	}
	ctx, fixture := newReplayPreflightFixture(t, entrypoint)
	fixture.replays.credentials = map[string]string{"MODEL_TOKEN": "secret-worker-value"}
	report, err := fixture.replays.Check(ctx, types.CreateReplayRequest{SourceCapsuleID: fixture.capsule.CapsuleID, Model: "model-v2"})
	if err != nil {
		t.Fatal(err)
	}
	if report.State != types.ReplayPreflightReady || report.Effective.Model != "model-v2" || len(report.Effective.AllowedHosts) != 1 || len(report.Effective.EnvironmentNames) != 1 {
		t.Fatalf("live model preflight = %#v", report)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-worker-value") {
		t.Fatalf("preflight leaked a worker credential: %s", encoded)
	}
}

func TestReplayPreflightReportsRuntimeAvailability(t *testing.T) {
	ctx, fixture := newReplayPreflightFixture(t, types.Entrypoint{
		Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64),
		InputMode: "stdin_json", OutputMode: "stdout_json",
	})
	fixture.runner.availableError = errors.New("docker is stopped")
	report, err := fixture.replays.Check(ctx, types.CreateReplayRequest{SourceCapsuleID: fixture.capsule.CapsuleID, SubstituteLLM: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.State != types.ReplayPreflightBlocked || len(report.Issues) != 1 || report.Issues[0].Code != "oci_runtime_unavailable" || strings.Contains(report.Issues[0].Message, "docker is stopped") {
		t.Fatalf("runtime preflight = %#v", report)
	}
}

func TestReplayPreflightReturnsStableBoundarySpecificOverrideIssues(t *testing.T) {
	ctx, fixture := newReplayPreflightFixture(t, types.Entrypoint{
		Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64),
		InputMode: "stdin_json", OutputMode: "stdout_json",
	})
	report, err := fixture.replays.Check(ctx, types.CreateReplayRequest{
		SourceCapsuleID: fixture.capsule.CapsuleID, SubstituteLLM: true,
		Overrides: []types.ReplayOverride{{BoundaryID: "missing-boundary", Target: "response", Value: json.RawMessage(`{"answer":"changed"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.State != types.ReplayPreflightBlocked || len(report.Issues) != 1 || report.Issues[0].Code != "replay_override_boundary_missing" || report.Issues[0].BoundaryID != "missing-boundary" {
		t.Fatalf("override preflight = %#v", report)
	}
}

type replayPreflightFixture struct {
	repo    repository.Repository
	capsule types.RunCapsule
	replays *ReplayService
	runner  *countingPreflightRunner
}

func newReplayPreflightFixture(t *testing.T, entrypoint types.Entrypoint) (context.Context, replayPreflightFixture) {
	t.Helper()
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	policy, err := NewPolicyEngine(config.Default(directory).Policy)
	if err != nil {
		t.Fatal(err)
	}
	capsules := NewCapsuleService(repo, policy)
	now := time.Unix(1, 0).UTC()
	capsule, err := capsules.Capture(ctx, CaptureRequest{
		TraceID: "00000000000000000000000000000001", ProjectID: "support", ServiceName: "support-agent", TerminalStatus: "ok",
		StartTime: now, EndTime: now.Add(time.Second), Provenance: types.Provenance{ProjectID: "support", Entrypoint: &entrypoint},
		Operations: []CaptureOperation{
			{Operation: types.Operation{SpanID: "root", Kind: "agent", Name: "answer", Status: "ok"}, Input: []byte(`{"question":"hello"}`), InputType: "application/json", Output: []byte(`{"answer":"world"}`), OutputType: "application/json"},
			{Operation: types.Operation{SpanID: "model", ParentSpanID: "root", Kind: "llm", Name: "model", Status: "ok"}, Input: []byte(`{"prompt":"hello"}`), InputType: "application/json", Output: []byte(`{"text":"world"}`), OutputType: "application/json", Boundary: &types.Boundary{ID: "llm-answer", LogicalKey: "llm.answer", Class: "llm", SideEffect: "read_only", ReplayPolicy: "live", MatchingRule: types.MatchingRule{Mode: "exact"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &countingPreflightRunner{}
	replays := NewReplayService(repo, capsules, nil, runner, NewDiffService(), nil, "")
	return ctx, replayPreflightFixture{repo: repo, capsule: capsule, replays: replays, runner: runner}
}

type countingPreflightRunner struct {
	available      int
	runs           int
	availableError error
}

func (r *countingPreflightRunner) Available(context.Context) error {
	r.available++
	return r.availableError
}

func (r *countingPreflightRunner) Run(context.Context, oci.RunSpec) (oci.RunResult, error) {
	r.runs++
	return oci.RunResult{}, nil
}
