package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/oci"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type CandidateCapture interface {
	Capture(context.Context, *collectortrace.ExportTraceServiceRequest) ([]types.RunCapsule, int, error)
}

type ReplayService struct {
	repository   repository.Repository
	capsules     *CapsuleService
	receiver     CandidateCapture
	runner       oci.Runner
	diffs        *DiffService
	credentials  map[string]string
	gatewayImage string
	clock        func() time.Time
}

func NewReplayService(repo repository.Repository, capsules *CapsuleService, receiver CandidateCapture, runner oci.Runner, diffs *DiffService, credentials map[string]string, gatewayImage string) *ReplayService {
	return &ReplayService{repository: repo, capsules: capsules, receiver: receiver, runner: runner, diffs: diffs, credentials: credentials, gatewayImage: defaultGatewayImage(gatewayImage), clock: func() time.Time { return time.Now().UTC() }}
}

func (s *ReplayService) Create(ctx context.Context, request types.CreateReplayRequest) (types.Job, error) {
	source, err := s.capsules.Get(ctx, request.SourceCapsuleID, true)
	if err != nil {
		return types.Job{}, err
	}
	for index := range request.Overrides {
		prepared, prepareErr := s.capsules.PrepareReplayOverride(request.Overrides[index].Value)
		if prepareErr != nil {
			return types.Job{}, prepareErr
		}
		request.Overrides[index].Value = prepared
	}
	if err := s.Preflight(ctx, source, request); err != nil {
		return types.Job{}, err
	}
	revision := request.CandidateCommit
	if revision == "" {
		revision = request.Revision
	}
	entrypoint := *source.Provenance.Entrypoint
	if request.CandidateImage != "" {
		entrypoint.Image = request.CandidateImage
	}
	environment, environmentErr := s.environmentNames(entrypoint.Environment, request.Environment)
	if environmentErr != nil {
		return types.Job{}, environmentErr
	}
	plan := types.ReplayPlan{SchemaVersion: types.SchemaVersion, SourceCapsuleID: source.CapsuleID, Model: request.Model, Revision: revision, CandidateDirtyDigest: request.CandidateDirtyDigest, CandidateSourceDigest: request.CandidateSourceDigest, Entrypoint: entrypoint, SubstituteLLM: request.SubstituteLLM, AllowSandboxedSideEffects: request.AllowSandboxedSideEffects, Repetitions: request.Repetitions, PassThreshold: request.PassThreshold, Environment: environment, Overrides: request.Overrides, MatchingRules: map[string]types.MatchingRule{}}
	if plan.Repetitions <= 0 {
		plan.Repetitions = 1
	}
	if plan.PassThreshold <= 0 {
		plan.PassThreshold = 1
	}
	plan.OriginalInput = replayRootInput(source.Operations)
	for _, artifact := range source.Artifacts {
		if artifact.State == "captured" {
			plan.ExpectedArtifacts = append(plan.ExpectedArtifacts, artifact.ID)
		}
	}
	canonical, err := CanonicalJSON(plan)
	if err != nil {
		return types.Job{}, err
	}
	plan.PlanDigest = DigestBytes(canonical)
	envelope, substitutions, live, allowedHosts, err := s.envelope(ctx, source, plan)
	if err != nil {
		return types.Job{}, err
	}
	jobRequest := types.ReplayJobRequest{Plan: plan, Envelope: envelope, GatewayImage: s.gatewayImage, AllowedHosts: allowedHosts}
	encoded, _ := json.Marshal(jobRequest)
	now := s.clock()
	job := types.Job{ID: uuid.NewString(), Kind: "replay", State: types.JobQueued, Request: encoded, CreatedAt: now, UpdatedAt: now}
	if err := s.repository.PutJob(ctx, job); err != nil {
		return types.Job{}, err
	}
	event, _ := json.Marshal(map[string]any{"plan_digest": plan.PlanDigest, "substitutions": substitutions, "live_boundaries": live, "overrides": plan.Overrides})
	_ = s.repository.AppendJobEvent(ctx, types.JobEvent{JobID: job.ID, Type: "queued", Data: event, Time: now})
	s.appendStage(ctx, job.ID, "preflight", "completed", "Integrity, entrypoint, isolation, boundaries, destinations, credentials, and side-effect policy passed.")
	return job, nil
}

