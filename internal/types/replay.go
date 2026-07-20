package types

import "encoding/json"

type CreateReplayRequest struct {
	SourceCapsuleID           string   `json:"source_capsule_id"`
	Model                     string   `json:"model,omitempty"`
	Revision                  string   `json:"revision,omitempty"`
	CandidateImage            string   `json:"candidate_image,omitempty"`
	CandidateCommit           string   `json:"candidate_commit,omitempty"`
	CandidateDirtyDigest      string   `json:"candidate_dirty_digest,omitempty"`
	CandidateSourceDigest     string   `json:"candidate_source_digest,omitempty"`
	SubstituteLLM             bool     `json:"substitute_llm"`
	AllowSandboxedSideEffects bool     `json:"allow_sandboxed_side_effects"`
	Repetitions               int      `json:"repetitions,omitempty"`
	PassThreshold             float64  `json:"pass_threshold,omitempty"`
	Environment               []string `json:"environment,omitempty"`
}

type BisectRequest struct {
	Revisions       []string             `json:"revisions"`
	Evaluations     []RevisionEvaluation `json:"evaluations"`
	AssumeMonotonic bool                 `json:"assume_monotonic"`
}

type ReplayJobRequest struct {
	Plan         ReplayPlan      `json:"plan"`
	Envelope     json.RawMessage `json:"envelope"`
	GatewayImage string          `json:"gateway_image"`
	AllowedHosts []string        `json:"allowed_hosts"`
}

type ReplayJobResult struct {
	SourceCapsuleID    string        `json:"source_capsule_id"`
	CandidateCapsuleID string        `json:"candidate_capsule_id"`
	ExitCode           int           `json:"exit_code"`
	DurationMillis     int64         `json:"duration_ms"`
	CandidateImage     string        `json:"candidate_image,omitempty"`
	CandidateSource    string        `json:"candidate_source_digest,omitempty"`
	Substitutions      []string      `json:"substitutions,omitempty"`
	LiveBoundaries     []string      `json:"live_boundaries,omitempty"`
	Outcome            string        `json:"outcome"`
	Trials             []ReplayTrial `json:"trials"`
}

type ReplayTrial struct {
	Index              int    `json:"index"`
	CandidateCapsuleID string `json:"candidate_capsule_id"`
	ExitCode           int    `json:"exit_code"`
	DurationMillis     int64  `json:"duration_ms"`
	Outcome            string `json:"outcome"`
}
