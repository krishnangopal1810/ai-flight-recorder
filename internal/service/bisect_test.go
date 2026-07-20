package service

import (
	"context"
	"testing"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRT13BisectValidatesMonotonicHistory(t *testing.T) {
	revisions := []string{"a", "b", "c", "d", "e"}
	states := map[string]types.RevisionOutcome{"a": types.RevisionGood, "b": types.RevisionGood, "c": types.RevisionBad, "d": types.RevisionBad, "e": types.RevisionBad}
	result, err := NewBisectService().Search(context.Background(), revisions, false, func(_ context.Context, revision string) (types.RevisionEvaluation, error) {
		return types.RevisionEvaluation{Revision: revision, Outcome: states[revision], Evidence: []string{"evaluator:incident.prompt", "diff:first-divergence"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "first_bad" || result.FirstBad != "c" || len(result.Evaluations) != len(revisions) {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(result.Evaluations[2].Evidence) != 2 {
		t.Fatalf("first-bad evidence was not retained: %#v", result.Evaluations[2])
	}
}

func TestAFRT14BisectRefusesNonMonotonicClaim(t *testing.T) {
	revisions := []string{"a", "b", "c", "d", "e"}
	states := map[string]types.RevisionOutcome{"a": types.RevisionGood, "b": types.RevisionBad, "c": types.RevisionGood, "d": types.RevisionBad, "e": types.RevisionBad}
	result, err := NewBisectService().Search(context.Background(), revisions, false, func(_ context.Context, revision string) (types.RevisionEvaluation, error) {
		return types.RevisionEvaluation{Revision: revision, Outcome: states[revision]}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "non_monotonic" || !result.NonMonotonic || result.FirstBad != "" {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestAFRT14BisectReturnsInconclusiveForFlakyRevision(t *testing.T) {
	revisions := []string{"a", "b", "c", "d"}
	states := map[string]types.RevisionOutcome{"a": types.RevisionGood, "b": types.RevisionInconclusive, "c": types.RevisionBad, "d": types.RevisionBad}
	result, err := NewBisectService().Search(context.Background(), revisions, false, func(_ context.Context, revision string) (types.RevisionEvaluation, error) {
		return types.RevisionEvaluation{Revision: revision, Outcome: states[revision]}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "inconclusive" || result.FirstBad != "" {
		t.Fatalf("flaky history produced a false first bad: %#v", result)
	}
}

func TestConditionalBisectDoesNotClaimValidation(t *testing.T) {
	revisions := []string{"a", "b", "c", "d"}
	states := map[string]types.RevisionOutcome{"a": types.RevisionGood, "b": types.RevisionGood, "c": types.RevisionBad, "d": types.RevisionBad}
	result, err := NewBisectService().Search(context.Background(), revisions, true, func(_ context.Context, revision string) (types.RevisionEvaluation, error) {
		return types.RevisionEvaluation{Revision: revision, Outcome: states[revision]}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "conditional_on_monotonicity" || result.ConditionalOn != "monotonic_history" {
		t.Fatalf("unexpected result: %#v", result)
	}
}
