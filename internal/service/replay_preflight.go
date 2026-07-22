package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func (s *ReplayService) Check(ctx context.Context, request types.CreateReplayRequest) (types.ReplayPreflight, error) {
	report := types.ReplayPreflight{State: types.ReplayPreflightBlocked, Issues: []types.ReplayPreflightIssue{}, Effective: types.ReplayPreflightEffective{Model: request.Model, Substitutions: []string{}, LiveBoundaries: []string{}, AllowedHosts: []string{}, EnvironmentNames: []string{}}}
	source, err := s.capsules.Get(ctx, request.SourceCapsuleID, true)
	if err != nil {
		return report, err
	}
	if source.Provenance.Entrypoint != nil {
		report.Effective.CandidateImage = source.Provenance.Entrypoint.Image
		report.Effective.AllowedHosts = append([]string(nil), source.Provenance.Entrypoint.ModelHosts...)
		report.Effective.EnvironmentNames = append([]string(nil), source.Provenance.Entrypoint.Environment...)
	}
	if request.CandidateImage != "" {
		report.Effective.CandidateImage = request.CandidateImage
	}
	prepared, err := s.prepareReplay(ctx, request)
	if err != nil {
		var flightErr *types.FlightError
		if errors.As(err, &flightErr) && (flightErr.Code == types.ErrStorage || flightErr.Code == types.ErrNotFound || flightErr.Code == types.ErrAuthentication) {
			return report, err
		}
		report.Issues = preflightIssues(err)
		return report, nil
	}
	report.State = types.ReplayPreflightReady
	report.Effective = types.ReplayPreflightEffective{
		CandidateImage: prepared.plan.Entrypoint.Image, Substitutions: prepared.substitutions, LiveBoundaries: prepared.live,
		AllowedHosts: prepared.allowedHosts, EnvironmentNames: prepared.plan.Environment, Model: prepared.plan.Model,
	}
	return report, nil
}

