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
