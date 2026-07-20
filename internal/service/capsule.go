package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type CaptureOperation struct {
	Operation  types.Operation
	Input      []byte
	InputType  string
	Output     []byte
	OutputType string
	Boundary   *types.Boundary
}

type CaptureRequest struct {
	TraceID         string
	SourceCapsuleID string
	ProjectID       string
	ServiceName     string
	TerminalStatus  string
	Provenance      types.Provenance
	Model           types.ModelConfiguration
	Operations      []CaptureOperation
	Artifacts       []struct {
		Artifact types.Artifact
		Data     []byte
	}
	StartTime      time.Time
	EndTime        time.Time
	IngestRejected int
}

type CapsuleService struct {
	repository repository.Repository
	policy     *PolicyEngine
	clock      func() time.Time
}

func NewCapsuleService(repo repository.Repository, policy *PolicyEngine) *CapsuleService {
	return &CapsuleService{repository: repo, policy: policy, clock: func() time.Time { return time.Now().UTC() }}
}

func (s *CapsuleService) Capture(ctx context.Context, request CaptureRequest) (types.RunCapsule, error) {
	if request.TraceID == "" {
		record := repository.QuarantineRecord{ID: uuid.NewString(), Reason: "root_trace_missing", Safe: map[string]any{"service_name_hash": DigestBytes([]byte(request.ServiceName)), "operation_count": len(request.Operations)}, CreatedAt: s.clock()}
		if err := s.repository.PutQuarantine(ctx, record); err != nil {
			return types.RunCapsule{}, err
		}
		return types.RunCapsule{}, types.NewError(types.ErrValidation, "root execution cannot be identified", "instrument the root span and include a trace ID", nil)
	}
	capsule := types.RunCapsule{
		SchemaVersion: types.SchemaVersion, TraceID: request.TraceID, SourceCapsuleID: request.SourceCapsuleID,
		StartTime: request.StartTime, EndTime: request.EndTime, TerminalStatus: request.TerminalStatus,
		Privacy: types.PrivacySummary{Mode: s.policy.policy.Mode, PolicyVersion: s.policy.policy.Version, Redactions: map[string]int{}, Exclusions: map[string]int{}, ExportPolicy: "allowed"},
	}
	if result, err := s.sanitizeValue("project_id", request.ProjectID, &capsule.ProjectID); err != nil {
		return types.RunCapsule{}, s.policyFailure(ctx, request, err)
	} else {
		mergePolicySummary(&capsule.Privacy, result)
	}
	if result, err := s.sanitizeValue("service_name", request.ServiceName, &capsule.ServiceName); err != nil {
		return types.RunCapsule{}, s.policyFailure(ctx, request, err)
	} else {
		mergePolicySummary(&capsule.Privacy, result)
	}
	if result, err := s.sanitizeValue("provenance", request.Provenance, &capsule.Provenance); err != nil {
		return types.RunCapsule{}, s.policyFailure(ctx, request, err)
	} else {
		mergePolicySummary(&capsule.Privacy, result)
	}
	if result, err := s.sanitizeValue("model_configuration", request.Model, &capsule.Model); err != nil {
		return types.RunCapsule{}, s.policyFailure(ctx, request, err)
	} else {
		mergePolicySummary(&capsule.Privacy, result)
	}
	if capsule.StartTime.IsZero() {
		capsule.StartTime = s.clock()
	}
	if capsule.EndTime.IsZero() {
		capsule.EndTime = capsule.StartTime
	}
	if retention := s.policy.Retention(capsule.ProjectID); retention > 0 {
		expires := capsule.EndTime.Add(retention)
		capsule.ExpiresAt = &expires
	}
	if capsule.TerminalStatus == "" {
		capsule.TerminalStatus = "unset"
	}
	payloads := map[string][]byte{}
	boundaryOccurrences := map[string]int{}
	for index, captured := range request.Operations {
		operation := captured.Operation
		operation.Sequence = index
		if len(captured.Input) > 0 {
			ref, data, result, err := s.capturePayload("operations.input", captured.Input, captured.InputType)
			if err != nil {
				return types.RunCapsule{}, s.policyFailure(ctx, request, err)
			}
			operation.Input = &ref
			mergePolicySummary(&capsule.Privacy, result)
			if data != nil {
				payloads[ref.Digest] = data
			}
		}
		if len(captured.Output) > 0 {
			ref, data, result, err := s.capturePayload("operations.output", captured.Output, captured.OutputType)
			if err != nil {
				return types.RunCapsule{}, s.policyFailure(ctx, request, err)
			}
			operation.Output = &ref
			mergePolicySummary(&capsule.Privacy, result)
			if data != nil {
				payloads[ref.Digest] = data
			}
		}
		var sanitizedOperation types.Operation
		metadataResult, err := s.sanitizeValue("operations.metadata", operation, &sanitizedOperation)
		if err != nil {
			return types.RunCapsule{}, s.policyFailure(ctx, request, err)
		}
		mergePolicySummary(&capsule.Privacy, metadataResult)
		if metadataResult.Omitted {
			sanitizedOperation = types.Operation{SpanID: operation.SpanID, ParentSpanID: operation.ParentSpanID, Kind: "omitted", Name: "metadata omitted by policy", StartTime: operation.StartTime, EndTime: operation.EndTime, Input: operation.Input, Output: operation.Output, Status: operation.Status, Sequence: operation.Sequence}
		}
		operation = sanitizedOperation
		capsule.Operations = append(capsule.Operations, operation)
		if captured.Boundary != nil {
			boundary := *captured.Boundary
			occurrenceKey := boundary.ParentLogicalKey + "\x00" + boundary.Class + "\x00" + boundary.LogicalKey
			boundary.Occurrence = boundaryOccurrences[occurrenceKey]
			boundaryOccurrences[occurrenceKey]++
			if boundary.ID == "" {
				boundary.ID = "boundary_" + operation.SpanID
			}
			boundary.OperationSpanID = operation.SpanID
			if operation.Output != nil {
				boundary.Response = operation.Output
			}
			var sanitizedBoundary types.Boundary
			boundaryResult, err := s.sanitizeValue("boundaries.metadata", boundary, &sanitizedBoundary)
			if err != nil {
				return types.RunCapsule{}, s.policyFailure(ctx, request, err)
			}
			mergePolicySummary(&capsule.Privacy, boundaryResult)
			if boundaryResult.Omitted {
				return types.RunCapsule{}, types.NewError(types.ErrValidation, "capture policy omitted required boundary metadata", "use redaction instead of excluding the complete boundary", nil)
			}
			capsule.Boundaries = append(capsule.Boundaries, sanitizedBoundary)
		}
	}
	for _, captured := range request.Artifacts {
		artifact := captured.Artifact
		if len(captured.Data) > 0 {
			ref, data, result, err := s.capturePayload("artifacts."+artifact.LogicalName, captured.Data, artifact.MediaType)
			if err != nil {
				return types.RunCapsule{}, s.policyFailure(ctx, request, err)
			}
			artifact.Payload = &ref
			artifact.State = ref.State
			mergePolicySummary(&capsule.Privacy, result)
			if data != nil {
				payloads[ref.Digest] = data
			}
		}
		var sanitizedArtifact types.Artifact
		artifactResult, err := s.sanitizeValue("artifacts.metadata", artifact, &sanitizedArtifact)
		if err != nil {
			return types.RunCapsule{}, s.policyFailure(ctx, request, err)
		}
		mergePolicySummary(&capsule.Privacy, artifactResult)
		if artifactResult.Omitted {
			sanitizedArtifact = types.Artifact{ID: artifact.ID, Type: "omitted", LogicalName: "metadata omitted by policy", State: "omitted"}
		}
		capsule.Artifacts = append(capsule.Artifacts, sanitizedArtifact)
	}
	policyJSON, _ := CanonicalJSON(s.policy.policy)
	capsule.Privacy.PolicyDigest = DigestBytes(policyJSON)
	ComputeCompleteness(&capsule)
	if request.IngestRejected > 0 {
		item := types.MissingItem{Code: "ingest_spans_rejected", Message: fmt.Sprintf("%d malformed spans were isolated from the accepted batch", request.IngestRejected)}
		playback := capsule.Completeness["playback"]
		playback.State = types.CompletenessIncomplete
		playback.Missing = append(playback.Missing, item)
		capsule.Completeness["playback"] = playback
	}
	if err := FinalizeCapsule(&capsule, payloads); err != nil {
		return types.RunCapsule{}, err
	}
	if err := s.repository.PutCapsule(ctx, capsule, payloads); err != nil {
		return types.RunCapsule{}, err
	}
	return capsule, nil
}

