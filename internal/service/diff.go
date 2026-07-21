package service

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type DiffService struct{}

func NewDiffService() *DiffService { return &DiffService{} }

func (d *DiffService) Compare(baseline, candidate types.RunCapsule) types.DiffResult {
	result := types.DiffResult{BaselineCapsuleID: baseline.CapsuleID, CandidateCapsuleID: candidate.CapsuleID, FirstDivergenceKnown: true}
	matches, unmatchedBase, unmatchedCandidate := alignOperations(baseline.Operations, candidate.Operations)
	confidenceTotal := 0.0
	for _, match := range matches {
		result.Alignments = append(result.Alignments, match.alignment)
		confidenceTotal += match.alignment.Confidence
		if match.alignment.Kind == "ambiguous" {
			continue
		}
		observations := compareOperation(baseline, candidate, match.baseline, match.candidate, match.alignment.Confidence)
		result.Observations = append(result.Observations, observations...)
	}
	for _, operation := range unmatchedBase {
		result.Alignments = append(result.Alignments, types.Alignment{BaselineSpan: operation.SpanID, Kind: "deleted", Confidence: 1})
		if replayRelevant(operation) {
			result.Observations = append(result.Observations, operationObservation("operation_deleted", baseline, candidate, operation, nil, 1))
		}
	}
	for _, operation := range unmatchedCandidate {
		result.Alignments = append(result.Alignments, types.Alignment{CandidateSpan: operation.SpanID, Kind: "inserted", Confidence: 1})
		if replayRelevant(operation) {
			result.Observations = append(result.Observations, operationObservation("operation_inserted", baseline, candidate, types.Operation{}, &operation, 1))
		}
	}
	result.Observations = append(result.Observations, compareProvenance(baseline, candidate)...)
	if len(result.Alignments) > 0 {
		for _, alignment := range result.Alignments {
			if alignment.Kind == "inserted" || alignment.Kind == "deleted" {
				confidenceTotal += alignment.Confidence
			}
		}
		result.AlignmentConfidence = confidenceTotal / float64(len(result.Alignments))
	}
	sort.SliceStable(result.Observations, func(i, j int) bool {
		left, right := observationMoment(result.Observations[i], baseline, candidate), observationMoment(result.Observations[j], baseline, candidate)
		if !left.Equal(right) {
			return left.Before(right)
		}
		leftSequence, rightSequence := observationSequence(result.Observations[i], baseline, candidate), observationSequence(result.Observations[j], baseline, candidate)
		if leftSequence != rightSequence {
			return leftSequence < rightSequence
		}
		return observationPriority(result.Observations[i]) < observationPriority(result.Observations[j])
	})
	if len(result.Observations) > 0 {
		first := result.Observations[0]
		if first.Confidence < 0.6 || ambiguityBefore(result.Alignments, baseline, first) {
			result.FirstDivergenceKnown = false
		} else {
			result.FirstDivergence = &first
		}
	}
	return result
}

func observationMoment(observation types.Observation, baseline, candidate types.RunCapsule) time.Time {
	for _, evidence := range observation.Evidence {
		operations := baseline.Operations
		if evidence.CapsuleID == candidate.CapsuleID {
			operations = candidate.Operations
		}
		for _, operation := range operations {
			if operation.SpanID != evidence.SpanID {
				continue
			}
			if strings.HasSuffix(observation.Path, ".status") || strings.HasSuffix(observation.Path, ".output_digest") {
				if !operation.EndTime.IsZero() {
					return operation.EndTime
				}
			}
			if !operation.StartTime.IsZero() {
				return operation.StartTime
			}
			return time.Unix(0, int64(operation.Sequence)).UTC()
		}
	}
	return time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC)
}

func observationPriority(observation types.Observation) int {
	switch {
	case observation.Kind == "operation_inserted" || observation.Kind == "operation_deleted":
		return 0
	case strings.HasSuffix(observation.Path, ".input_digest"):
		return 1
	case strings.HasSuffix(observation.Path, ".name") || strings.HasSuffix(observation.Path, ".kind"):
		return 2
	case strings.HasSuffix(observation.Path, ".output_digest"):
		return 3
	case strings.HasSuffix(observation.Path, ".status"):
		return 4
	default:
		return 5
	}
}

type operationMatch struct {
	baseline  types.Operation
	candidate types.Operation
	alignment types.Alignment
}

