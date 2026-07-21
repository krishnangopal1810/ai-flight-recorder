package repository

import (
	"context"
	"errors"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

var ErrPayloadCorrupt = errors.New("stored payload digest mismatch")

type Token struct {
	ID        string
	Name      string
	Hash      string
	Scopes    []string
	CreatedAt time.Time
	RevokedAt *time.Time
}

type QuarantineRecord struct {
	ID        string         `json:"id"`
	Reason    string         `json:"reason"`
	Safe      map[string]any `json:"safe_metadata"`
	CreatedAt time.Time      `json:"created_at"`
}

type AuditEvent struct {
	Sequence  int64          `json:"sequence"`
	Kind      string         `json:"kind"`
	Safe      map[string]any `json:"safe_metadata"`
	CreatedAt time.Time      `json:"created_at"`
}

type Repository interface {
	Close() error
	PutCapsule(context.Context, types.RunCapsule, map[string][]byte) error
	GetCapsule(context.Context, string) (types.RunCapsule, error)
	ListCapsules(context.Context, string, int) ([]types.CapsuleSummary, error)
	LoadPayload(context.Context, string) ([]byte, error)
	PutQuarantine(context.Context, QuarantineRecord) error
	DeleteExpired(context.Context, time.Time) (int, error)

	CreateToken(context.Context, Token) error
	GetTokenByHash(context.Context, string) (Token, error)
	RevokeToken(context.Context, string) error
	ListAuditEvents(context.Context, int64) ([]AuditEvent, error)

	PutJob(context.Context, types.Job) error
	GetJob(context.Context, string) (types.Job, error)
	ClaimJob(context.Context, string, time.Time) (*types.Job, error)
	RenewJobLease(context.Context, string, string, time.Time) error
	UpdateJob(context.Context, types.Job) error
	AppendJobEvent(context.Context, types.JobEvent) error
	ListJobEvents(context.Context, string, int64) ([]types.JobEvent, error)

	PutEvaluator(context.Context, string, types.EvaluatorDefinition) error
	GetEvaluator(context.Context, string, string) (types.EvaluatorDefinition, error)
	PutEvaluatorRun(context.Context, types.EvaluatorRun) error
	ListEvaluatorRuns(context.Context, string) ([]types.EvaluatorRun, error)
}
