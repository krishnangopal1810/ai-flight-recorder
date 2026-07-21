package service

import (
	"context"
	"encoding/base64"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type CapsuleListQuery struct {
	Project   string
	Service   string
	Search    string
	Outcome   string
	Readiness string
	After     *time.Time
	Before    *time.Time
	Order     string
	Cursor    string
	Limit     int
}

type CapsuleListResult struct {
	Items      []types.CapsuleSummary `json:"items"`
	NextCursor string                 `json:"next_cursor,omitempty"`
}

func (s *CapsuleService) ListRuns(ctx context.Context, query CapsuleListQuery) (CapsuleListResult, error) {
	items, err := s.repository.ListCapsules(ctx, query.Project, 5000)
	if err != nil {
		return CapsuleListResult{}, err
	}
	candidates := map[string]int{}
	for _, item := range items {
		if item.SourceCapsuleID != "" {
			candidates[item.SourceCapsuleID]++
		}
	}
	search := strings.ToLower(strings.TrimSpace(query.Search))
	filtered := make([]types.CapsuleSummary, 0, len(items))
	for _, item := range items {
		item.CandidateReplayCount = candidates[item.CapsuleID]
		if query.Service != "" && item.ServiceName != query.Service {
			continue
		}
		if query.Outcome != "" && item.TerminalStatus != query.Outcome {
			continue
		}
		if query.Readiness != "" && string(item.ReplayReadiness) != query.Readiness {
			continue
		}
		if query.After != nil && item.StartTime.Before(*query.After) {
			continue
		}
		if query.Before != nil && !item.StartTime.Before(*query.Before) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(strings.Join([]string{item.CapsuleID, item.TraceID, item.ProjectID, item.ServiceName, item.FailureOperation}, " ")), search) {
			continue
		}
		filtered = append(filtered, item)
	}
	if query.Order == "attention" {
		sort.SliceStable(filtered, func(i, j int) bool {
			left, right := attentionRank(filtered[i]), attentionRank(filtered[j])
			if left != right {
				return left < right
			}
			return filtered[i].StartTime.After(filtered[j].StartTime)
		})
	}
	offset := decodeCursor(query.Cursor)
	if offset > len(filtered) {
		offset = len(filtered)
	}
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	end := min(offset+limit, len(filtered))
	result := CapsuleListResult{Items: append([]types.CapsuleSummary{}, filtered[offset:end]...)}
	if end < len(filtered) {
		result.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	return result, nil
}

func attentionRank(item types.CapsuleSummary) int {
	if item.TerminalStatus != "ok" && item.TerminalStatus != "unset" {
		return 0
	}
	if item.EvidenceWarningCount > 0 || item.ReplayReadiness == types.CompletenessBlocked || item.ReplayReadiness == types.CompletenessIncomplete {
		return 1
	}
	if item.CandidateReplayCount > 0 {
		return 2
	}
	return 3
}

func decodeCursor(value string) int {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}