func replayRootInput(operations []types.Operation) *types.PayloadRef {
	for index := range operations {
		if operations[index].ParentSpanID == "" {
			return operations[index].Input
		}
	}
	return nil
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
		return types.NewError(types.ErrIncompleteCapsule, "replay entrypoint is missing", "register an argv entrypoint in the source execution", nil)
	}
	if source.Provenance.Entrypoint.Image == "" && request.CandidateImage == "" {
		return types.NewError(types.ErrBuild, "replay image is missing", "record an immutable OCI image reference in the entrypoint", nil)
	}
	if request.CandidateImage == "" && !immutableImage(source.Provenance.Entrypoint.Image) {
		return types.NewError(types.ErrBuild, "recorded replay image is not content-addressed", "use flight.yaml to build the candidate or record an image digest", nil)
	}
	if request.CandidateImage != "" && !strings.HasPrefix(request.CandidateImage, "sha256:") {
		return types.NewError(types.ErrBuild, "candidate image is not content-addressed", "build the candidate and provide its sha256 image ID", nil)
	}
	if request.Repetitions > 50 || request.PassThreshold < 0 || request.PassThreshold > 1 {
		return types.NewError(types.ErrValidation, "replay trial settings are invalid", "use at most 50 repetitions and a pass threshold from 0 to 1", nil)
	}
	if err := validateReplayOverrides(source, request); err != nil {
		return err
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
				return types.NewError(types.ErrUnsafeReplay, "a live boundary is mutating or unknown", "substitute it, or declare a sandbox and pass --allow-sandboxed-side-effects", nil)
			}
			if source.Provenance.Entrypoint.Sandboxes[boundary.Sandbox] == "" {
				return types.NewError(types.ErrUnsafeReplay, "declared boundary sandbox has no isolated destination", "add the sandbox destination to flight.yaml", nil)
			}
		}
	}
	return s.runner.Available(ctx)
}

