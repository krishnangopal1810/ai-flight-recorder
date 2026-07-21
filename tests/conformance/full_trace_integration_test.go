//go:build integration

package conformance_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/api"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

const openInferenceSchemaURL = "https://opentelemetry.io/schemas/1.37.0"

type capturedExport struct {
	repository repository.Repository
	capsules   *service.CapsuleService
	capsule    types.RunCapsule
	response   *collectortrace.ExportTraceServiceResponse
}

func TestAFRCAP001AndAFRING001FullTraceConformsAcrossOTLPTransports(t *testing.T) {
	export := fullTraceExport(51)
	httpCapture := captureHTTPExport(t, export)
	grpcCapture := captureGRPCExport(t, export)

	assertFullTraceCapsule(t, httpCapture)
	assertFullTraceCapsule(t, grpcCapture)
	if httpCapture.capsule.CapsuleID != grpcCapture.capsule.CapsuleID || httpCapture.capsule.Integrity.RootDigest != grpcCapture.capsule.Integrity.RootDigest {
		t.Fatalf("transport-specific translation changed capsule identity: http=%s grpc=%s", httpCapture.capsule.CapsuleID, grpcCapture.capsule.CapsuleID)
	}
}

func TestAFRING001MixedValidAndMalformedSpansReportPartialSuccessOverBothTransports(t *testing.T) {
	for _, test := range []struct {
		name    string
		capture func(*testing.T, *collectortrace.ExportTraceServiceRequest) capturedExport
	}{
		{name: "http", capture: captureHTTPExport},
		{name: "grpc", capture: captureGRPCExport},
	} {
		t.Run(test.name, func(t *testing.T) {
			captured := test.capture(t, mixedValidityExport(52))
			if captured.response.PartialSuccess == nil || captured.response.PartialSuccess.RejectedSpans != 1 || captured.response.PartialSuccess.ErrorMessage != "" {
				t.Fatalf("partial success = %#v", captured.response.PartialSuccess)
			}
			playback := captured.capsule.Completeness["playback"]
			if playback.State != types.CompletenessIncomplete || len(playback.Missing) != 1 || playback.Missing[0].Code != "ingest_spans_rejected" {
				t.Fatalf("partial loss was not attached to the accepted capsule: %#v", playback)
			}
		})
	}
}

