package service

import (
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRCAP002EveryProvenanceComponentChangesIdentityAndDiffEvidence(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		mutate func(*types.RunCapsule)
	}{
		{name: "project identity", path: "identity.project_id", mutate: func(c *types.RunCapsule) { c.ProjectID = "other" }},
		{name: "service identity", path: "identity.service_name", mutate: func(c *types.RunCapsule) { c.ServiceName = "other" }},
		{name: "repository", path: "provenance.repository_url", mutate: func(c *types.RunCapsule) { c.Provenance.RepositoryURL = "https://example.invalid/other.git" }},
		{name: "source project", path: "provenance.project_id", mutate: func(c *types.RunCapsule) { c.Provenance.ProjectID = "other" }},
		{name: "commit", path: "provenance.commit_sha", mutate: func(c *types.RunCapsule) { c.Provenance.CommitSHA = strings.Repeat("b", 40) }},
		{name: "dirty worktree", path: "provenance.dirty_digest", mutate: func(c *types.RunCapsule) { c.Provenance.DirtyDigest = "sha256:" + strings.Repeat("b", 64) }},
		{name: "build", path: "provenance.build_id", mutate: func(c *types.RunCapsule) { c.Provenance.BuildID = "build-2" }},
		{name: "container", path: "provenance.container_id", mutate: func(c *types.RunCapsule) { c.Provenance.ContainerID = "container-2" }},
		{name: "runtime", path: "provenance.runtime", mutate: func(c *types.RunCapsule) { c.Provenance.Runtime = "go:1.26" }},
		{name: "dependency lock", path: "provenance.dependency_lock", mutate: func(c *types.RunCapsule) { c.Provenance.DependencyLock["go.sum"] = "sha256:" + strings.Repeat("b", 64) }},
		{name: "feature flags", path: "provenance.feature_flags", mutate: func(c *types.RunCapsule) { c.Provenance.FeatureFlags["retrieval"] = "v3" }},
		{name: "environment facts", path: "provenance.environment_facts", mutate: func(c *types.RunCapsule) {
			c.Provenance.EnvironmentFacts["REGION"] = "sha256:" + strings.Repeat("b", 64)
		}},
		{name: "entrypoint", path: "provenance.entrypoint", mutate: func(c *types.RunCapsule) { c.Provenance.Entrypoint.Image = "sha256:" + strings.Repeat("b", 64) }},
		{name: "missing state", path: "provenance.missing", mutate: func(c *types.RunCapsule) { c.Provenance.Missing = []string{"runtime"} }},
		{name: "model configuration", path: "model_configuration", mutate: func(c *types.RunCapsule) { c.Model.RequestedModel = "gpt-flight-2" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := completeProvenanceCapsule(t)
			candidate := completeProvenanceCapsule(t)
			test.mutate(&candidate)
			if err := FinalizeCapsule(&candidate, map[string][]byte{}); err != nil {
				t.Fatal(err)
			}
			if candidate.CapsuleID == baseline.CapsuleID || candidate.Integrity.RootDigest == baseline.Integrity.RootDigest {
				t.Fatalf("%s did not change immutable capsule identity", test.path)
			}
			diff := NewDiffService().Compare(baseline, candidate)
			var observed *types.Observation
			for index := range diff.Observations {
				if diff.Observations[index].Path == test.path {
					observed = &diff.Observations[index]
					break
				}
			}
			if observed == nil || observed.Kind != "provenance_changed" || observed.Rule != "canonical_exact" || observed.Confidence != 1 || len(observed.Evidence) != 2 || observed.Evidence[0].Field != test.path || observed.Evidence[1].Field != test.path {
				t.Fatalf("%s diff evidence = %#v", test.path, diff.Observations)
			}
		})
	}
}

func completeProvenanceCapsule(t *testing.T) types.RunCapsule {
	t.Helper()
	now := time.Unix(1, 0).UTC()
	capsule := types.RunCapsule{
		SchemaVersion: types.SchemaVersion, TraceID: strings.Repeat("a", 32), ProjectID: "support", ServiceName: "agent", StartTime: now, EndTime: now.Add(time.Second), TerminalStatus: "ok",
		Completeness: map[string]types.Completeness{}, Privacy: types.PrivacySummary{Mode: "strict"},
		Provenance: types.Provenance{
			RepositoryURL: "https://example.invalid/agent.git", ProjectID: "support", CommitSHA: strings.Repeat("a", 40), DirtyDigest: "sha256:" + strings.Repeat("a", 64), BuildID: "build-1", ContainerID: "container-1", Runtime: "go:1.25",
			DependencyLock: map[string]string{"go.sum": "sha256:" + strings.Repeat("a", 64)}, FeatureFlags: map[string]any{"retrieval": "v2"}, EnvironmentFacts: map[string]string{"REGION": "sha256:" + strings.Repeat("a", 64)},
			Entrypoint: &types.Entrypoint{Command: []string{"/agent"}, Image: "sha256:" + strings.Repeat("a", 64), InputMode: "stdin_json", OutputMode: "stdout_json", ModelHosts: []string{"model.test.invalid"}},
		},
		Model:      types.ModelConfiguration{Provider: "openai", RequestedModel: "gpt-flight", ProviderReportedModel: "gpt-flight-2026", Parameters: map[string]any{"temperature": 0}, ResponseMetadata: map[string]any{"finish_reason": "stop"}},
		Operations: []types.Operation{{SpanID: strings.Repeat("1", 16), LogicalKey: "agent.answer", Kind: "agent", Name: "answer", StartTime: now, EndTime: now.Add(time.Second), Status: "ok"}},
	}
	if err := FinalizeCapsule(&capsule, map[string][]byte{}); err != nil {
		t.Fatal(err)
	}
	return capsule
}