func (s *ReplayService) RunClaimed(ctx context.Context, job types.Job) error {
	s.appendStage(ctx, job.ID, "preparing_candidate", "running", "Loading the immutable replay plan and recorded input.")
	var request types.ReplayJobRequest
	if err := json.Unmarshal(job.Request, &request); err != nil {
		return s.failJob(ctx, &job, types.NewError(types.ErrValidation, "stored replay job is invalid", "recreate the replay job", err))
	}
	input := []byte("{}")
	if request.Plan.OriginalInput != nil && request.Plan.OriginalInput.Digest != "" {
		loaded, err := s.repository.LoadPayload(ctx, request.Plan.OriginalInput.Digest)
		if err != nil {
			return s.failJob(ctx, &job, err)
		}
		input = loaded
	}
	resourceAttributes := []string{"flight.source_capsule_id=" + request.Plan.SourceCapsuleID, "flight.entrypoint.image=" + request.Plan.Entrypoint.Image}
	if request.Plan.Revision != "" {
		resourceAttributes = append(resourceAttributes, "flight.git.commit="+request.Plan.Revision)
	}
	if request.Plan.CandidateDirtyDigest != "" {
		resourceAttributes = append(resourceAttributes, "flight.git.dirty_digest="+request.Plan.CandidateDirtyDigest)
	}
	if request.Plan.CandidateSourceDigest != "" {
		resourceAttributes = append(resourceAttributes, "flight.source.digest="+request.Plan.CandidateSourceDigest)
	}
	environment := map[string]string{"OTEL_RESOURCE_ATTRIBUTES": strings.Join(resourceAttributes, ",")}
	if request.Plan.Model != "" {
		environment["FLIGHT_REPLAY_MODEL"] = request.Plan.Model
	}
	for _, name := range request.Plan.Environment {
		environment[name] = s.credentials[name]
	}
	source, err := s.capsules.Get(ctx, request.Plan.SourceCapsuleID, true)
	if err != nil {
		return s.failJob(ctx, &job, err)
	}
	s.appendStage(ctx, job.ID, "preparing_candidate", "completed", "Candidate input and declared replay environment are ready.")
	s.appendStage(ctx, job.ID, "building_image", "skipped", "Using the prebuilt immutable candidate image from the replay plan.")
	repetitions := request.Plan.Repetitions
	if repetitions <= 0 {
		repetitions = 1
	}
	jobResult := types.ReplayJobResult{SourceCapsuleID: request.Plan.SourceCapsuleID, CandidateImage: request.Plan.Entrypoint.Image, CandidateSource: request.Plan.CandidateSourceDigest, Overrides: request.Plan.Overrides}
	exact := 0
	overrideFailed := false
	for index := range repetitions {
		s.appendStage(ctx, job.ID, "starting_isolated_application", "running", "Starting the non-root candidate on a private OCI network.")
		s.appendStage(ctx, job.ID, "replaying_recorded_dependencies", "running", "Recorded boundaries will be substituted and undeclared calls will fail closed.")
		result, runErr := s.runner.Run(ctx, oci.RunSpec{JobID: fmt.Sprintf("%s-%d", job.ID, index), Image: request.Plan.Entrypoint.Image, GatewayImage: request.GatewayImage, Command: request.Plan.Entrypoint.Command, WorkingDir: request.Plan.Entrypoint.WorkingDir, Input: input, ReplayEnvelope: request.Envelope, Environment: environment, AllowedHosts: request.AllowedHosts, Timeout: request.Plan.Entrypoint.Timeout, CPULimit: request.Plan.Entrypoint.CPULimit, MemoryLimitMB: request.Plan.Entrypoint.MemoryLimitMB})
		if runErr != nil {
			return s.failJob(ctx, &job, runErr)
		}
		s.appendStage(ctx, job.ID, "starting_isolated_application", "completed", "The isolated candidate exited and no host resources were exposed.")
		s.appendStage(ctx, job.ID, "replaying_recorded_dependencies", "completed", "Boundary substitutions and declared live destinations completed under gateway policy.")
		s.appendStage(ctx, job.ID, "capturing_candidate", "running", "Verifying and storing candidate telemetry.")
		candidates, captureErr := s.captureCandidates(ctx, result)
		if captureErr != nil {
			return s.failJob(ctx, &job, captureErr)
		}
		if len(candidates) == 0 {
			return s.failJob(ctx, &job, types.NewError(types.ErrIncompleteCapsule, "replay produced no candidate capsule", "ensure the candidate exports an opted-in root trace to OTEL_EXPORTER_OTLP_ENDPOINT", nil))
		}
		overrideResults := verifyReplayOverrides(source, candidates[0], request.Plan.Overrides)
		if index == 0 {
			jobResult.OverrideResults = overrideResults
		}
		for _, overrideResult := range overrideResults {
			if !overrideResult.Applied {
				overrideFailed = true
			}
		}
		s.appendStage(ctx, job.ID, "capturing_candidate", "completed", "Candidate evidence was finalized as an immutable recorded run.")
		s.appendStage(ctx, job.ID, "comparing_executions", "running", "Aligning the baseline and candidate logical execution paths.")
		outcome := "divergent"
		if comparison := s.diffs.Compare(source, candidates[0]); executionEquivalent(comparison) {
			outcome, exact = "exact", exact+1
		}
		s.appendStage(ctx, job.ID, "comparing_executions", "completed", "The candidate comparison is ready for review.")
		trial := types.ReplayTrial{Index: index, CandidateCapsuleID: candidates[0].CapsuleID, ExitCode: result.ExitCode, DurationMillis: result.Duration.Milliseconds(), Outcome: outcome}
		jobResult.Trials = append(jobResult.Trials, trial)
		if index == 0 {
			jobResult.CandidateCapsuleID = candidates[0].CapsuleID
		}
		jobResult.ExitCode, jobResult.DurationMillis = result.ExitCode, jobResult.DurationMillis+result.Duration.Milliseconds()
		trialJSON, _ := json.Marshal(trial)
		_ = s.repository.AppendJobEvent(ctx, types.JobEvent{JobID: job.ID, Type: "trial_completed", Data: trialJSON, Time: s.clock()})
		if result.ExitCode != 0 || overrideFailed {
			break
		}
	}
	jobResult.Outcome = classifyReplayOutcome(exact, repetitions, request.Plan.PassThreshold)
	var envelope struct {
		Substitutions  []string `json:"substitutions"`
		LiveBoundaries []string `json:"live_boundaries"`
	}
	_ = json.Unmarshal(request.Envelope, &envelope)
	jobResult.Substitutions, jobResult.LiveBoundaries = envelope.Substitutions, envelope.LiveBoundaries
	encoded, _ := json.Marshal(jobResult)
	job.State, job.Result, job.UpdatedAt, job.LeaseOwner, job.LeaseExpiresAt = types.JobCompleted, encoded, s.clock(), "", nil
	if jobResult.Outcome == "inconclusive" {
		job.State = types.JobInconclusive
	}
	if overrideFailed {
		job.State = types.JobFailed
		job.Error = types.NewError(types.ErrUnmatchedBoundary, "the edited value was not applied", "the selected operation received a different request during replay; reproduce the recorded request or edit the model input instead", nil)
	} else if jobResult.ExitCode != 0 {
		job.State = types.JobFailed
		job.Error = types.NewError(types.ErrBuild, "candidate exited unsuccessfully", fmt.Sprintf("inspect candidate capsule %s", jobResult.CandidateCapsuleID), nil)
	}
	if err := s.repository.UpdateJob(ctx, job); err != nil {
		return err
	}
	_ = s.repository.AppendJobEvent(ctx, types.JobEvent{JobID: job.ID, Type: string(job.State), Data: encoded, Time: job.UpdatedAt})
	s.appendStage(ctx, job.ID, "complete", string(job.State), "Replay evidence and the candidate comparison are durable.")
	return nil
}

