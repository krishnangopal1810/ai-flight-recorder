package oci

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestDockerRunnerTransfersReplayFilesThroughOCIAPI(t *testing.T) {
	runner, logPath := fakeDockerRunner(t, false)
	result, err := runner.Run(context.Background(), RunSpec{
		JobID:          "remote-daemon-contract",
		Image:          "candidate:test",
		GatewayImage:   "gateway:test",
		Command:        []string{"/agent"},
		Input:          []byte(`{"prompt":"hello"}`),
		ReplayEnvelope: []byte(`{"schema_version":"1.0","boundaries":[]}`),
		Timeout:        time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("unexpected exit code %d", result.ExitCode)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	if strings.Contains(logText, "type=bind") {
		t.Fatalf("runner used a daemon-host bind mount:\n%s", logText)
	}
	if strings.Contains(logText, "network connect") {
		t.Fatalf("gateway joined an egress network without allowed hosts:\n%s", logText)
	}
	for _, required := range []string{
		"run -i --name flight-remote-daemon-contract-app",
		"volume create flight-remote-daemon-contract-plan",
		"cp ",
		"type=volume,src=flight-remote-daemon-contract-plan,dst=/run/flight/plan,readonly",
		"FLIGHT_REPLAY_PLAN=/run/flight/plan/replay-plan.json",
		"--entrypoint /agent candidate:test",
		"--entrypoint /flight-gateway gateway:test",
		"exec flight-remote-daemon-contract-gateway /flight-gateway healthcheck",
		"exec flight-remote-daemon-contract-gateway /flight-gateway export-captures",
		"volume rm -f flight-remote-daemon-contract-plan",
	} {
		if !strings.Contains(logText, required) {
			t.Fatalf("runner command log is missing %q:\n%s", required, logText)
		}
	}
}

func TestDockerRunnerReportsApplicationTimeout(t *testing.T) {
	runner, _ := fakeDockerRunner(t, true)
	_, err := runner.Run(context.Background(), RunSpec{
		JobID:          "timeout-contract",
		Image:          "candidate:test",
		GatewayImage:   "gateway:test",
		Command:        []string{"/agent"},
		ReplayEnvelope: []byte(`{"schema_version":"1.0","boundaries":[]}`),
		Timeout:        20 * time.Millisecond,
	})
	var flightErr *types.FlightError
	if !errors.As(err, &flightErr) || flightErr.Code != types.ErrBuild || flightErr.Message != "replay timed out" {
		t.Fatalf("expected typed replay timeout, got %v", err)
	}
}

func TestDockerRunnerStartsEvaluatorGatewayWithExplicitEntrypoint(t *testing.T) {
	runner, logPath := fakeDockerRunner(t, false)
	_, err := runner.Evaluate(context.Background(), EvaluatorSpec{
		ID:           "gateway-contract",
		Image:        "evaluator:test",
		GatewayImage: "gateway:test",
		Command:      []string{"/evaluator"},
		AllowedHosts: []string{"models.example"},
		Timeout:      time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"run -i --name flight-evaluator-gateway-contract",
		"--entrypoint /flight-gateway gateway:test",
		"exec flight-evaluator-gateway-contract-gateway /flight-gateway healthcheck",
		"network connect customer-egress flight-evaluator-gateway-contract-gateway",
		"--entrypoint /evaluator evaluator:test",
	} {
		if !strings.Contains(string(logData), required) {
			t.Fatalf("evaluator command log is missing %q:\n%s", required, logData)
		}
	}
}

func TestLimitedOCIOutputStopsAtDeclaredBound(t *testing.T) {
	var output limitedOutput
	output.limit = 3
	written, err := output.Write([]byte("oversized"))
	if !errors.Is(err, errOutputLimit) || written != 3 || output.String() != "ove" || !output.exceeded {
		t.Fatalf("unexpected bounded output result: written=%d output=%q exceeded=%t err=%v", written, output.String(), output.exceeded, err)
	}
}

func fakeDockerRunner(t *testing.T, slowApplication bool) (*DockerRunner, string) {
	t.Helper()
	root := t.TempDir()
	logPath := filepath.Join(root, "calls.log")
	binary := filepath.Join(root, "docker")
	sleep := ":"
	if slowApplication {
		sleep = "exec sleep 1"
	}
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$FLIGHT_FAKE_OCI_LOG"
if [ "${1:-}" = "version" ]; then
  printf '28.0.0\n'
  exit 0
fi
if [ "${1:-}" = "run" ] && [ "${2:-}" != "-d" ]; then
  ` + sleep + `
fi
exit 0
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLIGHT_FAKE_OCI_LOG", logPath)
	return NewDockerRunner(binary, "customer-egress"), logPath
}
