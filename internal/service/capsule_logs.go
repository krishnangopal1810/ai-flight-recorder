package service

import (
	"context"
	"strings"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type CaptureLog struct {
	Record             types.LogRecord
	Body               []byte
	BodyType           string
	BodyOmission       *types.PayloadRef
	Attributes         []byte
	AttributesType     string
	AttributesOmission *types.PayloadRef
}

func (s *CapsuleService) StageLog(ctx context.Context, projectID, traceID string, captured CaptureLog, expiresAt time.Time) error {
	record := captured.Record
	record.ID, record.Body, record.Attributes = "", nil, nil
	var sanitized types.LogRecord
	metadataResult, err := s.sanitizeValue("logs.metadata", record, &sanitized)
	if err != nil {
		return err
	}
	if metadataResult.Omitted {
		sanitized = types.LogRecord{
			SpanID: record.SpanID, Timestamp: record.Timestamp, ObservedTimestamp: record.ObservedTimestamp,
			SeverityNumber: record.SeverityNumber, EventName: "metadata omitted by policy",
		}
	}
	staged := repository.StagedLog{
		ProjectID: projectID, TraceID: traceID, Record: sanitized,
		Payloads: map[string][]byte{}, ExpiresAt: expiresAt,
		Policy: policyDelta(metadataResult),
	}
	if captured.Body != nil || captured.BodyOmission != nil {
		ref, data, result, captureErr := s.captureContent("logs.body", CapturePayload{Data: captured.Body, MediaType: captured.BodyType, Omission: captured.BodyOmission})
		if captureErr != nil {
			return captureErr
		}
		staged.Record.Body = ref
		mergePolicyDeltaValue(&staged.Policy, policyDelta(result))
		if data != nil {
			staged.Payloads[ref.Digest] = data
		}
	}
	if captured.Attributes != nil || captured.AttributesOmission != nil {
		ref, data, result, captureErr := s.captureContent("logs.attributes", CapturePayload{Data: captured.Attributes, MediaType: captured.AttributesType, Omission: captured.AttributesOmission})
		if captureErr != nil {
			return captureErr
		}
		staged.Record.Attributes = ref
		mergePolicyDeltaValue(&staged.Policy, policyDelta(result))
		if data != nil {
			staged.Payloads[ref.Digest] = data
		}
	}
	canonical, err := CanonicalJSON(staged.Record)
	if err != nil {
		return err
	}
	staged.Record.ID = "log_" + strings.TrimPrefix(DigestBytes(canonical), "sha256:")
	return s.repository.StageLog(ctx, staged)
}

func policyDelta(result PolicyResult) types.PolicyDelta {
	return types.PolicyDelta{Redactions: result.Redactions, Exclusions: result.Exclusions, Truncations: result.Truncations}
}

func mergePolicyDelta(summary *types.PrivacySummary, delta types.PolicyDelta) {
	for key, value := range delta.Redactions {
		summary.Redactions[key] += value
	}
	for key, value := range delta.Exclusions {
		summary.Exclusions[key] += value
	}
	for key, value := range delta.Truncations {
		summary.Truncations[key] += value
	}
}

func mergePolicyDeltaValue(target *types.PolicyDelta, source types.PolicyDelta) {
	if target.Redactions == nil {
		target.Redactions = map[string]int{}
	}
	if target.Exclusions == nil {
		target.Exclusions = map[string]int{}
	}
	if target.Truncations == nil {
		target.Truncations = map[string]int{}
	}
	for key, value := range source.Redactions {
		target.Redactions[key] += value
	}
	for key, value := range source.Exclusions {
		target.Exclusions[key] += value
	}
	for key, value := range source.Truncations {
		target.Truncations[key] += value
	}
}