func verifyReplayOverrides(source, candidate types.RunCapsule, overrides []types.ReplayOverride) []types.ReplayOverrideResult {
	results := make([]types.ReplayOverrideResult, 0, len(overrides))
	for _, override := range overrides {
		result := types.ReplayOverrideResult{BoundaryID: override.BoundaryID, Target: override.Target}
		var sourceBoundary *types.Boundary
		for index := range source.Boundaries {
			if source.Boundaries[index].ID == override.BoundaryID {
				sourceBoundary = &source.Boundaries[index]
				break
			}
		}
		if sourceBoundary == nil {
			result.Message = "the recorded boundary no longer exists"
			results = append(results, result)
			continue
		}
		var candidateBoundary *types.Boundary
		for index := range candidate.Boundaries {
			boundary := &candidate.Boundaries[index]
			if boundary.LogicalKey == sourceBoundary.LogicalKey && boundary.Class == sourceBoundary.Class && boundary.ParentLogicalKey == sourceBoundary.ParentLogicalKey && boundary.Occurrence == sourceBoundary.Occurrence {
				candidateBoundary = boundary
				break
			}
		}
		if candidateBoundary == nil {
			result.Message = "the candidate did not reach the selected operation"
			results = append(results, result)
			continue
		}
		var value any
		if json.Unmarshal(override.Value, &value) != nil {
			result.Message = "the edited value is not valid JSON"
			results = append(results, result)
			continue
		}
		canonical, err := CanonicalJSON(value)
		if err != nil {
			result.Message = "the edited value could not be canonicalized"
			results = append(results, result)
			continue
		}
		expectedDigest := DigestBytes(canonical)
		if override.Target == "request" {
			result.Applied = candidateBoundary.RequestDigest == expectedDigest
		} else {
			result.Applied = candidateBoundary.Response != nil && candidateBoundary.Response.Digest == expectedDigest
		}
		if result.Applied {
			result.Message = "the candidate recorded the edited value"
		} else {
			result.Message = "the candidate did not record the edited value"
		}
		results = append(results, result)
	}
	return results
}