func alignOperations(baseline, candidate []types.Operation) ([]operationMatch, []types.Operation, []types.Operation) {
	used := map[string]bool{}
	parentMap := map[string]string{}
	matches := []operationMatch{}
	unmatchedBase := []types.Operation{}
	for _, left := range baseline {
		type scored struct {
			index int
			score float64
		}
		scores := []scored{}
		for index, right := range candidate {
			if used[right.SpanID] {
				continue
			}
			scores = append(scores, scored{index: index, score: alignmentScore(left, right, parentMap)})
		}
		sort.SliceStable(scores, func(i, j int) bool {
			if scores[i].score == scores[j].score {
				return candidate[scores[i].index].Sequence < candidate[scores[j].index].Sequence
			}
			return scores[i].score > scores[j].score
		})
		if len(scores) == 0 || scores[0].score < 0.5 {
			unmatchedBase = append(unmatchedBase, left)
			continue
		}
		bestIndex, bestScore := scores[0].index, scores[0].score
		alternatives := []string{}
		for _, item := range scores {
			if item.score == bestScore {
				alternatives = append(alternatives, candidate[item.index].SpanID)
			}
		}
		if len(alternatives) > 1 {
			matches = append(matches, operationMatch{baseline: left, alignment: types.Alignment{BaselineSpan: left.SpanID, Kind: "ambiguous", Confidence: bestScore, Alternatives: alternatives}})
			continue
		}
		right := candidate[bestIndex]
		used[right.SpanID] = true
		kind := "matched"
		if expected, ok := parentMap[left.ParentSpanID]; ok && expected != right.ParentSpanID {
			kind = "moved"
		}
		parentMap[left.SpanID] = right.SpanID
		matches = append(matches, operationMatch{left, right, types.Alignment{BaselineSpan: left.SpanID, CandidateSpan: right.SpanID, Kind: kind, Confidence: bestScore}})
	}
	unmatchedCandidate := []types.Operation{}
	for _, operation := range candidate {
		if !used[operation.SpanID] {
			unmatchedCandidate = append(unmatchedCandidate, operation)
		}
	}
	return matches, unmatchedBase, unmatchedCandidate
}

func alignmentScore(left, right types.Operation, parentMap map[string]string) float64 {
	if left.LogicalKey != "" && right.LogicalKey != "" {
		if left.LogicalKey == right.LogicalKey {
			return 1
		}
		return 0
	}
	if stableIdentity(left) != "" && stableIdentity(left) == stableIdentity(right) {
		return 0.98
	}
	score := 0.0
	if left.Kind == right.Kind {
		score += 0.25
	}
	if left.Name == right.Name {
		score += 0.30
	}
	if left.ParentSpanID == "" && right.ParentSpanID == "" {
		score += 0.25
	} else if expected, ok := parentMap[left.ParentSpanID]; ok && expected == right.ParentSpanID {
		score += 0.25
	}
	distance := left.Sequence - right.Sequence
	if distance < 0 {
		distance = -distance
	}
	if distance == 0 {
		score += 0.20
	} else if distance <= 2 {
		score += 0.10
	}
	return score
}

func stableIdentity(operation types.Operation) string {
	return operation.Attributes["flight.instrumentation.id"]
}
func replayRelevant(operation types.Operation) bool {
	return operation.LogicalKey != "" || operation.Kind != "internal" || operation.Input != nil || operation.Output != nil || operation.Status == "error"
}

func compareProvenance(baseline, candidate types.RunCapsule) []types.Observation {
	fields := []struct {
		path          string
		before, after any
	}{
		{"identity.project_id", baseline.ProjectID, candidate.ProjectID},
		{"identity.service_name", baseline.ServiceName, candidate.ServiceName},
		{"provenance.repository_url", baseline.Provenance.RepositoryURL, candidate.Provenance.RepositoryURL},
		{"provenance.project_id", baseline.Provenance.ProjectID, candidate.Provenance.ProjectID},
		{"provenance.commit_sha", baseline.Provenance.CommitSHA, candidate.Provenance.CommitSHA},
		{"provenance.dirty_digest", baseline.Provenance.DirtyDigest, candidate.Provenance.DirtyDigest},
		{"provenance.build_id", baseline.Provenance.BuildID, candidate.Provenance.BuildID},
		{"provenance.container_id", baseline.Provenance.ContainerID, candidate.Provenance.ContainerID},
		{"provenance.runtime", baseline.Provenance.Runtime, candidate.Provenance.Runtime},
		{"provenance.dependency_lock", baseline.Provenance.DependencyLock, candidate.Provenance.DependencyLock},
		{"provenance.feature_flags", baseline.Provenance.FeatureFlags, candidate.Provenance.FeatureFlags},
		{"provenance.environment_facts", baseline.Provenance.EnvironmentFacts, candidate.Provenance.EnvironmentFacts},
		{"provenance.entrypoint", baseline.Provenance.Entrypoint, candidate.Provenance.Entrypoint},
		{"provenance.missing", baseline.Provenance.Missing, candidate.Provenance.Missing},
		{"model_configuration", baseline.Model, candidate.Model},
	}
	result := []types.Observation{}
	for _, field := range fields {
		beforeCanonical, beforeErr := CanonicalJSON(field.before)
		afterCanonical, afterErr := CanonicalJSON(field.after)
		if beforeErr == nil && afterErr == nil && bytes.Equal(beforeCanonical, afterCanonical) {
			continue
		}
		result = append(result, types.Observation{Kind: "provenance_changed", Path: field.path, Before: field.before, After: field.after, Rule: "canonical_exact", Confidence: 1, Evidence: []types.EvidenceRef{{CapsuleID: baseline.CapsuleID, Field: field.path}, {CapsuleID: candidate.CapsuleID, Field: field.path}}})
	}
	return result
}

