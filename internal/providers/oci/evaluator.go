package oci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type EvaluatorSpec struct {
	ID            string
	Image         string
	Command       []string
	Input         []byte
	Timeout       time.Duration
	CPULimit      float64
	MemoryLimitMB int64
	AllowedHosts  []string
	GatewayImage  string
	Environment   map[string]string
}

type EvaluatorResult struct {
	Stdout   []byte
	ExitCode int
	Duration time.Duration
}

type EvaluatorRunner interface {
	Available(context.Context) error
	Evaluate(context.Context, EvaluatorSpec) (EvaluatorResult, error)
}

func (r *DockerRunner) Evaluate(ctx context.Context, spec EvaluatorSpec) (EvaluatorResult, error) {
	if spec.ID == "" || spec.Image == "" || len(spec.Command) == 0 {
		return EvaluatorResult{}, types.NewError(types.ErrValidation, "incomplete evaluator OCI specification", "provide an evaluator ID, image, and argv command", nil)
	}
	if err := r.Available(ctx); err != nil {
		return EvaluatorResult{}, err
	}
	name := "flight-evaluator-" + safeName(spec.ID)
	cleanup := func() {
		_, _ = r.command(context.Background(), "rm", "-f", name, name+"-gateway")
		_, _ = r.command(context.Background(), "network", "rm", name)
	}
	defer cleanup()

	args := []string{"run", "-i", "--name", name, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--user", "65532:65532", "--tmpfs", "/tmp:rw,noexec,nosuid,size=32m"}
	allowed := uniqueHosts(spec.AllowedHosts)
	if len(allowed) == 0 {
		args = append(args, "--network", "none")
	} else {
		if spec.GatewayImage == "" {
			return EvaluatorResult{}, types.NewError(types.ErrUnsafeReplay, "model-assisted evaluator has no gateway image", "configure the Flight gateway image", nil)
		}
		if output, err := r.command(ctx, "network", "create", "--internal", name); err != nil {
			return EvaluatorResult{}, types.NewError(types.ErrUnsafeReplay, "cannot create evaluator isolation network", "verify OCI network support", fmt.Errorf("%s: %w", output, err))
		}
		gatewayArgs := []string{"run", "-d", "--name", name + "-gateway", "--network", name, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "128m", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", "-e", "FLIGHT_GATEWAY_ALLOWED_HOSTS=" + strings.Join(allowed, ","), "--entrypoint", "/flight-gateway", spec.GatewayImage}
		if output, err := r.command(ctx, gatewayArgs...); err != nil {
			return EvaluatorResult{}, types.NewError(types.ErrUnsafeReplay, "cannot start evaluator gateway", "build the configured Flight gateway image", fmt.Errorf("%s: %w", output, err))
		}
		if err := r.waitForGateway(ctx, name+"-gateway"); err != nil {
			return EvaluatorResult{}, err
		}
		if output, err := r.command(ctx, "network", "connect", r.egressNetwork(), name+"-gateway"); err != nil {
			return EvaluatorResult{}, types.NewError(types.ErrUnsafeReplay, "cannot attach evaluator gateway egress", "verify the OCI default bridge", fmt.Errorf("%s: %w", output, err))
		}
		args = append(args, "--network", name, "-e", "HTTP_PROXY=http://"+name+"-gateway:8080", "-e", "HTTPS_PROXY=http://"+name+"-gateway:8080", "-e", "NO_PROXY="+name+"-gateway")
	}
	if spec.CPULimit > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%.2f", spec.CPULimit))
	}
	if spec.MemoryLimitMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", spec.MemoryLimitMB))
	}
	keys := make([]string, 0, len(spec.Environment))
	for key := range spec.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !validEnvironmentName(key) {
			return EvaluatorResult{}, types.NewError(types.ErrValidation, "invalid evaluator environment name", "use POSIX environment variable names", nil)
		}
		args = append(args, "-e", key)
	}
	args = append(args, "--entrypoint", spec.Command[0], spec.Image)
	args = append(args, spec.Command[1:]...)

	runCtx := ctx
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	command := exec.CommandContext(runCtx, r.Binary, args...)
	command.Env = append([]string(nil), os.Environ()...)
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+spec.Environment[key])
	}
	command.Stdin = bytes.NewReader(spec.Input)
	var stdout, stderr cappedBuffer
	stdout.limit, stderr.limit = 4<<20, 64<<10
	command.Stdout, command.Stderr = &stdout, &stderr
	start := time.Now()
	err := command.Run()
	result := EvaluatorResult{Stdout: stdout.Bytes(), Duration: time.Since(start)}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.ExitCode = exit.ExitCode()
			return result, types.NewError(types.ErrEvaluator, "evaluator exited unsuccessfully", fmt.Sprintf("inspect evaluator %s; exit code %d", spec.ID, result.ExitCode), nil)
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return result, types.NewError(types.ErrEvaluator, "evaluator timed out", "increase its declared timeout only after reviewing the command", nil)
		}
		return result, types.NewError(types.ErrEvaluator, "cannot execute evaluator", "inspect the OCI runtime and evaluator image", err)
	}
	return result, nil
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		return len(value), nil
	}
	if len(value) > remaining {
		_, _ = b.Buffer.Write(value[:remaining])
		return len(value), nil
	}
	return b.Buffer.Write(value)
}
