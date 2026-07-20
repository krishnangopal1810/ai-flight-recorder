package types

import (
	"encoding/json"
	"time"
)

type EvaluatorDefinition struct {
	SchemaVersion string        `json:"schema_version" yaml:"schema_version"`
	ID            string        `json:"id" yaml:"id"`
	Version       string        `json:"version" yaml:"version"`
	Image         string        `json:"image" yaml:"image"`
	Command       []string      `json:"command" yaml:"command"`
	Timeout       time.Duration `json:"timeout" yaml:"timeout"`
	CPULimit      float64       `json:"cpu_limit,omitempty" yaml:"cpu_limit,omitempty"`
	MemoryLimitMB int64         `json:"memory_limit_mb,omitempty" yaml:"memory_limit_mb,omitempty"`
	ModelHosts    []string      `json:"model_hosts,omitempty" yaml:"model_hosts,omitempty"`
	Environment   []string      `json:"environment,omitempty" yaml:"environment,omitempty"`
}

type EvaluatorRunRequest struct {
	Definition  EvaluatorDefinition `json:"definition"`
	BaselineID  string              `json:"baseline_capsule_id"`
	CandidateID string              `json:"candidate_capsule_id"`
}

type EvaluatorOutput struct {
	Passed   bool            `json:"passed"`
	Score    *float64        `json:"score,omitempty"`
	Summary  string          `json:"summary,omitempty"`
	Evidence json.RawMessage `json:"evidence,omitempty"`
}

type EvaluatorRun struct {
	ID                 string          `json:"id"`
	DefinitionID       string          `json:"definition_id"`
	DefinitionVersion  string          `json:"definition_version"`
	BaselineCapsuleID  string          `json:"baseline_capsule_id"`
	CandidateCapsuleID string          `json:"candidate_capsule_id"`
	Outcome            string          `json:"outcome"`
	Output             EvaluatorOutput `json:"output"`
	CreatedAt          time.Time       `json:"created_at"`
}

type RevisionOutcome string

const (
	RevisionGood         RevisionOutcome = "good"
	RevisionBad          RevisionOutcome = "bad"
	RevisionInconclusive RevisionOutcome = "inconclusive"
)

type RevisionEvaluation struct {
	Revision   string          `json:"revision"`
	Outcome    RevisionOutcome `json:"outcome"`
	Passes     int             `json:"passes"`
	Trials     int             `json:"trials"`
	Confidence float64         `json:"confidence"`
	Evidence   []string        `json:"evidence,omitempty"`
}

type BisectResult struct {
	Outcome       string               `json:"outcome"`
	FirstBad      string               `json:"first_bad,omitempty"`
	Evaluations   []RevisionEvaluation `json:"evaluations"`
	NonMonotonic  bool                 `json:"non_monotonic"`
	ConditionalOn string               `json:"conditional_on,omitempty"`
	Message       string               `json:"message,omitempty"`
}