func (s *CapsuleService) sanitizeValue(name string, value, destination any) (PolicyResult, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return PolicyResult{}, types.NewError(types.ErrValidation, "capture metadata is not JSON", "use JSON-compatible metadata values", err)
	}
	result, err := s.policy.ApplyJSON(name, raw)
	if err != nil || result.Omitted {
		return result, err
	}
	if err := json.Unmarshal(result.Data, destination); err != nil {
		return result, types.NewError(types.ErrValidation, "policy-safe metadata is invalid", "fix the capture policy transformation", err)
	}
	return result, nil
}

func (s *CapsuleService) Get(ctx context.Context, id string, verify bool) (types.RunCapsule, error) {
	capsule, err := s.repository.GetCapsule(ctx, id)
	if err != nil {
		return types.RunCapsule{}, err
	}
	if verify {
		if err := VerifyCapsule(capsule, func(digest string) ([]byte, error) { return s.repository.LoadPayload(ctx, digest) }); err != nil {
			return types.RunCapsule{}, err
		}
	}
	return capsule, nil
}

func (s *CapsuleService) List(ctx context.Context, project string, limit int) ([]types.CapsuleSummary, error) {
	return s.repository.ListCapsules(ctx, project, limit)
}
func (s *CapsuleService) Payload(ctx context.Context, digest string) ([]byte, error) {
	return s.repository.LoadPayload(ctx, digest)
}

