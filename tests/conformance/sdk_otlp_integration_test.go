//go:build integration

package conformance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/api"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/otlp"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type sdkFixture struct {
	Producer      string `json:"producer"`
	SchemaURL     string `json:"schema_url"`
	KindAttribute string `json:"kind_attribute"`
	KindValue     string `json:"kind_value"`
}

func TestPythonAndTypeScriptFixturesConformOverOTLPHTTPAndGRPC(t *testing.T) {
	python := sendHTTPFixture(t, loadFixture(t, "python.json"))
	typescript := sendGRPCFixture(t, loadFixture(t, "typescript.json"))
	if python.ProjectID != typescript.ProjectID || python.ServiceName != typescript.ServiceName || python.TerminalStatus != typescript.TerminalStatus {
		t.Fatalf("capsule identity fields differ: python=%#v typescript=%#v", python, typescript)
	}
	if len(python.Operations) != 1 || len(typescript.Operations) != 1 {
		t.Fatalf("unexpected logical operations: python=%#v typescript=%#v", python.Operations, typescript.Operations)
	}
	left, right := python.Operations[0], typescript.Operations[0]
	if left.Kind != "agent" || right.Kind != "agent" || left.LogicalKey != right.LogicalKey || left.Input.Digest != right.Input.Digest || left.Output.Digest != right.Output.Digest {
		t.Fatalf("SDK logical capsules differ: python=%#v typescript=%#v", left, right)
	}
	if python.Completeness["replay"].State != types.CompletenessReady || typescript.Completeness["replay"].State != types.CompletenessReady {
		t.Fatalf("SDK completeness differs: python=%#v typescript=%#v", python.Completeness, typescript.Completeness)
	}
}

func sendHTTPFixture(t *testing.T, fixture sdkFixture) types.RunCapsule {
	t.Helper()
	repo, receiver, capsules := fixtureRuntime(t)
	cfg := config.Default(t.TempDir())
	cfg.Server.WebDir = filepath.Join(t.TempDir(), "missing")
	server := api.NewServer(cfg, capsules, nil, nil, nil, nil, nil, nil, receiver, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	payload, err := proto.Marshal(exportFor(fixture))
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/x-protobuf")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("OTLP HTTP status=%d body=%s", response.Code, response.Body.String())
	}
	return onlyCapsule(t, repo, capsules)
}

func sendGRPCFixture(t *testing.T, fixture sdkFixture) types.RunCapsule {
	t.Helper()
	repo, receiver, capsules := fixtureRuntime(t)
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	collectortrace.RegisterTraceServiceServer(server, receiver)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	client := collectortrace.NewTraceServiceClient(connection)
	if _, err := client.Export(context.Background(), exportFor(fixture)); err != nil {
		t.Fatal(err)
	}
	return onlyCapsule(t, repo, capsules)
}

func fixtureRuntime(t *testing.T) (repository.Repository, *otlp.Receiver, *service.CapsuleService) {
	t.Helper()
	root := t.TempDir()
	repo, err := repository.OpenLocal(context.Background(), filepath.Join(root, "flight.db"), filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	policy, err := service.NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	capsules := service.NewCapsuleService(repo, policy)
	return repo, otlp.NewReceiver(capsules), capsules
}

func onlyCapsule(t *testing.T, repo repository.Repository, capsules *service.CapsuleService) types.RunCapsule {
	t.Helper()
	items, err := repo.ListCapsules(context.Background(), "conformance", 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("durable capsule: %#v %v", items, err)
	}
	capsule, err := capsules.Get(context.Background(), items[0].CapsuleID, true)
	if err != nil {
		t.Fatal(err)
	}
	return capsule
}

func loadFixture(t *testing.T, name string) sdkFixture {
	t.Helper()
	data, err := io.ReadAll(mustOpen(t, filepath.Join("fixtures", name)))
	if err != nil {
		t.Fatal(err)
	}
	var fixture sdkFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func mustOpen(t *testing.T, name string) io.ReadCloser {
	t.Helper()
	file, err := http.Dir(".").Open(name)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func exportFor(fixture sdkFixture) *collectortrace.ExportTraceServiceRequest {
	traceID := make([]byte, 16)
	traceID[15] = 42
	spanID := make([]byte, 8)
	spanID[7] = 1
	attributes := []*commonv1.KeyValue{
		stringKV(fixture.KindAttribute, fixture.KindValue),
		stringKV("flight.logical_key", "agent.answer"),
		stringKV("input.value", `{"question":"policy"}`),
		stringKV("output.value", `{"answer":"recorded"}`),
		stringKV("flight.git.commit", strings.Repeat("a", 40)),
		stringKV("flight.entrypoint.command", `["/agent"]`),
		stringKV("flight.entrypoint.image", "sha256:"+strings.Repeat("b", 64)),
	}
	span := &tracev1.Span{TraceId: traceID, SpanId: spanID, Name: "answer", StartTimeUnixNano: uint64(time.Unix(1, 0).UnixNano()), EndTimeUnixNano: uint64(time.Unix(2, 0).UnixNano()), Status: &tracev1.Status{Code: tracev1.Status_STATUS_CODE_OK}, Attributes: attributes}
	resource := &resourcev1.Resource{Attributes: []*commonv1.KeyValue{stringKV("service.name", "reference-agent"), stringKV("flight.project.id", "conformance"), boolKV("flight.capture", true), stringKV("flight.sdk.language", fixture.Producer)}}
	return &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{Resource: resource, ScopeSpans: []*tracev1.ScopeSpans{{SchemaUrl: fixture.SchemaURL, Spans: []*tracev1.Span{span}}}}}}
}

func stringKV(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}}
}

func boolKV(key string, value bool) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_BoolValue{BoolValue: value}}}
}
