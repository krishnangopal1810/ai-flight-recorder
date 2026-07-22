package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/oci"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type EvaluatorService struct {
	repository  repository.Repository
	capsules    *CapsuleService
	diffs       *DiffService
	runner      oci.EvaluatorRunner
	credentials map[string]string
	clock       func() time.Time
}

func NewEvaluatorService(repo repository.Repository, capsules *CapsuleService, diffs *DiffService, runner oci.EvaluatorRunner, credentials map[string]string) *EvaluatorService {
	return &EvaluatorService{repository: repo, capsules: capsules, diffs: diffs, runner: runner, credentials: credentials, clock: func() time.Time { return time.Now().UTC() }}
}

func (s *EvaluatorService) Parse(data []byte) (types.EvaluatorDefinition, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var definition types.EvaluatorDefinition
	if err := decoder.Decode(&definition); err != nil {
		return definition, types.NewError(types.ErrEvaluator, "malformed evaluator definition", "fix the YAML/JSON fields reported by the parser", err)
	}
	if err := s.Validate(definition); err != nil {
		return definition, err
	}
	return definition, nil
}

func (s *EvaluatorService) Validate(definition types.EvaluatorDefinition) error {
	if definition.SchemaVersion != types.SchemaVersion {
		return types.NewError(types.ErrEvaluator, "unsupported evaluator schema version", "set schema_version to 1.0", nil)
	}
	if !identifierPattern.MatchString(definition.ID) || definition.Version == "" {
		return types.NewError(types.ErrEvaluator, "evaluator identity is invalid", "set a stable lowercase id and a non-empty version", nil)
	}
	if definition.Image == "" || len(definition.Command) == 0 {
		return types.NewError(types.ErrEvaluator, "evaluator image and command are required", "provide an OCI image and argv command", nil)
	}
	if !strings.HasPrefix(definition.Image, "sha256:") && !strings.Contains(definition.Image, "@sha256:") {
		return types.NewError(types.ErrEvaluator, "evaluator image is not content-addressed", "pin the evaluator image by sha256 digest", nil)
	}
	if definition.Timeout <= 0 || definition.Timeout > 15*time.Minute {
		return types.NewError(types.ErrEvaluator, "evaluator timeout is outside the allowed range", "set timeout between 1ns and 15m", nil)
	}
	if definition.CPULimit < 0 || definition.MemoryLimitMB < 0 {
		return types.NewError(types.ErrEvaluator, "evaluator resource limits cannot be negative", "set positive CPU and memory limits", nil)
	}
	seen := map[string]bool{}
	for _, name := range definition.Environment {
		if !environmentNamePattern.MatchString(name) || seen[name] {
			return types.NewError(types.ErrEvaluator, "evaluator environment declarations are invalid", "declare each POSIX environment name once; definitions never contain values", nil)
		}
		seen[name] = true
	}
	return nil
}

func (s *EvaluatorService) Run(ctx context.Context, request types.EvaluatorRunRequest, gatewayImage string) (types.EvaluatorRun, error) {
	if err := s.Validate(request.Definition); err != nil {
		return types.EvaluatorRun{}, err
	}
	baseline, err := s.capsules.GetWithoutLogs(ctx, request.BaselineID)
	if err != nil {
		return types.EvaluatorRun{}, err
	}
	candidate, err := s.capsules.GetWithoutLogs(ctx, request.CandidateID)
	if err != nil {
		return types.EvaluatorRun{}, err
	}
	if err := s.repository.PutEvaluator(ctx, baseline.CapsuleID, request.Definition); err != nil {
		return types.EvaluatorRun{}, err
	}
	diff := s.diffs.Compare(baseline, candidate)
	input, err := CanonicalJSON(map[string]any{
		"schema_version": types.SchemaVersion,
		"baseline":       baseline,
		"candidate":      candidate,
		"diff":           diff,
		"labels":         map[string]string{"recorded_values": "evidence", "computed_changes": "observations", "rule_based_explanations": "hypotheses"},
	})
	if err != nil {
		return types.EvaluatorRun{}, err
	}
	environment := map[string]string{}
	for _, name := range request.Definition.Environment {
		value, ok := s.credentials[name]
		if !ok {
			return types.EvaluatorRun{}, types.NewError(types.ErrUnsafeReplay, "declared evaluator credential is unavailable", "set FLIGHT_REPLAY_CREDENTIAL_"+name+" on the worker", nil)
		}
		environment[name] = value
	}
	result, err := s.runner.Evaluate(ctx, oci.EvaluatorSpec{ID: request.Definition.ID, Image: request.Definition.Image, Command: request.Definition.Command, Input: input, Timeout: request.Definition.Timeout, CPULimit: request.Definition.CPULimit, MemoryLimitMB: request.Definition.MemoryLimitMB, AllowedHosts: request.Definition.ModelHosts, GatewayImage: gatewayImage, Environment: environment})
	if err != nil {
		return types.EvaluatorRun{}, err
	}
	policySafeOutput, err := s.capsules.SanitizeEvidence("evaluator.output", result.Stdout)
	if err != nil {
		return types.EvaluatorRun{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(policySafeOutput))
	decoder.DisallowUnknownFields()
	var output types.EvaluatorOutput
	if err := decoder.Decode(&output); err != nil {
		return types.EvaluatorRun{}, types.NewError(types.ErrEvaluator, "evaluator returned invalid JSON", "write exactly one {passed, score?, summary?, evidence?} object to stdout", err)
	}
	if trailingErr := decoder.Decode(&struct{}{}); !errors.Is(trailingErr, io.EOF) {
		return types.EvaluatorRun{}, types.NewError(types.ErrEvaluator, "evaluator returned trailing data", "write exactly one result object to stdout", trailingErr)
	}
	if output.Score != nil && (*output.Score < 0 || *output.Score > 1) {
		return types.EvaluatorRun{}, types.NewError(types.ErrEvaluator, "evaluator score is outside 0..1", "return a normalized score or omit it", nil)
	}
	outcome := "fail"
	if output.Passed {
		outcome = "pass"
	}
	run := types.EvaluatorRun{ID: uuid.NewString(), DefinitionID: request.Definition.ID, DefinitionVersion: request.Definition.Version, BaselineCapsuleID: baseline.CapsuleID, CandidateCapsuleID: candidate.CapsuleID, Outcome: outcome, Output: output, CreatedAt: s.clock()}
	if err := s.repository.PutEvaluatorRun(ctx, run); err != nil {
		return types.EvaluatorRun{}, err
	}
	return run, nil
}

func (s *EvaluatorService) Runs(ctx context.Context, capsuleID string) ([]types.EvaluatorRun, error) {
	if strings.TrimSpace(capsuleID) == "" {
		return nil, types.NewError(types.ErrValidation, "capsule ID is required", "provide the source capsule ID", nil)
	}
	return s.repository.ListEvaluatorRuns(ctx, capsuleID)
}

func (s *EvaluatorService) Definition(ctx context.Context, id, version string) (types.EvaluatorDefinition, error) {
	if !identifierPattern.MatchString(id) {
		return types.EvaluatorDefinition{}, types.NewError(types.ErrValidation, "evaluator ID is invalid", "provide the stable evaluator ID", nil)
	}
	return s.repository.GetEvaluator(ctx, id, version)
}

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{1,63}$`)
var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
