package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestListRunsFiltersRanksAndPaginatesInvestigationSummaries(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	now := time.Now().UTC()
	ready := map[string]types.Completeness{"playback": {State: types.CompletenessReady}, "replay": {State: types.CompletenessReady}}
	for _, capsule := range []types.RunCapsule{
		{SchemaVersion: types.SchemaVersion, CapsuleID: "successful", TraceID: "trace-success", ProjectID: "refunds", ServiceName: "refund-agent", StartTime: now.Add(-time.Hour), EndTime: now.Add(-time.Hour + time.Second), TerminalStatus: "ok", Completeness: ready},
		{SchemaVersion: types.SchemaVersion, CapsuleID: "failed", TraceID: "trace-failed", ProjectID: "refunds", ServiceName: "refund-agent", StartTime: now.Add(-2 * time.Hour), EndTime: now.Add(-2*time.Hour + 2*time.Second), TerminalStatus: "error", Completeness: ready, Operations: []types.Operation{{SpanID: "root", Name: "review-refund", Status: "error", Sequence: 0}, {SpanID: "model", ParentSpanID: "root", Name: "refund-decision", Status: "error", Sequence: 1}}},
		{SchemaVersion: types.SchemaVersion, CapsuleID: "candidate", SourceCapsuleID: "failed", TraceID: "trace-candidate", ProjectID: "refunds", ServiceName: "refund-agent", StartTime: now.Add(-30 * time.Minute), EndTime: now.Add(-30*time.Minute + time.Second), TerminalStatus: "ok", Completeness: ready},
	} {
		if err := repo.PutCapsule(ctx, capsule, nil); err != nil {
			t.Fatal(err)
		}
	}
	service := NewCapsuleService(repo, nil)
	page, err := service.ListRuns(ctx, CapsuleListQuery{Project: "refunds", Search: "refund-decision", Order: "attention", After: timePointer(now.Add(-3 * time.Hour)), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].CapsuleID != "failed" || page.Items[0].FailureOperation != "refund-decision" || page.Items[0].CandidateReplayCount != 1 {
		t.Fatalf("first page = %#v", page)
	}
	all, err := service.ListRuns(ctx, CapsuleListQuery{Project: "refunds", Order: "attention", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 2 || all.Items[0].CapsuleID != "failed" || all.NextCursor == "" {
		t.Fatalf("attention order = %#v", all.Items)
	}
}

func timePointer(value time.Time) *time.Time { return &value }
