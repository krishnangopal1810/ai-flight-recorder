package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/oci"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRT15EvaluatorRunsInProviderAndPersistsEvidence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(root, "flight.db"), filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, _ := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1 << 20})
	capsules := NewCapsuleService(repo, policy)
	baseline := evaluatorCapsule(t, "base", "old")
	candidate := evaluatorCapsule(t, "candidate", "new")
	if err := repo.PutCapsule(ctx, baseline, nil); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutCapsule(ctx, candidate, nil); err != nil {
		t.Fatal(err)
	}
	definition := types.EvaluatorDefinition{SchemaVersion: types.SchemaVersion, ID: "incident.prompt", Version: "1", Image: "sha256:" + strings.Repeat("a", 64), Command: []string{"/evaluate"}, Timeout: time.Minute}
	runner := &evaluatorRunner{outputs: [][]byte{
		[]byte(`{"passed":false,"score":0,"summary":"incident reproduced"}`),
		[]byte(`{"passed":true,"score":1,"summary":"fixed"}`),
	}}
	service := NewEvaluatorService(repo, capsules, NewDiffService(), runner, nil)
	sourceRun, err := service.Run(ctx, types.EvaluatorRunRequest{Definition: definition, BaselineID: baseline.CapsuleID, CandidateID: baseline.CapsuleID}, "gateway")
	if err != nil {
		t.Fatal(err)
	}
	if sourceRun.Outcome != "fail" || sourceRun.Output.Passed {
		t.Fatalf("source incident did not fail evaluator: %#v", sourceRun)
	}
	run, err := service.Run(ctx, types.EvaluatorRunRequest{Definition: definition, BaselineID: baseline.CapsuleID, CandidateID: candidate.CapsuleID}, "gateway")
	if err != nil {
		t.Fatal(err)
	}
	if run.Outcome != "pass" || !run.Output.Passed {
		t.Fatalf("unexpected evaluator result: %#v", run)
	}
	runs, err := repo.ListEvaluatorRuns(ctx, baseline.CapsuleID)
	if err != nil || len(runs) != 2 {
		t.Fatalf("persisted evidence: %#v %v", runs, err)
	}
}

func evaluatorCapsule(t *testing.T, id, name string) types.RunCapsule {
	t.Helper()
	capsule := types.RunCapsule{SchemaVersion: types.SchemaVersion, TraceID: id, ProjectID: "test", ServiceName: "agent", StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0), TerminalStatus: "ok", Privacy: types.PrivacySummary{Mode: "strict"}, Operations: []types.Operation{{SpanID: id, LogicalKey: "root", Kind: "agent", Name: name, Status: "ok"}}}
	ComputeCompleteness(&capsule)
	if err := FinalizeCapsule(&capsule, nil); err != nil {
		t.Fatal(err)
	}
	return capsule
}

type evaluatorRunner struct {
	outputs [][]byte
	next    int
}

func (*evaluatorRunner) Available(context.Context) error { return nil }
func (runner *evaluatorRunner) Evaluate(context.Context, oci.EvaluatorSpec) (oci.EvaluatorResult, error) {
	output := runner.outputs[runner.next]
	runner.next++
	return oci.EvaluatorResult{Stdout: output}, nil
}