func (s *ReplayService) Preflight(ctx context.Context, source types.RunCapsule, request types.CreateReplayRequest) error {
	completeness := CompletenessReport(source).Replay
	if completeness.State != types.CompletenessReady {
		details := map[string]any{}
		if len(completeness.Missing) > 0 {
			details["missing"] = completeness.Missing
		}
		if len(completeness.Ambiguities) > 0 {
			details["ambiguities"] = completeness.Ambiguities
		}
		if len(completeness.AmbiguityDetails) > 0 {
			details["ambiguity_details"] = completeness.AmbiguityDetails
		}
		return &types.FlightError{Code: types.ErrIncompleteCapsule, Message: "capsule is not replay-ready", Remediation: "resolve the named missing or ambiguous entrypoint, artifact, or boundary prerequisites", Details: details}
	}
	if source.Provenance.Entrypoint == nil || len(source.Provenance.Entrypoint.Command) == 0 {
		return preflightError("entrypoint_missing", types.ErrIncompleteCapsule, "replay entrypoint is missing", "register an argv entrypoint in the source execution", nil)
	}
	if (request.Revision != "" || request.CandidateCommit != "") && request.CandidateImage == "" {
		return preflightError("revision_image_missing", types.ErrBuild, "a Git revision was provided without a resolved image", "build the revision with the CLI and submit its immutable candidate image", nil)
	}
	if source.Provenance.Entrypoint.Image == "" && request.CandidateImage == "" {
		return preflightError("entrypoint_image_missing", types.ErrBuild, "replay image is missing", "record with --replay-ready or provide a different immutable candidate image", nil)
	}
	if request.CandidateImage == "" && !immutableImage(source.Provenance.Entrypoint.Image) {
		return preflightError("entrypoint_image_mutable", types.ErrBuild, "recorded replay image is not content-addressed", "record with --replay-ready or provide a different immutable candidate image", nil)
	}
	if request.CandidateImage != "" && !immutableImage(request.CandidateImage) {
		return preflightError("candidate_image_mutable", types.ErrBuild, "candidate image is not content-addressed", "build the candidate and provide its sha256 image ID or repository digest", nil)
	}
	if request.Repetitions > 50 || request.PassThreshold < 0 || request.PassThreshold > 1 {
		return preflightError("trial_settings_invalid", types.ErrValidation, "replay trial settings are invalid", "use at most 50 repetitions and a pass threshold from 0 to 1", nil)
	}
	if err := validateReplayOverrides(source, request); err != nil {
		return err
	}
	liveModel := replayUsesLiveModel(source, request)
	if request.Model != "" {
		if !source.Provenance.Entrypoint.ModelOverride {
			return preflightError("model_override_not_declared", types.ErrUnsafeReplay, "this agent did not declare model override support", "set model_override: true, read FLIGHT_REPLAY_MODEL, and record the run again", nil)
		}
		if request.SubstituteLLM || !liveModel {
			return preflightError("model_override_requires_live_boundary", types.ErrValidation, "a model override has no declared live model boundary", "keep a declared LLM boundary live for the model experiment", nil)
		}
	}
	if !request.SubstituteLLM && !liveModel {
		return preflightError("live_model_boundary_missing", types.ErrUnsafeReplay, "live model execution was requested but no declared LLM boundary can run live", "declare the model boundary with replay_policy: live or substitute recorded model responses", nil)
	}
	if liveModel {
		if len(source.Provenance.Entrypoint.ModelHosts) == 0 {
			return preflightError("model_host_missing", types.ErrUnsafeReplay, "live model execution has no declared destination", "add the model hostname to flight.yaml and record the run again", nil)
		}
		for _, host := range source.Provenance.Entrypoint.ModelHosts {
			if !validModelHost(host) {
				return preflightError("model_host_invalid", types.ErrUnsafeReplay, "a declared model destination is not a normalized hostname", "use a lowercase hostname without a scheme, port, path, or wildcard and record again", nil)
			}
		}
		if len(source.Provenance.Entrypoint.Environment) == 0 {
			return preflightError("model_credential_missing", types.ErrUnsafeReplay, "live model execution has no declared credential name", "declare the model credential environment name in flight.yaml and record again", nil)
		}
	}
	responseOverrides := map[string]bool{}
	for _, override := range request.Overrides {
		responseOverrides[override.BoundaryID] = override.Target == "response"
	}
	for _, boundary := range source.Boundaries {
		if responseOverrides[boundary.ID] {
			continue
		}
		if boundary.ReplayPolicy == "live" && (boundary.SideEffect == "mutating" || boundary.SideEffect == "unknown") {
			if !request.AllowSandboxedSideEffects || boundary.Sandbox == "" {
				return preflightError("unsafe_live_boundary", types.ErrUnsafeReplay, "a live boundary is mutating or unknown", "substitute it, or declare a sandbox and pass --allow-sandboxed-side-effects", nil)
			}
			if source.Provenance.Entrypoint.Sandboxes[boundary.Sandbox] == "" {
				return preflightError("sandbox_destination_missing", types.ErrUnsafeReplay, "declared boundary sandbox has no isolated destination", "add the sandbox destination to flight.yaml", nil)
			}
		}
	}
	if _, err := s.environmentNames(source.Provenance.Entrypoint.Environment, request.Environment, replayUsesLiveBoundary(source, request)); err != nil {
		return err
	}
	if s.runner == nil {
		return preflightError("oci_runtime_unavailable", types.ErrBuild, "the replay runtime is unavailable", "configure a supported OCI runtime on the replay worker", nil)
	}
	if err := s.runner.Available(ctx); err != nil {
		return preflightError("oci_runtime_unavailable", types.ErrBuild, "the replay runtime is unavailable", "start or configure the worker OCI runtime", err)
	}
	return nil
}

func (s *ReplayService) environmentNames(declared, requested []string, needed bool) ([]string, error) {
	allowed := map[string]bool{}
	for _, name := range declared {
		allowed[name] = true
	}
	if len(requested) == 0 && needed {
		requested = append([]string(nil), declared...)
	}
	if len(requested) > 0 && !needed {
		return nil, preflightError("replay_credentials_unnecessary", types.ErrUnsafeReplay, "credentials were requested for an all-substituted replay", "remove replay environment names unless a declared boundary will run live", nil)
	}
	for _, name := range requested {
		if !allowed[name] {
			return nil, preflightError("replay_credential_undeclared", types.ErrUnsafeReplay, "replay credential was not declared by the entrypoint", "add the environment name to flight.yaml and record again", nil)
		}
		if _, ok := s.credentials[name]; !ok {
			return nil, preflightError("replay_credential_unavailable", types.ErrUnsafeReplay, "declared replay credential is unavailable", "set FLIGHT_REPLAY_CREDENTIAL_"+name+" on the worker", nil)
		}
	}
	return uniqueStrings(requested), nil
}

func replayUsesLiveModel(source types.RunCapsule, request types.CreateReplayRequest) bool {
	overrides := map[string]types.ReplayOverride{}
	for _, override := range request.Overrides {
		overrides[override.BoundaryID] = override
	}
	for _, boundary := range source.Boundaries {
		if boundary.Class != "llm" {
			continue
		}
		if override, ok := overrides[boundary.ID]; ok {
			if override.Target == "request" {
				return true
			}
			continue
		}
		if boundary.ReplayPolicy == "live" && !request.SubstituteLLM {
			return true
		}
	}
	return false
}