func ambiguityBefore(alignments []types.Alignment, baseline types.RunCapsule, first types.Observation) bool {
	firstSequence := observationSequence(first, baseline, types.RunCapsule{})
	for _, alignment := range alignments {
		if alignment.Kind != "ambiguous" {
			continue
		}
		for _, operation := range baseline.Operations {
			if operation.SpanID == alignment.BaselineSpan && operation.Sequence <= firstSequence {
				return true
			}
		}
	}
	return false
}

func compareOperation(baseCapsule, candidateCapsule types.RunCapsule, left, right types.Operation, confidence float64) []types.Observation {
	observations := []types.Observation{}
	fields := []struct {
		name          string
		before, after any
	}{
		{"name", left.Name, right.Name}, {"kind", left.Kind, right.Kind}, {"status", left.Status, right.Status},
		{"input_digest", payloadDigest(left.Input), payloadDigest(right.Input)}, {"output_digest", payloadDigest(left.Output), payloadDigest(right.Output)},
	}
	for _, field := range fields {
		if fmt.Sprint(field.before) == fmt.Sprint(field.after) {
			continue
		}
		leftPayload, rightPayload := "", ""
		if field.name == "input_digest" {
			leftPayload, rightPayload = payloadDigest(left.Input), payloadDigest(right.Input)
		}
		if field.name == "output_digest" {
			leftPayload, rightPayload = payloadDigest(left.Output), payloadDigest(right.Output)
		}
		observations = append(observations, types.Observation{
			Kind: "field_changed", Path: "operations." + left.SpanID + "." + field.name, Before: field.before, After: field.after,
			OperationName: left.Name, Field: field.name, Rule: "normalized_exact", Confidence: confidence,
			Evidence: []types.EvidenceRef{{CapsuleID: baseCapsule.CapsuleID, SpanID: left.SpanID, Field: field.name, Payload: leftPayload}, {CapsuleID: candidateCapsule.CapsuleID, SpanID: right.SpanID, Field: field.name, Payload: rightPayload}},
		})
	}
	return observations
}

func operationObservation(kind string, baseline, candidate types.RunCapsule, left types.Operation, right *types.Operation, confidence float64) types.Observation {
	observation := types.Observation{Kind: kind, OperationName: left.Name, Rule: "logical_tree", Confidence: confidence}
	if right == nil {
		observation.Path = "operations." + left.SpanID
		observation.Before = left.Name
		observation.Evidence = []types.EvidenceRef{{CapsuleID: baseline.CapsuleID, SpanID: left.SpanID}}
	} else {
		observation.OperationName = right.Name
		observation.Path = "operations." + right.SpanID
		observation.After = right.Name
		observation.Evidence = []types.EvidenceRef{{CapsuleID: candidate.CapsuleID, SpanID: right.SpanID}}
	}
	return observation
}

func payloadDigest(ref *types.PayloadRef) string {
	if ref == nil {
		return ""
	}
	return ref.Digest
}

func observationSequence(observation types.Observation, baseline, candidate types.RunCapsule) int {
	if len(observation.Evidence) > 0 && observation.Evidence[0].SpanID == "" {
		return 1 << 29
	}
	for _, evidence := range observation.Evidence {
		operations := baseline.Operations
		if evidence.CapsuleID == candidate.CapsuleID {
			operations = candidate.Operations
		}
		for _, operation := range operations {
			if operation.SpanID == evidence.SpanID {
				return operation.Sequence
			}
		}
	}
	return 1 << 30
}
