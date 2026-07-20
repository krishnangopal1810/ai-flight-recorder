//go:build security

package security_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/oci"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRT08SeededSecretsNeverReachLocalStore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(root, "flight.db"), filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, err := service.NewPolicyEngine(config.CapturePolicy{Version: "test", Mode: "strict", MaxFieldSize: 1 << 20, Rules: []config.PolicyRule{{Name: "seed", Path: "*", Pattern: `SEEDED_SECRET_[A-Z0-9]+`, Action: "redact"}}})
	if err != nil {
		t.Fatal(err)
	}
	capsule, err := service.NewCapsuleService(repo, policy).Capture(ctx, service.CaptureRequest{TraceID: strings.Repeat("a", 32), ProjectID: "security", ServiceName: "fixture", Operations: []service.CaptureOperation{{Operation: types.Operation{SpanID: strings.Repeat("b", 16), Name: "root", Kind: "agent", Status: "ok", StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0)}, Input: []byte(`{"token":"SEEDED_SECRET_123ABC"}`), InputType: "application/json"}}, StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if capsule.Privacy.Redactions["seed"] == 0 {
		t.Fatal("manifest did not record redaction")
	}
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "SEEDED_SECRET_123ABC") {
			t.Fatalf("seeded secret persisted in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAFRT09MutatingLiveBoundaryFailsPreflight(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(root, "flight.db"), filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, _ := service.NewPolicyEngine(config.CapturePolicy{Version: "test", Mode: "strict", MaxFieldSize: 1024})
	capsules := service.NewCapsuleService(repo, policy)
	response := []byte(`{"ok":true}`)
	capsule := types.RunCapsule{SchemaVersion: types.SchemaVersion, TraceID: strings.Repeat("a", 32), ProjectID: "security", ServiceName: "fixture", StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0), TerminalStatus: "ok", Provenance: types.Provenance{CommitSHA: "abc", Entrypoint: &types.Entrypoint{Command: []string{"agent"}, Image: "sha256:image", InputMode: "stdin_json", OutputMode: "stdout_json"}}, Privacy: types.PrivacySummary{Mode: "strict"}, Operations: []types.Operation{{SpanID: "root", Name: "root", Kind: "agent"}}, Boundaries: []types.Boundary{{ID: "payment", LogicalKey: "payment", SideEffect: "mutating", ReplayPolicy: "live", Response: &types.PayloadRef{Digest: service.DigestBytes(response), State: "captured"}}}}
	service.ComputeCompleteness(&capsule)
	payloads := map[string][]byte{capsule.Boundaries[0].Response.Digest: response}
	if err := service.FinalizeCapsule(&capsule, payloads); err != nil {
		t.Fatal(err)
	}
	if err := repo.PutCapsule(ctx, capsule, payloads); err != nil {
		t.Fatal(err)
	}
	replays := service.NewReplayService(repo, capsules, nil, safeRunner{}, service.NewDiffService(), nil)
	for _, mutation := range []string{"email.send", "payment.charge", "database.write", "filesystem.write"} {
		capsule.Boundaries[0].LogicalKey = mutation
		err = replays.Preflight(ctx, capsule, types.CreateReplayRequest{SourceCapsuleID: capsule.CapsuleID})
		var flightErr *types.FlightError
		if !errors.As(err, &flightErr) || flightErr.Code != types.ErrUnsafeReplay {
			t.Fatalf("%s was not blocked: %v", mutation, err)
		}
	}
}

func TestAFRT09OCIContainerCannotEgressWriteHostOrReachDocker(t *testing.T) {
	runner := oci.NewDockerRunner("docker")
	if err := runner.Available(context.Background()); err != nil {
		if config.RequireOCISecurityContract() {
			t.Fatalf("blocking OCI security contract is unavailable: %v", err)
		}
		t.Skip("OCI daemon is unavailable outside the blocking CI security environment")
	}
	script := `set -eu
test ! -e /var/run/docker.sock
test -r "$FLIGHT_REPLAY_PLAN"
grep -q '"schema_version":"1.0"' "$FLIGHT_REPLAY_PLAN"
if touch /host-write; then exit 21; fi
touch "$FLIGHT_WORKSPACE/probe"
if env -u HTTP_PROXY -u HTTPS_PROXY -u ALL_PROXY wget -q -T 2 -O /dev/null http://1.1.1.1; then exit 22; fi
wget -q -O /dev/null --header 'Content-Type: application/json' --post-data '{"resourceSpans":[]}' "$OTEL_EXPORTER_OTLP_ENDPOINT/v1/traces"
printf 'isolation-ok\n'`
	result, err := runner.Run(context.Background(), oci.RunSpec{
		JobID:          "security-contract",
		Image:          "alpine:3.22",
		GatewayImage:   "ai-flight-recorder:local",
		Command:        []string{"/bin/sh", "-c", script},
		Input:          []byte(`{}`),
		ReplayEnvelope: []byte(`{"schema_version":"1.0","boundaries":[]}`),
		Timeout:        30 * time.Second,
		CPULimit:       0.5,
		MemoryLimitMB:  64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.Contains(string(result.Stdout), "isolation-ok") {
		t.Fatalf("OCI isolation probe failed: exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if len(result.OTLPBatches) != 1 {
		t.Fatalf("expected one candidate OTLP batch copied through the OCI API, got %d", len(result.OTLPBatches))
	}
}

type safeRunner struct{}

func (safeRunner) Available(context.Context) error { return nil }
func (safeRunner) Run(context.Context, oci.RunSpec) (oci.RunResult, error) {
	panic("preflight must block before execution")
}
