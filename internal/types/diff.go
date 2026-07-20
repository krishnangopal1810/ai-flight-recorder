package types

type EvidenceRef struct {
	CapsuleID string `json:"capsule_id"`
	SpanID    string `json:"span_id,omitempty"`
	Field     string `json:"field,omitempty"`
	Payload   string `json:"payload_digest,omitempty"`
}

type Observation struct {
	Kind       string        `json:"kind"`
	Path       string        `json:"path"`
	Before     any           `json:"before,omitempty"`
	After      any           `json:"after,omitempty"`
	Rule       string        `json:"rule"`
	Confidence float64       `json:"confidence"`
	Evidence   []EvidenceRef `json:"evidence"`
}

type Hypothesis struct {
	Summary       string        `json:"summary"`
	Supporting    []EvidenceRef `json:"supporting"`
	Contradicting []EvidenceRef `json:"contradicting,omitempty"`
	Validated     bool          `json:"validated"`
}

type Alignment struct {
	BaselineSpan  string   `json:"baseline_span,omitempty"`
	CandidateSpan string   `json:"candidate_span,omitempty"`
	Kind          string   `json:"kind"`
	Confidence    float64  `json:"confidence"`
	Alternatives  []string `json:"alternatives,omitempty"`
}

type DiffResult struct {
	BaselineCapsuleID    string        `json:"baseline_capsule_id"`
	CandidateCapsuleID   string        `json:"candidate_capsule_id"`
	Alignments           []Alignment   `json:"alignments"`
	FirstDivergence      *Observation  `json:"first_divergence,omitempty"`
	Observations         []Observation `json:"observations"`
	Hypotheses           []Hypothesis  `json:"hypotheses,omitempty"`
	AlignmentConfidence  float64       `json:"alignment_confidence"`
	FirstDivergenceKnown bool          `json:"first_divergence_known"`
}
