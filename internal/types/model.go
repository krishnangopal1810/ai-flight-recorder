package types

import (
	"encoding/json"
	"time"
)

const SchemaVersion = "1.0"

type CompletenessState string

const (
	CompletenessReady         CompletenessState = "ready"
	CompletenessIncomplete    CompletenessState = "incomplete"
	CompletenessBlocked       CompletenessState = "blocked"
	CompletenessNotApplicable CompletenessState = "not_applicable"
)

type Completeness struct {
	State       CompletenessState `json:"state"`
	Missing     []MissingItem     `json:"missing,omitempty"`
	Ambiguities []string          `json:"ambiguities,omitempty"`
}

type MissingItem struct {
	Code       string `json:"code"`
	BoundaryID string `json:"boundary_id,omitempty"`
	Message    string `json:"message"`
}

type Integrity struct {
	Algorithm      string   `json:"algorithm"`
	ManifestDigest string   `json:"manifest_digest"`
	PayloadDigests []string `json:"payload_digests"`
	RootDigest     string   `json:"root_digest"`
}

type Provenance struct {
	RepositoryURL    string            `json:"repository_url,omitempty"`
	ProjectID        string            `json:"project_id,omitempty"`
	CommitSHA        string            `json:"commit_sha,omitempty"`
	DirtyDigest      string            `json:"dirty_digest,omitempty"`
	BuildID          string            `json:"build_id,omitempty"`
	ContainerID      string            `json:"container_id,omitempty"`
	Runtime          string            `json:"runtime,omitempty"`
	DependencyLock   map[string]string `json:"dependency_lock,omitempty"`
	FeatureFlags     map[string]any    `json:"feature_flags,omitempty"`
	EnvironmentFacts map[string]string `json:"environment_facts,omitempty"`
	Entrypoint       *Entrypoint       `json:"entrypoint,omitempty"`
	Missing          []string          `json:"missing,omitempty"`
}

type Entrypoint struct {
	Command       []string          `json:"command" yaml:"command"`
	WorkingDir    string            `json:"working_dir,omitempty" yaml:"working_dir,omitempty"`
	Image         string            `json:"image,omitempty" yaml:"image,omitempty"`
	Dockerfile    string            `json:"dockerfile,omitempty" yaml:"dockerfile,omitempty"`
	Context       string            `json:"context,omitempty" yaml:"context,omitempty"`
	InputMode     string            `json:"input_mode" yaml:"input_mode"`
	OutputMode    string            `json:"output_mode" yaml:"output_mode"`
	Timeout       time.Duration     `json:"timeout" yaml:"timeout"`
	Environment   []string          `json:"environment,omitempty" yaml:"environment,omitempty"`
	ModelHosts    []string          `json:"model_hosts,omitempty" yaml:"model_hosts,omitempty"`
	Sandboxes     map[string]string `json:"sandboxes,omitempty" yaml:"sandboxes,omitempty"`
	CPULimit      float64           `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
	MemoryLimitMB int64             `json:"memory_limit_mb,omitempty" yaml:"memory_limit_mb,omitempty"`
}

type ModelConfiguration struct {
	Provider              string         `json:"provider,omitempty"`
	RequestedModel        string         `json:"requested_model,omitempty"`
	ProviderReportedModel string         `json:"provider_reported_model,omitempty"`
	Parameters            map[string]any `json:"parameters,omitempty"`
	ToolDefinitions       []PayloadRef   `json:"tool_definitions,omitempty"`
	ResponseMetadata      map[string]any `json:"response_metadata,omitempty"`
}

type PayloadRef struct {
	Digest    string `json:"digest"`
	MediaType string `json:"media_type,omitempty"`
	Size      int64  `json:"size"`
	State     string `json:"state,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type Artifact struct {
	ID          string      `json:"id"`
	Type        string      `json:"type"`
	LogicalName string      `json:"logical_name"`
	Version     string      `json:"version,omitempty"`
	MediaType   string      `json:"media_type,omitempty"`
	State       string      `json:"state"`
	Payload     *PayloadRef `json:"payload,omitempty"`
}

type Operation struct {
	SpanID                    string            `json:"span_id"`
	ParentSpanID              string            `json:"parent_span_id,omitempty"`
	LogicalKey                string            `json:"logical_key,omitempty"`
	Kind                      string            `json:"kind"`
	Name                      string            `json:"name"`
	StartTime                 time.Time         `json:"start_time"`
	EndTime                   time.Time         `json:"end_time"`
	Input                     *PayloadRef       `json:"input,omitempty"`
	Output                    *PayloadRef       `json:"output,omitempty"`
	Status                    string            `json:"status"`
	SemanticConventionVersion string            `json:"semantic_convention_version,omitempty"`
	Attributes                map[string]string `json:"attributes,omitempty"`
	Sequence                  int               `json:"sequence"`
}

type Boundary struct {
	ID               string       `json:"id"`
	OperationSpanID  string       `json:"operation_span_id"`
	LogicalKey       string       `json:"logical_key"`
	ParentLogicalKey string       `json:"parent_logical_key,omitempty"`
	Class            string       `json:"class"`
	RequestDigest    string       `json:"request_digest,omitempty"`
	Response         *PayloadRef  `json:"response,omitempty"`
	SideEffect       string       `json:"side_effect"`
	ReplayPolicy     string       `json:"replay_policy"`
	MatchingRule     MatchingRule `json:"matching_rule"`
	Sandbox          string       `json:"sandbox,omitempty"`
	Occurrence       int          `json:"occurrence"`
}

type MatchingRule struct {
	Mode        string   `json:"mode"`
	IgnorePaths []string `json:"ignore_paths,omitempty"`
}

