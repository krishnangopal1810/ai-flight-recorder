package service

import (
	"context"
	"errors"
	"testing"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRING003CompletenessReportPreservesBoundaryEvidenceAndAllModes(t *testing.T) {
	missing := types.MissingItem{Code: "boundary_response_missing", BoundaryID: "boundary_tool", Message: "recorded response is required"}
	ambiguous := types.AmbiguityItem{Code: "boundary_match_ambiguous", BoundaryID: "boundary_tool", Message: "duplicate logical identity"}
	capsule := types.RunCapsule{
		CapsuleID: "afr_fixture",
		Completeness: map[string]types.Completeness{
			"playback": {State: types.CompletenessReady},
			"replay":   {State: types.CompletenessBlocked, Missing: []types.MissingItem{missing}, AmbiguityDetails: []types.AmbiguityItem{ambiguous}},
			"diff":     {State: types.CompletenessReady},
			"bisect":   {State: types.CompletenessBlocked, Missing: []types.MissingItem{missing}},
		},
	}
	report := CompletenessReport(capsule)
	if report.SchemaVersion != types.SchemaVersion || report.CapsuleID != capsule.CapsuleID {
		t.Fatalf("report identity = %#v", report)
	}
	if report.Playback.State != types.CompletenessReady || report.Diff.State != types.CompletenessReady || report.Replay.State != types.CompletenessBlocked || report.Bisect.State != types.CompletenessBlocked {
		t.Fatalf("independent mode states = %#v", report)
	}
	if len(report.Replay.Missing) != 1 || report.Replay.Missing[0].BoundaryID != "boundary_tool" || report.Replay.Missing[0].Code != "boundary_response_missing" {
		t.Fatalf("boundary evidence = %#v", report.Replay.Missing)
	}
	if len(report.Replay.AmbiguityDetails) != 1 || report.Replay.AmbiguityDetails[0].BoundaryID != "boundary_tool" || report.Replay.AmbiguityDetails[0].Code != "boundary_match_ambiguous" {
		t.Fatalf("boundary ambiguity = %#v", report.Replay.AmbiguityDetails)
	}
}

func TestAFRING003DuplicateBoundaryIdentityBlocksReplayByBoundary(t *testing.T) {
	response := &types.PayloadRef{Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: types.PayloadStateCaptured}
	capsule := types.RunCapsule{
		Provenance: types.Provenance{CommitSHA: "revision", Entrypoint: &types.Entrypoint{Command: []string{"/agent"}}},
		Operations: []types.Operation{{SpanID: "root", Kind: "agent"}},
		Boundaries: []types.Boundary{
			{ID: "boundary_one", OperationSpanID: "root", LogicalKey: "tool.lookup", Class: "tool", Occurrence: 0, Response: response},
			{ID: "boundary_two", OperationSpanID: "root", LogicalKey: "tool.lookup", Class: "tool", Occurrence: 0, Response: response},
		},
	}
	ComputeCompleteness(&capsule)
	replay := capsule.Completeness["replay"]
	if replay.State != types.CompletenessBlocked || len(replay.AmbiguityDetails) != 2 || replay.AmbiguityDetails[0].BoundaryID != "boundary_one" || replay.AmbiguityDetails[1].BoundaryID != "boundary_two" {
		t.Fatalf("duplicate boundary completeness = %#v", replay)
	}
	if capsule.Completeness["playback"].State != types.CompletenessReady || capsule.Completeness["diff"].State != types.CompletenessReady {
		t.Fatalf("unaffected modes changed: %#v", capsule.Completeness)
	}
	capsule.Completeness["replay"] = types.Completeness{State: types.CompletenessReady}
	report := CompletenessReport(capsule)
	if report.Replay.State != types.CompletenessBlocked || len(report.Replay.AmbiguityDetails) != 2 {
		t.Fatalf("projected imported ambiguity did not fail closed: %#v", report.Replay)
	}
	err := (&ReplayService{}).Preflight(context.Background(), capsule, types.CreateReplayRequest{})
	var flightErr *types.FlightError
	if !errors.As(err, &flightErr) || flightErr.Code != types.ErrIncompleteCapsule {
		t.Fatalf("ambiguous replay reached execution preflight: %#v", err)
	}
	details, ok := flightErr.Details["ambiguity_details"].([]types.AmbiguityItem)
	if !ok || len(details) != 2 || details[0].BoundaryID != "boundary_one" {
		t.Fatalf("ambiguous preflight evidence = %#v", flightErr.Details)
	}
}

func TestAFRING003DistinctRequestDigestsDoNotCreateFalseAmbiguity(t *testing.T) {
	response := &types.PayloadRef{Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: types.PayloadStateCaptured}
	capsule := types.RunCapsule{
		Provenance: types.Provenance{CommitSHA: "revision", Entrypoint: &types.Entrypoint{Command: []string{"/agent"}}},
		Operations: []types.Operation{{SpanID: "root", Kind: "agent"}},
		Boundaries: []types.Boundary{
			{ID: "boundary_one", OperationSpanID: "root", LogicalKey: "tool.lookup", Class: "tool", Occurrence: 0, RequestDigest: "sha256:one", Response: response},
			{ID: "boundary_two", OperationSpanID: "root", LogicalKey: "tool.lookup", Class: "tool", Occurrence: 0, RequestDigest: "sha256:two", Response: response},
		},
	}
	ComputeCompleteness(&capsule)
	if replay := capsule.Completeness["replay"]; replay.State != types.CompletenessReady || len(replay.AmbiguityDetails) != 0 {
		t.Fatalf("request-digest-disambiguated boundaries were blocked: %#v", replay)
	}
}

func TestAFRING003AbsentModeFailsClosed(t *testing.T) {
	report := CompletenessReport(types.RunCapsule{CapsuleID: "afr_fixture", Completeness: map[string]types.Completeness{
		"playback": {State: types.CompletenessReady},
		"replay":   {State: types.CompletenessReady, Ambiguities: []string{"legacy ambiguity"}},
		"diff":     {State: "future_state"},
	}})
	if report.Bisect.State != types.CompletenessBlocked || len(report.Bisect.Missing) != 1 || report.Bisect.Missing[0].Code != "completeness_mode_missing" {
		t.Fatalf("missing bisect mode did not fail closed: %#v", report.Bisect)
	}
	if report.Replay.State != types.CompletenessBlocked || len(report.Replay.Ambiguities) != 1 {
		t.Fatalf("ambiguous ready replay did not fail closed: %#v", report.Replay)
	}
	if report.Diff.State != types.CompletenessIncomplete || len(report.Diff.Missing) != 1 || report.Diff.Missing[0].Code != "completeness_state_unsupported" {
		t.Fatalf("unsupported diff state did not fail closed: %#v", report.Diff)
	}
}
