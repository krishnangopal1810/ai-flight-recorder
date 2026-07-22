package types

import "fmt"

type ErrorCode string

const (
	ErrValidation         ErrorCode = "validation"
	ErrIncompleteCapsule  ErrorCode = "incomplete_capsule"
	ErrUnmatchedBoundary  ErrorCode = "unmatched_boundary"
	ErrOverrideNotApplied ErrorCode = "override_not_applied"
	ErrUnsafeReplay       ErrorCode = "unsafe_replay"
	ErrAuthentication     ErrorCode = "authentication"
	ErrStorage            ErrorCode = "storage"
	ErrBuild              ErrorCode = "build"
	ErrEvaluator          ErrorCode = "evaluator"
	ErrInconclusive       ErrorCode = "inconclusive"
	ErrNotFound           ErrorCode = "not_found"
	ErrConflict           ErrorCode = "conflict"
)

type FlightError struct {
	Code        ErrorCode      `json:"code"`
	Message     string         `json:"message"`
	Remediation string         `json:"remediation,omitempty"`
	Details     map[string]any `json:"details,omitempty"`
	Cause       error          `json:"-"`
}

func (e *FlightError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
}

func (e *FlightError) Unwrap() error { return e.Cause }

func NewError(code ErrorCode, message, remediation string, cause error) *FlightError {
	return &FlightError{Code: code, Message: message, Remediation: remediation, Cause: cause}
}
