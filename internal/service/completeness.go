package service

import "github.com/krishnangopal1810/ai-flight-recorder/internal/types"

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
		if operation.ParentSpanID == "" && payloadOmitted(operation.Input) {
			item := types.MissingItem{Code: "root_input_omitted", Message: "the replay entrypoint input was omitted: " + operation.Input.Reason}
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
	}
	for _, artifact := range capsule.Artifacts {
		if payloadOmitted(artifact.Payload) {
			item := types.MissingItem{Code: "artifact_payload_omitted", Message: "artifact " + artifact.ID + " was omitted: " + artifact.Payload.Reason}
			playback.State, playback.Missing = types.CompletenessIncomplete, append(playback.Missing, item)
			diff.State, diff.Missing = types.CompletenessIncomplete, append(diff.Missing, item)
		}
	}
	for _, boundary := range capsule.Boundaries {
		operation := operations[boundary.OperationSpanID]
		if payloadOmitted(operation.Input) {
			item := types.MissingItem{Code: "boundary_request_omitted", BoundaryID: boundary.ID, Message: "recorded request content was omitted and cannot be matched safely"}
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
	}
	capsule.Completeness = map[string]types.Completeness{"playback": playback, "replay": replay, "diff": diff, "bisect": bisect}
}

func payloadOmitted(ref *types.PayloadRef) bool {
	return ref != nil && ref.State == types.PayloadStateOmitted
}