func (s *ReplayService) environmentNames(declared, requested []string) ([]string, error) {
	allowed := map[string]bool{}
	for _, name := range declared {
		allowed[name] = true
	}
	if len(requested) == 0 {
		requested = append([]string(nil), declared...)
	}
	for _, name := range requested {
		if !allowed[name] {
			return nil, types.NewError(types.ErrUnsafeReplay, "replay credential was not declared by the entrypoint", "add the environment name to flight.yaml", nil)
		}
		if _, ok := s.credentials[name]; !ok {
			return nil, types.NewError(types.ErrUnsafeReplay, "declared replay credential is unavailable", "set FLIGHT_REPLAY_CREDENTIAL_"+name+" on the worker", nil)
		}
	}
	return uniqueStrings(requested), nil
}

func executionEquivalent(result types.DiffResult) bool {
	for _, observation := range result.Observations {
		if observation.Kind != "provenance_changed" {
			return false
		}
	}
	return true
}

func classifyReplayOutcome(exact, total int, threshold float64) string {
	if total > 0 && exact == total {
		return "exact"
	}
	if exact == 0 || total <= 0 {
		return "divergent"
	}
	if float64(exact)/float64(total) >= threshold {
		return "equivalent"
	}
	return "inconclusive"
}

func (s *ReplayService) captureCandidates(ctx context.Context, result oci.RunResult) ([]types.RunCapsule, error) {
	candidates := []types.RunCapsule{}
	for _, data := range result.OTLPBatches {
		var export collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(data, &export); err != nil {
			return nil, types.NewError(types.ErrValidation, "candidate emitted malformed OTLP", "fix the candidate instrumentation", err)
		}
		captured, _, err := s.receiver.Capture(ctx, &export)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, captured...)
	}
	return candidates, nil
}

func (s *ReplayService) envelope(ctx context.Context, source types.RunCapsule, plan types.ReplayPlan) (json.RawMessage, []string, []string, []string, error) {
	boundaries := []map[string]any{}
	substitutions, live, allowedHosts := []string{}, []string{}, []string{}
	overrides := map[string]types.ReplayOverride{}
	for _, override := range plan.Overrides {
		overrides[override.BoundaryID] = override
	}
	for _, boundary := range source.Boundaries {
		item := map[string]any{"boundary_id": boundary.ID, "logical_key": boundary.LogicalKey, "boundary_class": boundary.Class, "parent_logical_key": boundary.ParentLogicalKey, "occurrence": boundary.Occurrence, "request_digest": boundary.RequestDigest, "response_state": "omitted"}
		if boundary.Response != nil && boundary.Response.Digest != "" {
			data, err := s.repository.LoadPayload(ctx, boundary.Response.Digest)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			var response any
			if json.Unmarshal(data, &response) != nil {
				response = string(data)
			}
			item["response"], item["response_state"] = response, "captured"
		}
		override, overridden := overrides[boundary.ID]
		if overridden {
			var value any
			if err := json.Unmarshal(override.Value, &value); err != nil {
				return nil, nil, nil, nil, types.NewError(types.ErrValidation, "replay override is not valid JSON", "enter one complete JSON value", err)
			}
			item["override_target"] = override.Target
			if override.Target == "request" {
				canonical, err := CanonicalJSON(value)
				if err != nil {
					return nil, nil, nil, nil, err
				}
				item["request_override"], item["request_digest"] = value, DigestBytes(canonical)
			} else {
				item["response"], item["response_state"] = value, "captured"
			}
		}
		isLLM := boundary.Class == "llm"
		if overridden && override.Target == "response" {
			substitutions = append(substitutions, boundary.LogicalKey)
		} else if overridden && override.Target == "request" {
			live = append(live, boundary.LogicalKey)
			allowedHosts = append(allowedHosts, plan.Entrypoint.ModelHosts...)
		} else if boundary.ReplayPolicy == "live" && (!isLLM || !plan.SubstituteLLM) {
			live = append(live, boundary.LogicalKey)
			if boundary.Sandbox != "" {
				allowedHosts = append(allowedHosts, destinationHost(plan.Entrypoint.Sandboxes[boundary.Sandbox]))
			}
		} else {
			substitutions = append(substitutions, boundary.LogicalKey)
		}
		boundaries = append(boundaries, item)
	}
	if !plan.SubstituteLLM {
		allowedHosts = append(allowedHosts, plan.Entrypoint.ModelHosts...)
	}
	substitutions, live, allowedHosts = uniqueStrings(substitutions), uniqueStrings(live), uniqueStrings(allowedHosts)
	envelope := map[string]any{"schema_version": types.SchemaVersion, "plan": plan, "boundaries": boundaries, "substitutions": substitutions, "live_boundaries": live}
	encoded, err := CanonicalJSON(envelope)
	return encoded, substitutions, live, allowedHosts, err
}