// SanitizeEvidence applies the same ordered capture policy used before capsule
// persistence. Evaluator output is evidence and cannot bypass that boundary.
func (s *CapsuleService) SanitizeEvidence(name string, data []byte) ([]byte, error) {
	result, err := s.policy.ApplyJSON(name, data)
	if err != nil {
		return nil, err
	}
	if result.Omitted {
		return nil, types.NewError(types.ErrEvaluator, "capture policy omitted evaluator output", "return policy-safe evaluator evidence or adjust the capture policy", nil)
	}
	return result.Data, nil
}

func (s *CapsuleService) QuarantineDiagnostic(ctx context.Context, reason, traceID string, operationCount int) error {
	return s.repository.PutQuarantine(ctx, repository.QuarantineRecord{ID: uuid.NewString(), Reason: reason, Safe: map[string]any{"trace_id_hash": DigestBytes([]byte(traceID)), "operation_count": operationCount}, CreatedAt: s.clock()})
}

func (s *CapsuleService) capturePayload(name string, raw []byte, mediaType string) (types.PayloadRef, []byte, PolicyResult, error) {
	result, err := s.policy.ApplyJSON(name, raw)
	if err != nil {
		return types.PayloadRef{}, nil, PolicyResult{}, err
	}
	if result.Omitted {
		return types.PayloadRef{MediaType: mediaType, Size: int64(len(raw)), State: "omitted", Reason: result.Reason}, nil, result, nil
	}
	digest := DigestBytes(result.Data)
	return types.PayloadRef{Digest: digest, MediaType: mediaType, Size: int64(len(result.Data)), State: "captured"}, result.Data, result, nil
}

func (s *CapsuleService) policyFailure(ctx context.Context, request CaptureRequest, cause error) error {
	if s.policy.policy.Mode == "strict" {
		_ = s.repository.PutQuarantine(ctx, repository.QuarantineRecord{ID: uuid.NewString(), Reason: "policy_failure", Safe: map[string]any{"trace_id_hash": DigestBytes([]byte(request.TraceID)), "operation_count": len(request.Operations), "error_code": string(types.ErrValidation)}, CreatedAt: s.clock()})
	}
	return types.NewError(types.ErrValidation, "capture policy rejected a payload", "fix the policy or omit the affected field; raw payload was not retained", cause)
}

func mergePolicySummary(summary *types.PrivacySummary, result PolicyResult) {
	for name, count := range result.Redactions {
		summary.Redactions[name] += count
	}
	for name, count := range result.Exclusions {
		summary.Exclusions[name] += count
	}
}

func SortOperations(operations []types.Operation) {
	sort.SliceStable(operations, func(i, j int) bool {
		if operations[i].StartTime.Equal(operations[j].StartTime) {
			return operations[i].SpanID < operations[j].SpanID
		}
		return operations[i].StartTime.Before(operations[j].StartTime)
	})
	for index := range operations {
		operations[index].Sequence = index
	}
}
