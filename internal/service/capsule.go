package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type CaptureOperation struct {
	Operation      types.Operation
	Input          []byte
	InputType      string
	InputOmission  *types.PayloadRef
	Output         []byte
	OutputType     string
	OutputOmission *types.PayloadRef
	Boundary       *types.Boundary
	Evaluation     *types.Evaluation
}

type CapturePayload struct {
	Data      []byte
	MediaType string
	Omission  *types.PayloadRef
}

type CaptureRequest struct {
	TraceID         string
	SourceCapsuleID string
	ProjectID       string
	ServiceName     string
	TerminalStatus  string
	Provenance      types.Provenance
	Model           types.ModelConfiguration
	ToolDefinitions []CapturePayload
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
		Privacy: types.PrivacySummary{Mode: s.policy.policy.Mode, PolicyVersion: s.policy.policy.Version, Redactions: map[string]int{}, Exclusions: map[string]int{}, Truncations: map[string]int{}, ExportPolicy: "allowed"},
	}
	payloads := map[string][]byte{}
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
	model := request.Model
	model.ToolDefinitions = nil
	if result, err := s.sanitizeValue("model_configuration", model, &capsule.Model); err != nil {
		return types.RunCapsule{}, s.policyFailure(ctx, request, err)
	} else {
		mergePolicySummary(&capsule.Privacy, result)
	}
	for _, captured := range request.ToolDefinitions {
		ref, data, result, err := s.captureContent("model_configuration.tool_definitions", captured)
		if err != nil {
			return types.RunCapsule{}, s.policyFailure(ctx, request, err)
		}
		if ref == nil {
			continue
		}
		capsule.Model.ToolDefinitions = appendUniquePayload(capsule.Model.ToolDefinitions, *ref)
		mergePolicySummary(&capsule.Privacy, result)
		if data != nil {
			payloads[ref.Digest] = data
		}
	}
	sort.Slice(capsule.Model.ToolDefinitions, func(i, j int) bool {
		left, right := capsule.Model.ToolDefinitions[i], capsule.Model.ToolDefinitions[j]
		return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%s", left.Digest, left.State, left.Reason, left.Size, left.OriginalSize, left.MediaType) < fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%s", right.Digest, right.State, right.Reason, right.Size, right.OriginalSize, right.MediaType)
	})
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
	boundaryOccurrences := map[string]int{}
	for index, captured := range request.Operations {
		operation := captured.Operation
		operation.Sequence = index
		if captured.Input != nil || captured.InputOmission != nil {
			ref, data, result, err := s.captureContent("operations.input", CapturePayload{Data: captured.Input, MediaType: captured.InputType, Omission: captured.InputOmission})
			if err != nil {
				return types.RunCapsule{}, s.policyFailure(ctx, request, err)
			}
			operation.Input = ref
			mergePolicySummary(&capsule.Privacy, result)
			if data != nil {
				payloads[ref.Digest] = data
			}
		}
		if captured.Output != nil || captured.OutputOmission != nil {
			ref, data, result, err := s.captureContent("operations.output", CapturePayload{Data: captured.Output, MediaType: captured.OutputType, Omission: captured.OutputOmission})
			if err != nil {
				return types.RunCapsule{}, s.policyFailure(ctx, request, err)
			}
			operation.Output = ref
			mergePolicySummary(&capsule.Privacy, result)
			if data != nil {
				payloads[ref.Digest] = data
			}
		}
		inputRef, outputRef := operation.Input, operation.Output
		operation.Input, operation.Output = nil, nil
		var sanitizedOperation types.Operation
		metadataResult, err := s.sanitizeValue("operations.metadata", operation, &sanitizedOperation)
		if err != nil {
			return types.RunCapsule{}, s.policyFailure(ctx, request, err)
		}
		mergePolicySummary(&capsule.Privacy, metadataResult)
		if metadataResult.Omitted {
			sanitizedOperation = types.Operation{SpanID: operation.SpanID, ParentSpanID: operation.ParentSpanID, Kind: "omitted", Name: "metadata omitted by policy", StartTime: operation.StartTime, EndTime: operation.EndTime, Status: operation.Status, Sequence: operation.Sequence}
		}
		sanitizedOperation.Input, sanitizedOperation.Output = inputRef, outputRef
		operation = sanitizedOperation
		capsule.Operations = append(capsule.Operations, operation)
		if captured.Evaluation != nil {
			evaluation := *captured.Evaluation
			evaluation.Inputs, evaluation.Evidence = nil, nil
			var sanitizedEvaluation types.Evaluation
			evaluationResult, err := s.sanitizeValue("evaluations.metadata", evaluation, &sanitizedEvaluation)
			if err != nil {
				return types.RunCapsule{}, s.policyFailure(ctx, request, err)
			}
			mergePolicySummary(&capsule.Privacy, evaluationResult)
			if !evaluationResult.Omitted {
				if operation.Input != nil {
					sanitizedEvaluation.Inputs = []types.PayloadRef{*operation.Input}
				}
				if operation.Output != nil {
					sanitizedEvaluation.Evidence = operation.Output
				}
				capsule.Evaluations = append(capsule.Evaluations, sanitizedEvaluation)
			}
		}
		if captured.Boundary != nil {
			boundary := *captured.Boundary
			requestDigest, err := boundaryDigestAfterPolicy(operation.Input, payloads, boundary.MatchingRule.IgnorePaths)
			if err != nil {
				return types.RunCapsule{}, err
			}
			boundary.RequestDigest = requestDigest
			occurrenceKey := boundary.ParentLogicalKey + "\x00" + boundary.Class + "\x00" + boundary.LogicalKey
			boundary.Occurrence = boundaryOccurrences[occurrenceKey]
			boundaryOccurrences[occurrenceKey]++
			if boundary.ID == "" {
				boundary.ID = "boundary_" + operation.SpanID
			}
			boundary.OperationSpanID = operation.SpanID
			boundary.Response = nil
			var sanitizedBoundary types.Boundary
			boundaryResult, err := s.sanitizeValue("boundaries.metadata", boundary, &sanitizedBoundary)
			if err != nil {
				return types.RunCapsule{}, s.policyFailure(ctx, request, err)
			}
			mergePolicySummary(&capsule.Privacy, boundaryResult)
			if boundaryResult.Omitted {
				return types.RunCapsule{}, types.NewError(types.ErrValidation, "capture policy omitted required boundary metadata", "use redaction instead of excluding the complete boundary", nil)
			}
			sanitizedBoundary.Response = operation.Output
			capsule.Boundaries = append(capsule.Boundaries, sanitizedBoundary)
		}
	}
	for _, captured := range request.Artifacts {
		artifact := captured.Artifact
		declaredPayload := artifact.Payload
		artifact.Payload = nil
		if captured.Data != nil || declaredPayload != nil {
			ref, data, result, err := s.captureContentClass("artifacts", artifact.Type, CapturePayload{Data: captured.Data, MediaType: artifact.MediaType, Omission: declaredPayload})
			if err != nil {
				return types.RunCapsule{}, s.policyFailure(ctx, request, err)
			}
			artifact.Payload = ref
			artifact.State = ref.State
			mergePolicySummary(&capsule.Privacy, result)
			if data != nil {
				payloads[ref.Digest] = data
			}
		}
		payloadRef := artifact.Payload
		artifact.Payload = nil
		var sanitizedArtifact types.Artifact
		artifactResult, err := s.sanitizeValue("artifacts.metadata", artifact, &sanitizedArtifact)
		if err != nil {
			return types.RunCapsule{}, s.policyFailure(ctx, request, err)
		}
		mergePolicySummary(&capsule.Privacy, artifactResult)
		if artifactResult.Omitted {
			sanitizedArtifact = types.Artifact{ID: artifact.ID, Type: "omitted", LogicalName: "metadata omitted by policy", State: "omitted"}
		}
		sanitizedArtifact.Payload = payloadRef
		if payloadRef != nil {
			sanitizedArtifact.State = payloadRef.State
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

func (s *CapsuleService) capturePayload(name, artifactClass string, raw []byte, mediaType string) (types.PayloadRef, []byte, PolicyResult, error) {
	if isJSONMediaType(mediaType) && !json.Valid(raw) {
		return types.PayloadRef{MediaType: mediaType, Size: int64(len(raw)), State: types.PayloadStateOmitted, Reason: types.OmissionSerialization}, nil, PolicyResult{}, nil
	}
	result, err := s.policy.ApplyPayload(name, artifactClass, mediaType, raw)
	if err != nil {
		return types.PayloadRef{}, nil, PolicyResult{}, err
	}
	if result.Omitted {
		return types.PayloadRef{MediaType: mediaType, Size: int64(len(raw)), State: types.PayloadStateOmitted, Reason: result.Reason}, nil, result, nil
	}
	digest := DigestBytes(result.Data)
	ref := types.PayloadRef{Digest: digest, MediaType: mediaType, Size: int64(len(result.Data)), State: types.PayloadStateCaptured}
	if result.Truncated {
		ref.OriginalSize, ref.Reason = int64(result.OriginalSize), types.PayloadTruncated
	}
	return ref, result.Data, result, nil
}

func boundaryDigestAfterPolicy(ref *types.PayloadRef, payloads map[string][]byte, ignorePaths []string) (string, error) {
	if ref == nil || ref.State != types.PayloadStateCaptured {
		return "", nil
	}
	data, ok := payloads[ref.Digest]
	if !ok {
		return "", types.NewError(types.ErrValidation, "boundary request payload is unavailable", "derive the match digest only from policy-processed request bytes", nil)
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(ref.MediaType, ";")[0]))
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return ref.Digest, nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return "", types.NewError(types.ErrValidation, "policy-safe boundary request is not JSON", "emit valid JSON or use a non-JSON media type", err)
	}
	value, err := applyBoundaryIgnores(value, ignorePaths)
	if err != nil {
		return "", err
	}
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	return DigestBytes(canonical), nil
}

