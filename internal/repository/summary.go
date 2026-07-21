package repository

import "github.com/krishnangopal1810/ai-flight-recorder/internal/types"

func summarizeCapsule(capsule types.RunCapsule) types.CapsuleSummary {
	failure := ""
	for index := len(capsule.Operations) - 1; index >= 0; index-- {
		operation := capsule.Operations[index]
		if operation.Status != "" && operation.Status != "ok" && operation.Status != "unset" {
			failure = operation.Name
			break
		}
	}
	replay := capsule.Completeness["replay"]
	playback := capsule.Completeness["playback"]
	warnings := len(playback.Missing) + len(playback.Ambiguities) + len(playback.AmbiguityDetails)
	duration := capsule.EndTime.Sub(capsule.StartTime).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	return types.CapsuleSummary{
		CapsuleID:            capsule.CapsuleID,
		SourceCapsuleID:      capsule.SourceCapsuleID,
		TraceID:              capsule.TraceID,
		ProjectID:            capsule.ProjectID,
		ServiceName:          capsule.ServiceName,
		StartTime:            capsule.StartTime,
		EndTime:              capsule.EndTime,
		DurationMillis:       duration,
		TerminalStatus:       capsule.TerminalStatus,
		FailureOperation:     failure,
		ReplayReadiness:      replay.State,
		EvidenceWarningCount: warnings,
		RequestedModel:       capsule.Model.RequestedModel,
		Completeness:         capsule.Completeness,
	}
}