type PrivacySummary struct {
	PolicyVersion string         `json:"policy_version,omitempty"`
	PolicyDigest  string         `json:"policy_digest,omitempty"`
	Mode          string         `json:"mode"`
	Redactions    map[string]int `json:"redactions,omitempty"`
	Exclusions    map[string]int `json:"exclusions,omitempty"`
	ExportPolicy  string         `json:"export_policy,omitempty"`
}

type Evaluation struct {
	ID        string       `json:"id"`
	Version   string       `json:"version"`
	Inputs    []PayloadRef `json:"inputs,omitempty"`
	Result    any          `json:"result,omitempty"`
	Score     *float64     `json:"score,omitempty"`
	Threshold *float64     `json:"threshold,omitempty"`
	Evidence  *PayloadRef  `json:"evidence,omitempty"`
}

type ReplayRecord struct {
	PlanDigest       string   `json:"plan_digest"`
	Substitutions    []string `json:"substitutions,omitempty"`
	LiveBoundaries   []string `json:"live_boundaries,omitempty"`
	CandidateCapsule string   `json:"candidate_capsule,omitempty"`
	TrialIndex       int      `json:"trial_index,omitempty"`
	Outcome          string   `json:"outcome,omitempty"`
}

type RunCapsule struct {
	SchemaVersion   string                     `json:"schema_version"`
	CapsuleID       string                     `json:"capsule_id"`
	SourceCapsuleID string                     `json:"source_capsule_id,omitempty"`
	TraceID         string                     `json:"trace_id"`
	ProjectID       string                     `json:"project_id"`
	ServiceName     string                     `json:"service_name"`
	StartTime       time.Time                  `json:"start_time"`
	EndTime         time.Time                  `json:"end_time"`
	ExpiresAt       *time.Time                 `json:"expires_at,omitempty"`
	TerminalStatus  string                     `json:"terminal_status"`
	Integrity       Integrity                  `json:"integrity"`
	Completeness    map[string]Completeness    `json:"completeness"`
	Provenance      Provenance                 `json:"provenance"`
	Model           ModelConfiguration         `json:"model_configuration,omitempty"`
	Artifacts       []Artifact                 `json:"artifacts,omitempty"`
	Operations      []Operation                `json:"operations"`
	Boundaries      []Boundary                 `json:"boundaries,omitempty"`
	Privacy         PrivacySummary             `json:"privacy"`
	Evaluations     []Evaluation               `json:"evaluations,omitempty"`
	ReplayHistory   []ReplayRecord             `json:"replay_history,omitempty"`
	Extensions      map[string]json.RawMessage `json:"extensions,omitempty"`
	UnknownFields   map[string]json.RawMessage `json:"-"`
	unknownDocument json.RawMessage
}

type CapsuleSummary struct {
	CapsuleID      string                  `json:"capsule_id"`
	TraceID        string                  `json:"trace_id"`
	ProjectID      string                  `json:"project_id"`
	ServiceName    string                  `json:"service_name"`
	StartTime      time.Time               `json:"start_time"`
	TerminalStatus string                  `json:"terminal_status"`
	Completeness   map[string]Completeness `json:"completeness"`
}

type ReplayPlan struct {
	SchemaVersion             string                  `json:"schema_version" yaml:"schema_version"`
	SourceCapsuleID           string                  `json:"source_capsule_id" yaml:"source_capsule_id"`
	Model                     string                  `json:"model,omitempty" yaml:"model,omitempty"`
	Revision                  string                  `json:"revision,omitempty" yaml:"revision,omitempty"`
	CandidateDirtyDigest      string                  `json:"candidate_dirty_digest,omitempty" yaml:"candidate_dirty_digest,omitempty"`
	CandidateSourceDigest     string                  `json:"candidate_source_digest,omitempty" yaml:"candidate_source_digest,omitempty"`
	Entrypoint                Entrypoint              `json:"entrypoint" yaml:"entrypoint"`
	OriginalInput             *PayloadRef             `json:"original_input,omitempty" yaml:"original_input,omitempty"`
	SubstituteLLM             bool                    `json:"substitute_llm" yaml:"substitute_llm"`
	AllowSandboxedSideEffects bool                    `json:"allow_sandboxed_side_effects" yaml:"allow_sandboxed_side_effects"`
	MatchingRules             map[string]MatchingRule `json:"matching_rules,omitempty" yaml:"matching_rules,omitempty"`
	EvaluatorID               string                  `json:"evaluator_id,omitempty" yaml:"evaluator_id,omitempty"`
	Repetitions               int                     `json:"repetitions" yaml:"repetitions"`
	PassThreshold             float64                 `json:"pass_threshold" yaml:"pass_threshold"`
	ExpectedArtifacts         []string                `json:"expected_artifacts,omitempty" yaml:"expected_artifacts,omitempty"`
	Environment               []string                `json:"environment,omitempty" yaml:"environment,omitempty"`
	PlanDigest                string                  `json:"plan_digest" yaml:"plan_digest"`
}

type JobState string

const (
	JobQueued       JobState = "queued"
	JobRunning      JobState = "running"
	JobCompleted    JobState = "completed"
	JobFailed       JobState = "failed"
	JobBlocked      JobState = "blocked"
	JobInconclusive JobState = "inconclusive"
)

type Job struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	State          JobState        `json:"state"`
	Request        json.RawMessage `json:"request"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          *FlightError    `json:"error,omitempty"`
	LeaseOwner     string          `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type JobEvent struct {
	Sequence int64           `json:"sequence"`
	JobID    string          `json:"job_id"`
	Type     string          `json:"type"`
	Data     json.RawMessage `json:"data,omitempty"`
	Time     time.Time       `json:"time"`
}
