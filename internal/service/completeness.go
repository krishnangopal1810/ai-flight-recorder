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
	for _, boundary := range capsule.Boundaries {
		if boundary.Response == nil || boundary.Response.State == "omitted" {
			item := types.MissingItem{Code: "boundary_response_missing", BoundaryID: boundary.ID, Message: "recorded response is required for default substitution"}
			replay.State, replay.Missing = types.CompletenessBlocked, append(replay.Missing, item)
			bisect.State, bisect.Missing = types.CompletenessBlocked, append(bisect.Missing, item)
		}
	}
	capsule.Completeness = map[string]types.Completeness{"playback": playback, "replay": replay, "diff": diff, "bisect": bisect}
}