func replayUsesLiveBoundary(source types.RunCapsule, request types.CreateReplayRequest) bool {
	overrides := map[string]types.ReplayOverride{}
	for _, override := range request.Overrides {
		overrides[override.BoundaryID] = override
	}
	for _, boundary := range source.Boundaries {
		if override, ok := overrides[boundary.ID]; ok {
			if override.Target == "request" {
				return true
			}
			continue
		}
		if boundary.ReplayPolicy == "live" && (boundary.Class != "llm" || !request.SubstituteLLM) {
			return true
		}
	}
	return false
}

func validModelHost(host string) bool {
	if host == "" || host != strings.ToLower(host) || strings.ContainsAny(host, ":/@* ") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func preflightError(issueCode string, code types.ErrorCode, message, remediation string, cause error) *types.FlightError {
	err := types.NewError(code, message, remediation, cause)
	err.Details = map[string]any{"issue_code": issueCode}
	return err
}

func preflightBoundaryError(issueCode string, code types.ErrorCode, message, remediation, boundaryID string, cause error) *types.FlightError {
	err := preflightError(issueCode, code, message, remediation, cause)
	if boundaryID != "" {
		err.Details["boundary_id"] = boundaryID
	}
	return err
}

func preflightIssues(err error) []types.ReplayPreflightIssue {
	var flightErr *types.FlightError
	if !errors.As(err, &flightErr) {
		return []types.ReplayPreflightIssue{{Code: "preflight_failed", Message: "replay preflight failed", Remediation: "inspect service diagnostics and retry"}}
	}
	issues := []types.ReplayPreflightIssue{}
	if missing, ok := flightErr.Details["missing"].([]types.MissingItem); ok {
		for _, item := range missing {
			issues = append(issues, types.ReplayPreflightIssue{Code: item.Code, Message: item.Message, Remediation: flightErr.Remediation, BoundaryID: item.BoundaryID})
		}
	}
	if ambiguity, ok := flightErr.Details["ambiguity_details"].([]types.AmbiguityItem); ok {
		for _, item := range ambiguity {
			issues = append(issues, types.ReplayPreflightIssue{Code: item.Code, Message: item.Message, Remediation: flightErr.Remediation, BoundaryID: item.BoundaryID})
		}
	}
	if len(issues) > 0 {
		return issues
	}
	issueCode := string(flightErr.Code)
	if typed, ok := flightErr.Details["issue_code"].(string); ok && typed != "" {
		issueCode = typed
	}
	boundaryID, _ := flightErr.Details["boundary_id"].(string)
	return []types.ReplayPreflightIssue{{Code: issueCode, Message: flightErr.Message, Remediation: flightErr.Remediation, BoundaryID: boundaryID}}
}

func validateReplayOverrides(source types.RunCapsule, request types.CreateReplayRequest) error {
	if len(request.Overrides) > 1 {
		return preflightError("replay_override_count_invalid", types.ErrValidation, "a replay may change only one recorded value", "create separate controlled replays for separate changes", nil)
	}
	for _, override := range request.Overrides {
		if override.BoundaryID == "" || (override.Target != "request" && override.Target != "response") {
			return preflightBoundaryError("replay_override_target_invalid", types.ErrValidation, "replay override target is invalid", "choose a recorded boundary input or output", override.BoundaryID, nil)
		}
		if len(override.Value) == 0 || len(override.Value) > 1<<20 || !json.Valid(override.Value) {
			return preflightBoundaryError("replay_override_value_invalid", types.ErrValidation, "replay override must be one JSON value under 1 MiB", "enter valid JSON or attach larger data as an artifact", override.BoundaryID, nil)
		}
		var boundary *types.Boundary
		for index := range source.Boundaries {
			if source.Boundaries[index].ID == override.BoundaryID {
				boundary = &source.Boundaries[index]
				break
			}
		}
		if boundary == nil {
			return preflightBoundaryError("replay_override_boundary_missing", types.ErrUnmatchedBoundary, "the selected replay boundary does not exist", "choose a boundary from the recorded execution", override.BoundaryID, nil)
		}
		if override.Target == "request" && boundary.Class != "llm" {
			return preflightBoundaryError("replay_override_request_class_invalid", types.ErrValidation, "only model input overrides are supported", "change a tool or model output, or select a model input", override.BoundaryID, nil)
		}
		if override.Target == "request" && request.SubstituteLLM {
			return preflightBoundaryError("replay_override_requires_live_model", types.ErrValidation, "a changed model input requires a declared live model call", "keep the selected model boundary live for this replay", override.BoundaryID, nil)
		}
	}
	return nil
}