func applyBoundaryIgnores(value any, pointers []string) (any, error) {
	for _, pointer := range pointers {
		if !strings.HasPrefix(pointer, "/") {
			return nil, types.NewError(types.ErrValidation, "boundary ignore path is not a JSON pointer", "start every ignore path with /", nil)
		}
		encoded := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
		parts := make([]string, len(encoded))
		for index, part := range encoded {
			parts[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		}
		var err error
		value, err = removeBoundaryPath(value, parts)
		if err != nil {
			return nil, err
		}
	}
	return value, nil
}

func removeBoundaryPath(value any, parts []string) (any, error) {
	if len(parts) == 0 {
		return value, nil
	}
	switch current := value.(type) {
	case map[string]any:
		child, exists := current[parts[0]]
		if !exists {
			return value, nil
		}
		if len(parts) == 1 {
			delete(current, parts[0])
			return current, nil
		}
		updated, err := removeBoundaryPath(child, parts[1:])
		if err != nil {
			return nil, err
		}
		current[parts[0]] = updated
		return current, nil
	case []any:
		index, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, types.NewError(types.ErrValidation, "boundary ignore path has a non-numeric array index", "use a zero-based integer in JSON array pointers", err)
		}
		if index < 0 || index >= len(current) {
			return current, nil
		}
		if len(parts) == 1 {
			return append(current[:index], current[index+1:]...), nil
		}
		updated, err := removeBoundaryPath(current[index], parts[1:])
		if err != nil {
			return nil, err
		}
		current[index] = updated
		return current, nil
	default:
		return value, nil
	}
}