func assertFullTraceCapsule(t *testing.T, captured capturedExport) {
	t.Helper()
	capsule := captured.capsule
	if captured.response.PartialSuccess != nil {
		t.Fatalf("unexpected partial success: %#v", captured.response.PartialSuccess)
	}
	if len(capsule.Operations) != 7 {
		t.Fatalf("operations = %#v", capsule.Operations)
	}
	expectedKinds := []string{"agent", "evaluator", "llm", "mcp", "prompt", "retriever", "tool"}
	kinds := make([]string, 0, len(capsule.Operations))
	operationByKind := map[string]types.Operation{}
	for _, operation := range capsule.Operations {
		kinds = append(kinds, operation.Kind)
		operationByKind[operation.Kind] = operation
		if operation.SemanticConventionVersion != openInferenceSchemaURL {
			t.Fatalf("semantic convention version was lost for %s: %q", operation.Kind, operation.SemanticConventionVersion)
		}
		if operation.Input == nil || operation.Output == nil {
			t.Fatalf("operation payload references are incomplete for %s: %#v", operation.Kind, operation)
		}
		for _, reference := range []*types.PayloadRef{operation.Input, operation.Output} {
			if _, err := captured.repository.LoadPayload(context.Background(), reference.Digest); err != nil {
				t.Fatalf("load %s payload %s: %v", operation.Kind, reference.Digest, err)
			}
		}
	}
	sort.Strings(kinds)
	if !reflect.DeepEqual(kinds, expectedKinds) {
		t.Fatalf("logical kinds = %v, want %v", kinds, expectedKinds)
	}
	if operationByKind["llm"].Attributes["openinference.span.kind"] != "LLM" || operationByKind["llm"].Attributes["gen_ai.operation.name"] != "chat" {
		t.Fatalf("GenAI/OpenInference metadata was not preserved: %#v", operationByKind["llm"].Attributes)
	}
	if capsule.Model.Provider != "openai" || capsule.Model.RequestedModel != "gpt-flight" || capsule.Model.ProviderReportedModel != "gpt-flight-2026-07-21" || capsule.Model.ResponseMetadata["gen_ai.usage.input_tokens"] != "17" {
		t.Fatalf("child LLM model configuration was not associated with the capsule: %#v", capsule.Model)
	}
	if len(capsule.Boundaries) != 4 {
		t.Fatalf("boundaries = %#v", capsule.Boundaries)
	}
	if len(capsule.Artifacts) != 1 || capsule.Artifacts[0].State != "captured" || capsule.Artifacts[0].Payload == nil {
		t.Fatalf("artifact = %#v", capsule.Artifacts)
	}
	artifactData, err := captured.repository.LoadPayload(context.Background(), capsule.Artifacts[0].Payload.Digest)
	if err != nil || string(artifactData) != "prompt-template-v3" {
		t.Fatalf("artifact payload = %q %v", artifactData, err)
	}
	if len(capsule.Evaluations) != 1 {
		t.Fatalf("evaluations = %#v", capsule.Evaluations)
	}
	evaluation := capsule.Evaluations[0]
	if evaluation.ID != "quality.faithfulness" || evaluation.Version != "3" || evaluation.Score == nil || *evaluation.Score != 0.95 || evaluation.Threshold == nil || *evaluation.Threshold != 0.8 || len(evaluation.Inputs) != 1 || evaluation.Evidence == nil {
		t.Fatalf("evaluation = %#v", evaluation)
	}
	if evaluation.Inputs[0].Digest != operationByKind["evaluator"].Input.Digest || evaluation.Evidence.Digest != operationByKind["evaluator"].Output.Digest {
		t.Fatalf("evaluation payload evidence diverged from its span: eval=%#v operation=%#v", evaluation, operationByKind["evaluator"])
	}
	if capsule.Privacy.PolicyVersion != "1" || capsule.Privacy.PolicyDigest == "" {
		t.Fatalf("capture policy evidence = %#v", capsule.Privacy)
	}
	manifest, err := json.Marshal(capsule)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{[]byte("prompt-template-v3"), []byte(`"explanation":"supported"`), []byte(`"documents":[`)} {
		if bytes.Contains(manifest, payload) {
			t.Fatalf("payload bypassed content-addressed storage: fragment=%s manifest=%s", payload, manifest)
		}
	}
	assertVerifiedBundleImport(t, captured.repository, capsule)
}

