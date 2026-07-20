package oci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type RunSpec struct {
	JobID          string
	Image          string
	GatewayImage   string
	Command        []string
	WorkingDir     string
	Input          []byte
	ReplayEnvelope []byte
	Environment    map[string]string
	AllowedHosts   []string
	Timeout        time.Duration
	CPULimit       float64
	MemoryLimitMB  int64
}

type RunResult struct {
	Stdout      []byte
	Stderr      []byte
	ExitCode    int
	OTLPBatches [][]byte
	Duration    time.Duration
}

type Runner interface {
	Available(context.Context) error
	Run(context.Context, RunSpec) (RunResult, error)
}

type DockerRunner struct{ Binary string }

func NewDockerRunner(binary string) *DockerRunner {
	if binary == "" {
		binary = "docker"
	}
	return &DockerRunner{Binary: binary}
}

func (r *DockerRunner) Available(ctx context.Context) error {
	command := exec.CommandContext(ctx, r.Binary, "version", "--format", "{{.Server.Version}}")
	if output, err := command.CombinedOutput(); err != nil {
		return types.NewError(types.ErrUnsafeReplay, "OCI runtime is unavailable", "start Docker or Podman and verify the daemon is reachable", fmt.Errorf("%s: %w", strings.TrimSpace(string(output)), err))
	}
	return nil
}

