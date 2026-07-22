package types

import (
	"encoding/json"
	"time"
)

const SchemaVersion = "1.1"

const (
	PayloadStateCaptured = "captured"
	PayloadStateOmitted  = "omitted"

	OmissionPayloadLimit    = "payload_limit"
	OmissionPolicyExclusion = "policy_exclusion"
	OmissionSerialization   = "serialization_failed"
	OmissionInvalidMetadata = "invalid_omission_metadata"
	PayloadTruncated        = "payload_truncated"
)

type CompletenessState string

const (
	CompletenessReady         CompletenessState = "ready"
	CompletenessIncomplete    CompletenessState = "incomplete"
	CompletenessBlocked       CompletenessState = "blocked"
	CompletenessNotApplicable CompletenessState = "not_applicable"
)

type Completeness struct {
	State            CompletenessState `json:"state"`
	Missing          []MissingItem     `json:"missing,omitempty"`
	Ambiguities      []string          `json:"ambiguities,omitempty"`
	AmbiguityDetails []AmbiguityItem   `json:"ambiguity_details,omitempty"`
}

type CapsuleCompleteness struct {
	SchemaVersion string       `json:"schema_version"`
	CapsuleID     string       `json:"capsule_id"`
	Playback      Completeness `json:"playback"`
	Replay        Completeness `json:"replay"`
	Diff          Completeness `json:"diff"`
	Bisect        Completeness `json:"bisect"`
}

type MissingItem struct {
	Code       string `json:"code"`
	BoundaryID string `json:"boundary_id,omitempty"`
	Message    string `json:"message"`
}

type AmbiguityItem struct {
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
	ModelOverride bool              `json:"model_override,omitempty" yaml:"model_override,omitempty"`
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
	Digest       string `json:"digest,omitempty"`
	MediaType    string `json:"media_type,omitempty"`
	Size         int64  `json:"size"`
	OriginalSize int64  `json:"original_size,omitempty"`
	State        string `json:"state,omitempty"`
	Reason       string `json:"reason,omitempty"`
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

type LogRecord struct {
	ID                     string      `json:"id"`
	SpanID                 string      `json:"span_id,omitempty"`
	ServiceName            string      `json:"service_name,omitempty"`
	Timestamp              time.Time   `json:"timestamp"`
	ObservedTimestamp      *time.Time  `json:"observed_timestamp,omitempty"`
	SeverityNumber         int32       `json:"severity_number"`
	SeverityText           string      `json:"severity_text,omitempty"`
	EventName              string      `json:"event_name,omitempty"`
	Body                   *PayloadRef `json:"body,omitempty"`
	Attributes             *PayloadRef `json:"attributes,omitempty"`
	DroppedAttributesCount uint32      `json:"dropped_attributes_count,omitempty"`
	ScopeName              string      `json:"scope_name,omitempty"`
	ScopeVersion           string      `json:"scope_version,omitempty"`
	ScopeSchemaURL         string      `json:"scope_schema_url,omitempty"`
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
	Truncations   map[string]int `json:"truncations,omitempty"`
	ExportPolicy  string         `json:"export_policy,omitempty"`
}

type PolicyDelta struct {
	Redactions  map[string]int `json:"redactions,omitempty"`
	Exclusions  map[string]int `json:"exclusions,omitempty"`
	Truncations map[string]int `json:"truncations,omitempty"`
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
	Logs            []LogRecord                `json:"logs,omitempty"`
	Boundaries      []Boundary                 `json:"boundaries,omitempty"`
	Privacy         PrivacySummary             `json:"privacy"`
	Evaluations     []Evaluation               `json:"evaluations,omitempty"`
	ReplayHistory   []ReplayRecord             `json:"replay_history,omitempty"`
	Extensions      map[string]json.RawMessage `json:"extensions,omitempty"`
	UnknownFields   map[string]json.RawMessage `json:"-"`
	unknownDocument json.RawMessage
}

type CapsuleSummary struct {
	CapsuleID            string                  `json:"capsule_id"`
	SourceCapsuleID      string                  `json:"source_capsule_id,omitempty"`
	TraceID              string                  `json:"trace_id"`
	ProjectID            string                  `json:"project_id"`
	ServiceName          string                  `json:"service_name"`
	StartTime            time.Time               `json:"start_time"`
	EndTime              time.Time               `json:"end_time"`
	DurationMillis       int64                   `json:"duration_ms"`
	TerminalStatus       string                  `json:"terminal_status"`
	FailureOperation     string                  `json:"failure_operation,omitempty"`
	ReplayReadiness      CompletenessState       `json:"replay_readiness"`
	EvidenceWarningCount int                     `json:"evidence_warning_count"`
	CandidateReplayCount int                     `json:"candidate_replay_count"`
	RequestedModel       string                  `json:"requested_model,omitempty"`
	Completeness         map[string]Completeness `json:"completeness"`
}

type SessionInfo struct {
	SchemaVersion      string   `json:"schema_version"`
	ServiceVersion     string   `json:"service_version"`
	ReleaseVersion     string   `json:"release_version,omitempty"`
	AuthMode           string   `json:"auth_mode"`
	Connected          bool     `json:"connected"`
	LocalModeAvailable bool     `json:"local_mode_available"`
	Scopes             []string `json:"scopes"`
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
	Overrides                 []ReplayOverride        `json:"overrides,omitempty" yaml:"overrides,omitempty"`
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

type JobSummary struct {
	ID                 string       `json:"id"`
	Kind               string       `json:"kind"`
	State              JobState     `json:"state"`
	SourceCapsuleID    string       `json:"source_capsule_id,omitempty"`
	CandidateCapsuleID string       `json:"candidate_capsule_id,omitempty"`
	Outcome            string       `json:"outcome,omitempty"`
	Error              *FlightError `json:"error,omitempty"`
	CreatedAt          time.Time    `json:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at"`
}

type JobStageEvent struct {
	Stage   string `json:"stage"`
	State   string `json:"state"`
	Message string `json:"message"`
}