func (s *CapsuleService) captureContent(name string, captured CapturePayload) (*types.PayloadRef, []byte, PolicyResult, error) {
	return s.captureContentClass(name, "", captured)
}

func (s *CapsuleService) captureContentClass(name, artifactClass string, captured CapturePayload) (*types.PayloadRef, []byte, PolicyResult, error) {
	if captured.Data != nil && captured.Omission != nil {
		return nil, nil, PolicyResult{}, types.NewError(types.ErrValidation, "captured content has both bytes and an omission", "emit either policy-processable bytes or one typed omission", nil)
	}
	if captured.Data != nil {
		ref, data, result, err := s.capturePayload(name, artifactClass, captured.Data, captured.MediaType)
		return &ref, data, result, err
	}
	if captured.Omission == nil {
		return nil, nil, PolicyResult{}, nil
	}
	ref := normalizeOmission(*captured.Omission, captured.MediaType)
	return &ref, nil, PolicyResult{}, nil
}

func normalizeOmission(ref types.PayloadRef, mediaType string) types.PayloadRef {
	ref.State = types.PayloadStateOmitted
	if ref.MediaType == "" {
		ref.MediaType = mediaType
	}
	if ref.Size < 0 || !validOmissionReason(ref.Reason) {
		ref.Size = max(ref.Size, 0)
		ref.Reason = types.OmissionInvalidMetadata
	}
	if ref.Digest != "" && !validSHA256(ref.Digest) {
		ref.Digest = ""
		ref.Reason = types.OmissionInvalidMetadata
	}
	return ref
}

func validOmissionReason(reason string) bool {
	switch reason {
	case types.OmissionPayloadLimit, types.OmissionPolicyExclusion, types.OmissionSerialization, types.OmissionInvalidMetadata:
		return true
	default:
		return false
	}
}

func validSHA256(value string) bool {
	raw := strings.TrimPrefix(value, "sha256:")
	if raw == value || len(raw) != 64 || raw != strings.ToLower(raw) {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil
}

func appendUniquePayload(values []types.PayloadRef, candidate types.PayloadRef) []types.PayloadRef {
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
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
	for name, count := range result.Truncations {
		summary.Truncations[name] += count
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
