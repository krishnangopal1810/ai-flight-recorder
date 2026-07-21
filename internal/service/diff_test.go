package service

import (
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestDiffFindsFirstMeaningfulDivergenceAcrossInsertedSpan(t *testing.T) {
	now := time.Unix(1, 0).UTC()
	baseline := types.RunCapsule{CapsuleID: "base", Operations: []types.Operation{{SpanID: "a", LogicalKey: "agent", Kind: "agent", Name: "run", Status: "ok", Sequence: 0, StartTime: now}, {SpanID: "b", ParentSpanID: "a", LogicalKey: "tool", Kind: "tool", Name: "lookup", Status: "ok", Sequence: 1, StartTime: now.Add(time.Second)}}}
	candidate := types.RunCapsule{CapsuleID: "candidate", Operations: []types.Operation{{SpanID: "x", LogicalKey: "agent", Kind: "agent", Name: "run", Status: "ok", Sequence: 0, StartTime: now}, {SpanID: "noise", ParentSpanID: "x", Kind: "internal", Name: "instrumentation", Status: "ok", Sequence: 1, StartTime: now.Add(time.Second)}, {SpanID: "y", ParentSpanID: "x", LogicalKey: "tool", Kind: "tool", Name: "changed-lookup", Status: "error", Sequence: 2, StartTime: now.Add(2 * time.Second)}}}
	result := NewDiffService().Compare(baseline, candidate)
	if !result.FirstDivergenceKnown || result.FirstDivergence == nil {
		t.Fatalf("expected known divergence: %#v", result)
	}
	if result.FirstDivergence.Kind != "operation_inserted" && result.FirstDivergence.Path != "operations.b.name" {
		t.Fatalf("unexpected first divergence: %#v", result.FirstDivergence)
	}
	foundToolAlignment := false
	for _, alignment := range result.Alignments {
		if alignment.BaselineSpan == "b" && alignment.CandidateSpan == "y" {
			foundToolAlignment = true
		}
	}
	if !foundToolAlignment {
		t.Fatalf("logical tool operation was not aligned: %#v", result.Alignments)
	}
}

func TestAFRT11InternalInstrumentationDoesNotBecomeDivergence(t *testing.T) {
	baseline := types.RunCapsule{CapsuleID: "base", Operations: []types.Operation{{SpanID: "a", LogicalKey: "agent", Kind: "agent", Name: "answer", Status: "ok", Sequence: 0}, {SpanID: "b", ParentSpanID: "a", LogicalKey: "tool", Kind: "tool", Name: "lookup", Status: "ok", Sequence: 1}}}
	candidate := types.RunCapsule{CapsuleID: "candidate", Operations: []types.Operation{{SpanID: "x", LogicalKey: "agent", Kind: "agent", Name: "answer", Status: "ok", Sequence: 0}, {SpanID: "noise", ParentSpanID: "x", Kind: "internal", Name: "extra instrumentation", Status: "ok", Sequence: 1}, {SpanID: "y", ParentSpanID: "x", LogicalKey: "tool", Kind: "tool", Name: "lookup", Status: "ok", Sequence: 2}}}
	result := NewDiffService().Compare(baseline, candidate)
	if len(result.Observations) != 0 {
		t.Fatalf("internal span was treated as meaningful: %#v", result.Observations)
	}
}

func TestDiffReportsChangedChildInputBeforeParentOutcome(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	baseInput := &types.PayloadRef{Digest: "sha256:base"}
	candidateInput := &types.PayloadRef{Digest: "sha256:candidate"}
	baseline := types.RunCapsule{CapsuleID: "base", Operations: []types.Operation{
		{SpanID: "root", LogicalKey: "agent", Kind: "agent", Name: "run", Status: "error", Sequence: 0, StartTime: start, EndTime: start.Add(3 * time.Second)},
		{SpanID: "model", ParentSpanID: "root", LogicalKey: "model", Kind: "llm", Name: "decide", Status: "error", Sequence: 1, StartTime: start.Add(time.Second), EndTime: start.Add(2 * time.Second), Input: baseInput},
	}}
	candidate := types.RunCapsule{CapsuleID: "candidate", Operations: []types.Operation{
		{SpanID: "other-root", LogicalKey: "agent", Kind: "agent", Name: "run", Status: "ok", Sequence: 0, StartTime: start, EndTime: start.Add(3 * time.Second)},
		{SpanID: "other-model", ParentSpanID: "other-root", LogicalKey: "model", Kind: "llm", Name: "decide", Status: "ok", Sequence: 1, StartTime: start.Add(time.Second), EndTime: start.Add(2 * time.Second), Input: candidateInput},
	}}
	result := NewDiffService().Compare(baseline, candidate)
	if result.FirstDivergence == nil || result.FirstDivergence.Path != "operations.model.input_digest" {
		t.Fatalf("expected the changed model input before downstream outcome changes: %#v", result.FirstDivergence)
	}
	if result.FirstDivergence.OperationName != "decide" || result.FirstDivergence.Field != "input_digest" {
		t.Fatalf("expected a human-readable typed observation: %#v", result.FirstDivergence)
	}
	if len(result.FirstDivergence.Evidence) != 2 || result.FirstDivergence.Evidence[0].Payload != baseInput.Digest || result.FirstDivergence.Evidence[1].Payload != candidateInput.Digest {
		t.Fatalf("expected exact before/after payload references: %#v", result.FirstDivergence.Evidence)
	}
	if len(result.Hypotheses) != 0 {
		t.Fatalf("unvalidated causal speculation should not be generated: %#v", result.Hypotheses)
	}
}