func (r *DockerRunner) Run(ctx context.Context, spec RunSpec) (RunResult, error) {
	if err := validateSpec(spec); err != nil {
		return RunResult{}, err
	}
	if err := r.Available(ctx); err != nil {
		return RunResult{}, err
	}
	root, err := os.MkdirTemp("", "flight-replay-"+safeName(spec.JobID)+"-")
	if err != nil {
		return RunResult{}, types.NewError(types.ErrStorage, "cannot create replay workspace", "check temporary directory permissions", err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0o700); err != nil {
		return RunResult{}, err
	}
	planPath := filepath.Join(root, "replay-plan.json")
	if err := os.WriteFile(planPath, spec.ReplayEnvelope, 0o400); err != nil {
		return RunResult{}, types.NewError(types.ErrStorage, "cannot stage replay plan", "check temporary directory permissions", err)
	}
	outputDir := filepath.Join(root, "output")
	if err := os.Mkdir(outputDir, 0o777); err != nil {
		return RunResult{}, err
	}
	if err := os.Chmod(outputDir, 0o777); err != nil {
		return RunResult{}, err
	}

	network := "flight-" + safeName(spec.JobID)
	gateway := network + "-gateway"
	application := network + "-app"
	cleanup := func() {
		_, _ = r.command(context.Background(), "rm", "-f", application, gateway)
		_, _ = r.command(context.Background(), "network", "rm", network)
	}
	defer cleanup()
	if output, err := r.command(ctx, "network", "create", "--internal", network); err != nil {
		return RunResult{}, types.NewError(types.ErrUnsafeReplay, "cannot create isolated replay network", "verify the OCI runtime supports internal networks", fmt.Errorf("%s: %w", output, err))
	}

	allowed := uniqueHosts(spec.AllowedHosts)
	gatewayArgs := []string{"run", "-d", "--name", gateway, "--network", network, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "64", "--memory", "128m", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", "--mount", "type=bind,src=" + outputDir + ",dst=/output", "-e", "FLIGHT_GATEWAY_ALLOWED_HOSTS=" + strings.Join(allowed, ","), "-e", "FLIGHT_GATEWAY_OUTPUT=/output", spec.GatewayImage, "/flight-gateway"}
	if output, err := r.command(ctx, gatewayArgs...); err != nil {
		return RunResult{}, types.NewError(types.ErrUnsafeReplay, "cannot start replay gateway", "build the configured Flight gateway image", fmt.Errorf("%s: %w", output, err))
	}
	if len(allowed) > 0 {
		if output, err := r.command(ctx, "network", "connect", r.egressNetwork(), gateway); err != nil {
			return RunResult{}, types.NewError(types.ErrUnsafeReplay, "cannot attach the gateway egress network", "verify the OCI runtime default bridge exists", fmt.Errorf("%s: %w", output, err))
		}
	}

	args := []string{"run", "--name", application, "--network", network, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--pids-limit", "128", "--user", "65532:65532", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m", "--tmpfs", "/run/flight/workspace:rw,nosuid,size=256m", "--mount", "type=bind,src=" + planPath + ",dst=/run/flight/replay-plan.json,readonly", "-e", "FLIGHT_REPLAY_PLAN=/run/flight/replay-plan.json", "-e", "FLIGHT_WORKSPACE=/run/flight/workspace", "-e", "FLIGHT_CAPTURE=true", "-e", "OTEL_EXPORTER_OTLP_ENDPOINT=http://" + gateway + ":8080", "-e", "HTTP_PROXY=http://" + gateway + ":8080", "-e", "HTTPS_PROXY=http://" + gateway + ":8080", "-e", "NO_PROXY=" + gateway}
	if spec.WorkingDir != "" {
		args = append(args, "--workdir", spec.WorkingDir)
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
			return RunResult{}, types.NewError(types.ErrValidation, "invalid replay environment name", "use POSIX environment variable names", nil)
		}
		// Pass only the name in argv. The Docker/Podman client reads the value
		// from its own environment, keeping replay credentials out of process
		// arguments, job records, and command diagnostics.
		args = append(args, "-e", key)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Command...)
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
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	start := time.Now()
	runErr := command.Run()
	duration := time.Since(start)
	result := RunResult{Stdout: stdout.Bytes(), Stderr: safeStderr(stderr.Bytes()), Duration: duration}
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			result.ExitCode = exit.ExitCode()
		} else if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return result, types.NewError(types.ErrBuild, "replay timed out", "increase the declared timeout only after inspecting the candidate logs", runErr)
		} else {
			return result, types.NewError(types.ErrBuild, "cannot execute replay container", "inspect the OCI runtime and image", runErr)
		}
	}
	files, _ := filepath.Glob(filepath.Join(outputDir, "*.otlp.pb"))
	for _, filename := range files {
		data, err := os.ReadFile(filename)
		if err != nil {
			return result, types.NewError(types.ErrStorage, "cannot read replay OTLP output", "inspect gateway filesystem permissions", err)
		}
		if len(data) > 64<<20 {
			return result, types.NewError(types.ErrValidation, "replay OTLP output exceeds the batch limit", "reduce candidate exporter batch size", nil)
		}
		result.OTLPBatches = append(result.OTLPBatches, data)
	}
	return result, nil
}

func (r *DockerRunner) egressNetwork() string {
	if strings.Contains(filepath.Base(r.Binary), "podman") {
		return "podman"
	}
	return "bridge"
}

func validateSpec(spec RunSpec) error {
	if spec.JobID == "" || spec.Image == "" || spec.GatewayImage == "" || len(spec.Command) == 0 {
		return types.NewError(types.ErrValidation, "incomplete OCI replay specification", "provide job ID, application image, gateway image, and argv command", nil)
	}
	if len(spec.ReplayEnvelope) == 0 {
		return types.NewError(types.ErrValidation, "replay envelope is empty", "materialize and verify an immutable replay plan before execution", nil)
	}
	return nil
}

func (r *DockerRunner) command(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, r.Binary, args...)
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

var safePattern = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)
var environmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func safeName(value string) string {
	value = strings.Trim(safePattern.ReplaceAllString(value, "-"), "-")
	if len(value) > 40 {
		value = value[:40]
	}
	if value == "" {
		return "job"
	}
	return value
}
func validEnvironmentName(value string) bool { return environmentPattern.MatchString(value) }
func uniqueHosts(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
func safeStderr(value []byte) []byte {
	if len(value) > 64<<10 {
		value = value[len(value)-(64<<10):]
	}
	return value
}