func validateReplayOverrides(source types.RunCapsule, request types.CreateReplayRequest) error {
	if len(request.Overrides) > 1 {
		return types.NewError(types.ErrValidation, "a replay may change only one recorded value", "create separate controlled replays for separate changes", nil)
	}
	for _, override := range request.Overrides {
		if override.BoundaryID == "" || (override.Target != "request" && override.Target != "response") {
			return types.NewError(types.ErrValidation, "replay override target is invalid", "choose a recorded boundary input or output", nil)
		}
		if len(override.Value) == 0 || len(override.Value) > 1<<20 || !json.Valid(override.Value) {
			return types.NewError(types.ErrValidation, "replay override must be one JSON value under 1 MiB", "enter valid JSON or attach larger data as an artifact", nil)
		}
		var boundary *types.Boundary
		for index := range source.Boundaries {
			if source.Boundaries[index].ID == override.BoundaryID {
				boundary = &source.Boundaries[index]
				break
			}
		}
		if boundary == nil {
			return types.NewError(types.ErrUnmatchedBoundary, "the selected replay boundary does not exist", "choose a boundary from the recorded execution", nil)
		}
		if override.Target == "request" && boundary.Class != "llm" {
			return types.NewError(types.ErrValidation, "only model input overrides are supported", "change a tool or model output, or select a model input", nil)
		}
		if override.Target == "request" && request.SubstituteLLM {
			return types.NewError(types.ErrValidation, "a changed model input requires a declared live model call", "keep the selected model boundary live for this replay", nil)
		}
	}
	return nil
}

func (s *ReplayService) failJob(ctx context.Context, job *types.Job, err error) error {
	typed, ok := err.(*types.FlightError)
	if !ok {
		typed = types.NewError(types.ErrStorage, "replay failed", "inspect the job event stream", err)
	}
	job.State, job.Error, job.UpdatedAt, job.LeaseOwner, job.LeaseExpiresAt = types.JobFailed, typed, s.clock(), "", nil
	if typed.Code == types.ErrUnsafeReplay || typed.Code == types.ErrIncompleteCapsule {
		job.State = types.JobBlocked
	}
	_ = s.repository.UpdateJob(ctx, *job)
	data, _ := json.Marshal(map[string]any{"error": typed})
	_ = s.repository.AppendJobEvent(ctx, types.JobEvent{JobID: job.ID, Type: string(job.State), Data: data, Time: job.UpdatedAt})
	s.appendStage(ctx, job.ID, "complete", string(job.State), typed.Message+" "+typed.Remediation)
	return typed
}

func (s *ReplayService) appendStage(ctx context.Context, jobID, stage, state, message string) {
	data, _ := json.Marshal(types.JobStageEvent{Stage: stage, State: state, Message: message})
	_ = s.repository.AppendJobEvent(ctx, types.JobEvent{JobID: jobID, Type: "stage", Data: data, Time: s.clock()})
}

func destinationHost(value string) string {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return strings.Split(value, ":")[0]
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
func defaultGatewayImage(value string) string {
	if value != "" {
		return value
	}
	return "ai-flight-recorder:local"
}

func immutableImage(value string) bool {
	return strings.HasPrefix(value, "sha256:") || strings.Contains(value, "@sha256:")
}
