package service

import (
	"context"
	"fmt"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func (s *CapsuleService) Completeness(ctx context.Context, id string) (types.CapsuleCompleteness, error) {
	playback, err := s.Playback(ctx, id)
	if err != nil {
		return types.CapsuleCompleteness{}, err
	}
	return playback.Completeness, nil
}

func CompletenessReport(capsule types.RunCapsule) types.CapsuleCompleteness {
	report := types.CapsuleCompleteness{
		SchemaVersion: types.SchemaVersion,
		CapsuleID:     capsule.CapsuleID,
		Playback:      completenessMode(capsule.Completeness, "playback", false),
		Replay:        completenessMode(capsule.Completeness, "replay", true),
		Diff:          completenessMode(capsule.Completeness, "diff", false),
		Bisect:        completenessMode(capsule.Completeness, "bisect", true),
	}
	for _, item := range boundaryAmbiguities(capsule.Boundaries) {
		report.Replay.State = types.CompletenessBlocked
		report.Replay.AmbiguityDetails = appendUniqueAmbiguity(report.Replay.AmbiguityDetails, item)
		report.Bisect.State = types.CompletenessBlocked
		report.Bisect.AmbiguityDetails = appendUniqueAmbiguity(report.Bisect.AmbiguityDetails, item)
	}
	return report
}

func completenessMode(values map[string]types.Completeness, mode string, blocking bool) types.Completeness {
	value, present := values[mode]
	if present {
		switch value.State {
		case types.CompletenessReady, types.CompletenessIncomplete, types.CompletenessBlocked, types.CompletenessNotApplicable:
			return failClosedForIssues(value, blocking)
		}
	}
	code, message := "completeness_mode_missing", mode+" completeness was not recorded"
	if present {
		code, message = "completeness_state_unsupported", mode+" completeness has an unsupported state"
	}
	state := types.CompletenessIncomplete
	if blocking {
		state = types.CompletenessBlocked
	}
	return types.Completeness{
		State: state,
		Missing: []types.MissingItem{{
			Code:    code,
			Message: message,
		}},
	}
}

func failClosedForIssues(value types.Completeness, blocking bool) types.Completeness {
	if len(value.Missing) == 0 && len(value.Ambiguities) == 0 && len(value.AmbiguityDetails) == 0 {
		return value
	}
	if blocking {
		value.State = types.CompletenessBlocked
	} else if value.State == types.CompletenessReady || value.State == types.CompletenessNotApplicable {
		value.State = types.CompletenessIncomplete
	}
	return value
}

func ComputeCompleteness(capsule *types.RunCapsule) {
	playback := types.Completeness{State: types.CompletenessReady}
	diff := types.Completeness{State: types.CompletenessReady}
	replay := types.Completeness{State: types.CompletenessReady}
	bisect := types.Completeness{State: types.CompletenessReady}

	if len(capsule.Operations) == 0 {
		item := types.MissingItem{Code: "operations_missing", Message: "the trace contains no accepted operations"}
		playback.State, playback.Missing = types.CompletenessIncomplete, append(playback.Missing, item)
		diff.State, diff.Missing = types.CompletenessBlocked, append(diff.Missing, item)
	}
	if capsule.Provenance.Entrypoint == nil {
		item := types.MissingItem{Code: "entrypoint_missing", Message: "a registered replay entrypoint is required"}
		replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
		bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
	}
	if capsule.Provenance.CommitSHA == "" {
		item := types.MissingItem{Code: "revision_missing", Message: "a Git revision is required for bisect"}
		bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
	}
	operations := map[string]types.Operation{}
	for _, operation := range capsule.Operations {
		operations[operation.SpanID] = operation
		for _, content := range []struct {
			direction string
			ref       *types.PayloadRef
		}{{"input", operation.Input}, {"output", operation.Output}} {
			direction, ref := content.direction, content.ref
			if !payloadOmitted(ref) {
				continue
			}
			item := types.MissingItem{Code: "operation_" + direction + "_omitted", Message: "operation " + direction + " was omitted: " + ref.Reason}
			playback.State, playback.Missing = types.CompletenessIncomplete, append(playback.Missing, item)
			diff.State, diff.Missing = types.CompletenessIncomplete, append(diff.Missing, item)
		}
		for _, content := range []struct {
			direction string
			ref       *types.PayloadRef
		}{{"input", operation.Input}, {"output", operation.Output}} {
			if !payloadTruncated(content.ref) {
				continue
			}
			item := types.MissingItem{Code: "operation_" + content.direction + "_truncated", Message: "operation " + content.direction + " was truncated by capture policy"}
			playback.State, playback.Missing = types.CompletenessIncomplete, append(playback.Missing, item)
			diff.State, diff.Missing = types.CompletenessIncomplete, append(diff.Missing, item)
		}
		if operation.ParentSpanID == "" && payloadOmitted(operation.Input) {
			item := types.MissingItem{Code: "root_input_omitted", Message: "the replay entrypoint input was omitted: " + operation.Input.Reason}
			replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
			bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
		}
		if operation.ParentSpanID == "" && payloadTruncated(operation.Input) {
			item := types.MissingItem{Code: "root_input_truncated", Message: "the replay entrypoint input was truncated by capture policy"}
			replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
			bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
		}
	}
	for _, definition := range capsule.Model.ToolDefinitions {
		if payloadOmitted(&definition) {
			item := types.MissingItem{Code: "tool_definition_omitted", Message: "a model tool definition was omitted: " + definition.Reason}
			playback.State, playback.Missing = types.CompletenessIncomplete, append(playback.Missing, item)
			diff.State, diff.Missing = types.CompletenessIncomplete, append(diff.Missing, item)
			replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
			bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
		}
		if payloadTruncated(&definition) {
			item := types.MissingItem{Code: "tool_definition_truncated", Message: "a model tool definition was truncated by capture policy"}
			playback.State, playback.Missing = types.CompletenessIncomplete, append(playback.Missing, item)
			diff.State, diff.Missing = types.CompletenessIncomplete, append(diff.Missing, item)
			replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
			bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
		}
	}
	for _, artifact := range capsule.Artifacts {
		if payloadOmitted(artifact.Payload) {
			item := types.MissingItem{Code: "artifact_payload_omitted", Message: "artifact " + artifact.ID + " was omitted: " + artifact.Payload.Reason}
			playback.State, playback.Missing = types.CompletenessIncomplete, append(playback.Missing, item)
			diff.State, diff.Missing = types.CompletenessIncomplete, append(diff.Missing, item)
		}
		if payloadTruncated(artifact.Payload) {
			item := types.MissingItem{Code: "artifact_payload_truncated", Message: "artifact " + artifact.ID + " was truncated by capture policy"}
			playback.State, playback.Missing = types.CompletenessIncomplete, append(playback.Missing, item)
			diff.State, diff.Missing = types.CompletenessIncomplete, append(diff.Missing, item)
		}
	}
	for _, boundary := range capsule.Boundaries {
		operation := operations[boundary.OperationSpanID]
		if operation.Input == nil {
			item := types.MissingItem{Code: "boundary_request_missing", BoundaryID: boundary.ID, Message: "recorded request content is missing and cannot be matched safely"}
			replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
			bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
		} else if payloadOmitted(operation.Input) {
			item := types.MissingItem{Code: "boundary_request_omitted", BoundaryID: boundary.ID, Message: "recorded request content was omitted and cannot be matched safely"}
			replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
			bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
		}
		if payloadTruncated(operation.Input) {
			item := types.MissingItem{Code: "boundary_request_truncated", BoundaryID: boundary.ID, Message: "recorded request content was truncated and cannot be matched safely"}
			replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
			bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
		}
		if boundary.Response == nil || boundary.Response.State == types.PayloadStateOmitted {
			reason := "missing"
			if boundary.Response != nil && boundary.Response.Reason != "" {
				reason = boundary.Response.Reason
			}
			item := types.MissingItem{Code: "boundary_response_missing", BoundaryID: boundary.ID, Message: "recorded response is required for default substitution: " + reason}
			replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
			bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
		}
		if payloadTruncated(boundary.Response) {
			item := types.MissingItem{Code: "boundary_response_truncated", BoundaryID: boundary.ID, Message: "recorded response was truncated and cannot be substituted safely"}
			replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
			bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
		}
	}
	for _, item := range boundaryAmbiguities(capsule.Boundaries) {
		replay.State, replay.AmbiguityDetails = types.CompletenessBlocked, append(replay.AmbiguityDetails, item)
		bisect.State, bisect.AmbiguityDetails = types.CompletenessBlocked, append(bisect.AmbiguityDetails, item)
	}
	capsule.Completeness = map[string]types.Completeness{"playback": playback, "replay": replay, "diff": diff, "bisect": bisect}
}

func boundaryAmbiguities(boundaries []types.Boundary) []types.AmbiguityItem {
	identities := map[string]int{}
	for _, boundary := range boundaries {
		identity := boundary.ParentLogicalKey + "\x00" + boundary.Class + "\x00" + boundary.LogicalKey + "\x00" + fmt.Sprint(boundary.Occurrence) + "\x00" + boundary.RequestDigest
		identities[identity]++
	}
	result := []types.AmbiguityItem{}
	for _, boundary := range boundaries {
		identity := boundary.ParentLogicalKey + "\x00" + boundary.Class + "\x00" + boundary.LogicalKey + "\x00" + fmt.Sprint(boundary.Occurrence) + "\x00" + boundary.RequestDigest
		if identities[identity] > 1 {
			result = append(result, types.AmbiguityItem{Code: "boundary_match_ambiguous", BoundaryID: boundary.ID, Message: "multiple recorded boundaries share the same logical match identity"})
		}
	}
	return result
}

func appendUniqueAmbiguity(values []types.AmbiguityItem, candidate types.AmbiguityItem) []types.AmbiguityItem {
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func payloadOmitted(ref *types.PayloadRef) bool {
	return ref != nil && ref.State == types.PayloadStateOmitted
}

func payloadTruncated(ref *types.PayloadRef) bool {
	return ref != nil && ref.State == types.PayloadStateCaptured && ref.Reason == types.PayloadTruncated
}