func assertVerifiedBundleImport(t *testing.T, source repository.Repository, capsule types.RunCapsule) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	target, err := repository.OpenLocal(ctx, filepath.Join(root, "flight.db"), filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Close() })
	var bundle bytes.Buffer
	if err := service.NewBundleService(source).Export(ctx, capsule, &bundle); err != nil {
		t.Fatal(err)
	}
	imported, err := service.NewBundleService(target).Import(ctx, bytes.NewReader(bundle.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if imported.CapsuleID != capsule.CapsuleID || imported.Integrity.RootDigest != capsule.Integrity.RootDigest {
		t.Fatalf("verified import changed identity: source=%s imported=%s", capsule.CapsuleID, imported.CapsuleID)
	}
	if err := service.VerifyCapsule(imported, func(digest string) ([]byte, error) { return target.LoadPayload(ctx, digest) }); err != nil {
		t.Fatal(err)
	}
}

func captureHTTPExport(t *testing.T, export *collectortrace.ExportTraceServiceRequest) capturedExport {
	t.Helper()
	repo, receiver, capsules := fixtureRuntime(t)
	cfg := config.Default(t.TempDir())
	cfg.Server.WebDir = filepath.Join(t.TempDir(), "missing")
	server := api.NewServer(cfg, capsules, nil, nil, nil, nil, nil, nil, receiver, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	payload, err := proto.Marshal(export)
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
	var exportResponse collectortrace.ExportTraceServiceResponse
	if err := proto.Unmarshal(response.Body.Bytes(), &exportResponse); err != nil {
		t.Fatal(err)
	}
	return capturedExport{repository: repo, capsules: capsules, capsule: onlyCapsule(t, repo, capsules), response: &exportResponse}
}

func captureGRPCExport(t *testing.T, export *collectortrace.ExportTraceServiceRequest) capturedExport {
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
	t.Cleanup(func() { _ = connection.Close() })
	response, err := collectortrace.NewTraceServiceClient(connection).Export(context.Background(), export)
	if err != nil {
		t.Fatal(err)
	}
	return capturedExport{repository: repo, capsules: capsules, capsule: onlyCapsule(t, repo, capsules), response: response}
}

func fullTraceExport(traceMarker byte) *collectortrace.ExportTraceServiceRequest {
	traceID := id(16, traceMarker)
	rootID := id(8, 1)
	span := func(marker byte, name, kind, input, output string, extras ...*commonv1.KeyValue) *tracev1.Span {
		attributes := []*commonv1.KeyValue{
			stringKV("openinference.span.kind", kind), stringKV("flight.logical_key", strings.ToLower(kind)+"."+name),
			stringKV("input.value", input), stringKV("input.mime_type", "application/json"),
			stringKV("output.value", output), stringKV("output.mime_type", "application/json"),
		}
		attributes = append(attributes, extras...)
		return &tracev1.Span{TraceId: traceID, SpanId: id(8, marker), ParentSpanId: rootID, Name: name, StartTimeUnixNano: uint64(time.Unix(int64(marker), 0).UnixNano()), EndTimeUnixNano: uint64(time.Unix(int64(marker), int64(time.Millisecond)).UnixNano()), Status: &tracev1.Status{Code: tracev1.Status_STATUS_CODE_OK}, Attributes: attributes}
	}
	boundary := func(key, class string) []*commonv1.KeyValue {
		return []*commonv1.KeyValue{
			stringKV("flight.logical_key", key), stringKV("flight.boundary.logical_key", key), stringKV("flight.boundary.class", class),
			stringKV("flight.boundary.side_effect", "read_only"), stringKV("flight.boundary.replay_policy", "substitute"),
			stringKV("flight.boundary.request_digest", "sha256:"+strings.Repeat(class[:1], 64)),
		}
	}
	root := &tracev1.Span{
		TraceId: traceID, SpanId: rootID, Name: "answer", StartTimeUnixNano: uint64(time.Unix(1, 0).UnixNano()), EndTimeUnixNano: uint64(time.Unix(9, 0).UnixNano()), Status: &tracev1.Status{Code: tracev1.Status_STATUS_CODE_OK},
		Attributes: []*commonv1.KeyValue{
			stringKV("openinference.span.kind", "AGENT"), stringKV("flight.logical_key", "agent.answer"),
			stringKV("input.value", `{"question":"policy"}`), stringKV("output.value", `{"answer":"grounded"}`),
			stringKV("flight.git.commit", strings.Repeat("a", 40)), stringKV("flight.entrypoint.command", `["/agent"]`), stringKV("flight.entrypoint.image", "sha256:"+strings.Repeat("b", 64)),
		},
		Events: []*tracev1.Span_Event{{Name: "flight.artifact", Attributes: []*commonv1.KeyValue{
			stringKV("flight.artifact.logical_name", "support-prompt"), stringKV("flight.artifact.type", "prompt"), stringKV("flight.artifact.version", "3"), stringKV("flight.artifact.media_type", "text/plain"),
			stringKV("flight.artifact.content_base64", base64.StdEncoding.EncodeToString([]byte("prompt-template-v3"))),
		}}},
	}
	prompt := span(2, "render", "PROMPT", `{"template":"answer {question}"}`, `{"prompt":"answer policy"}`, stringKV("prompt.id", "support-answer"), stringKV("prompt.vendor", "flight-fixture"))
	retriever := span(3, "documents", "RETRIEVER", `{"query":"policy"}`, `{"documents":[{"id":"policy-v2","content":"grounding"}]}`, boundary("retriever.policy", "retriever")...)
	llmExtras := append(boundary("llm.answer", "llm"),
		stringKV("gen_ai.operation.name", "chat"), stringKV("gen_ai.provider.name", "openai"), stringKV("gen_ai.request.model", "gpt-flight"), stringKV("gen_ai.response.model", "gpt-flight-2026-07-21"), intKV("gen_ai.usage.input_tokens", 17), intKV("gen_ai.usage.output_tokens", 5),
	)
	llm := span(4, "completion", "LLM", `{"messages":[{"role":"user","content":"answer policy"}]}`, `{"message":{"role":"assistant","content":"grounded"}}`, llmExtras...)
	mcp := span(5, "resource-read", "INTERNAL", `{"uri":"policy://refund"}`, `{"text":"grounding"}`, append(boundary("mcp.policy", "mcp"), stringKV("flight.operation.kind", "mcp"))...)
	mcp.Attributes = withoutAttribute(mcp.Attributes, "openinference.span.kind")
	tool := span(6, "calculator", "TOOL", `{"expression":"17+5"}`, `{"value":22}`, append(boundary("tool.calculator", "tool"), stringKV("tool.name", "calculator"))...)
	evaluator := span(7, "faithfulness", "EVALUATOR", `{"answer":"grounded","evidence":"grounding"}`, `{"explanation":"supported"}`,
		stringKV("flight.evaluation.id", "quality.faithfulness"), stringKV("flight.evaluation.version", "3"), doubleKV("flight.evaluation.score", 0.95), doubleKV("flight.evaluation.threshold", 0.8),
	)
	return exportWithSpans(openInferenceSchemaURL, root, prompt, retriever, llm, mcp, tool, evaluator)
}

func mixedValidityExport(traceMarker byte) *collectortrace.ExportTraceServiceRequest {
	traceID := id(16, traceMarker)
	root := &tracev1.Span{TraceId: traceID, SpanId: id(8, 1), Name: "root", StartTimeUnixNano: uint64(time.Unix(1, 0).UnixNano()), EndTimeUnixNano: uint64(time.Unix(2, 0).UnixNano()), Attributes: []*commonv1.KeyValue{stringKV("openinference.span.kind", "AGENT"), stringKV("input.value", `{}`), stringKV("output.value", `{}`)}}
	malformed := &tracev1.Span{TraceId: traceID, SpanId: []byte{1, 2, 3}, ParentSpanId: root.SpanId, Name: "malformed"}
	return exportWithSpans(openInferenceSchemaURL, root, malformed)
}

func exportWithSpans(schemaURL string, spans ...*tracev1.Span) *collectortrace.ExportTraceServiceRequest {
	request := exportFor(sdkFixture{Producer: "conformance", SchemaURL: schemaURL, KindAttribute: "openinference.span.kind", KindValue: "AGENT"})
	request.ResourceSpans[0].ScopeSpans[0].Spans = spans
	return request
}

func id(size int, marker byte) []byte {
	value := make([]byte, size)
	value[size-1] = marker
	return value
}

func withoutAttribute(attributes []*commonv1.KeyValue, key string) []*commonv1.KeyValue {
	filtered := attributes[:0]
	for _, attribute := range attributes {
		if attribute.Key != key {
			filtered = append(filtered, attribute)
		}
	}
	return filtered
}

func intKV(key string, value int64) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: value}}}
}

func doubleKV(key string, value float64) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_DoubleValue{DoubleValue: value}}}
}
