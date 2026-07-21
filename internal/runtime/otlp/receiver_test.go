package otlp

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestReceiverCreatesReplayAwareCapsuleFromOTLP(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, _ := service.NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1 << 20})
	receiver := NewReceiver(service.NewCapsuleService(repo, policy))
	traceID := make([]byte, 16)
	traceID[15] = 1
	spanID := make([]byte, 8)
	spanID[7] = 1
	request := &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{
		Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
			stringKV("service.name", "policy-agent"), stringKV("flight.project.id", "demo"), boolKV("flight.capture", true),
		}},
		ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{
			TraceId: traceID, SpanId: spanID, Name: "answer",
			StartTimeUnixNano: uint64(time.Unix(1, 0).UnixNano()), EndTimeUnixNano: uint64(time.Unix(2, 0).UnixNano()),
			Attributes: []*commonv1.KeyValue{
				stringKV("openinference.span.kind", "AGENT"), stringKV("input.value", `{"question":"policy"}`),
				stringKV("output.value", `{"answer":"incorrect"}`), stringKV("flight.entrypoint.command", `["python","app.py"]`),
				stringKV("flight.entrypoint.image", "example:latest"), stringKV("flight.git.commit", "abc"), boolKV("flight.reference.model_callback_invoked", true),
			},
		}}}},
	}}}
	capsules, rejected, err := receiver.Capture(ctx, request)
	if err != nil || rejected != 0 || len(capsules) != 1 {
		t.Fatalf("capture: %#v rejected=%d err=%v", capsules, rejected, err)
	}
	if capsules[0].Operations[0].Kind != "agent" || capsules[0].Operations[0].Attributes["flight.reference.model_callback_invoked"] != "true" || capsules[0].Completeness["playback"].State != "ready" {
		t.Fatalf("unexpected capsule: %#v", capsules[0])
	}
}

func TestReceiverRequiresExplicitBoundaryNamespace(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, _ := service.NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1 << 20})
	receiver := NewReceiver(service.NewCapsuleService(repo, policy))
	traceID := make([]byte, 16)
	traceID[15] = 2
	rootID, childID := make([]byte, 8), make([]byte, 8)
	rootID[7], childID[7] = 1, 2
	request := &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{
		Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{stringKV("service.name", "agent"), stringKV("flight.project.id", "demo"), boolKV("flight.capture", true)}},
		ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{
			{TraceId: traceID, SpanId: rootID, Name: "root", Attributes: []*commonv1.KeyValue{stringKV("flight.logical_key", "agent.root")}},
			{TraceId: traceID, SpanId: childID, ParentSpanId: rootID, Name: "api", Attributes: []*commonv1.KeyValue{stringKV("flight.logical_key", "api.policy"), stringKV("flight.boundary.logical_key", "api.policy"), stringKV("flight.boundary.class", "api")}},
		}}},
	}}}
	capsules, rejected, err := receiver.Capture(ctx, request)
	if err != nil || rejected != 0 || len(capsules) != 1 {
		t.Fatalf("capture: %#v rejected=%d err=%v", capsules, rejected, err)
	}
	if len(capsules[0].Boundaries) != 1 || capsules[0].Boundaries[0].LogicalKey != "api.policy" {
		t.Fatalf("logical operations leaked into boundaries: %#v", capsules[0].Boundaries)
	}
}

func TestReceiverMergesCrossBatchChildrenDuringLateSpanGrace(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, _ := service.NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1 << 20})
	receiver := NewReceiver(service.NewCapsuleService(repo, policy), 25*time.Millisecond)
	traceID := make([]byte, 16)
	traceID[15] = 9
	rootID := make([]byte, 8)
	rootID[7] = 1
	childID := make([]byte, 8)
	childID[7] = 2
	resource := func(span *tracev1.Span) *collectortrace.ExportTraceServiceRequest {
		return &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{stringKV("service.name", "agent"), stringKV("flight.project.id", "demo"), boolKV("flight.capture", true)}}, ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{span}}}}}}
	}
	child := resource(&tracev1.Span{TraceId: traceID, SpanId: childID, ParentSpanId: rootID, Name: "model", StartTimeUnixNano: uint64(time.Unix(2, 0).UnixNano()), EndTimeUnixNano: uint64(time.Unix(3, 0).UnixNano()), Attributes: []*commonv1.KeyValue{stringKV("openinference.span.kind", "LLM"), stringKV("gen_ai.provider.name", "openai"), stringKV("gen_ai.request.model", "gpt-flight")}})
	root := resource(&tracev1.Span{TraceId: traceID, SpanId: rootID, Name: "agent", StartTimeUnixNano: uint64(time.Unix(1, 0).UnixNano()), EndTimeUnixNano: uint64(time.Unix(4, 0).UnixNano()), Attributes: []*commonv1.KeyValue{stringKV("openinference.span.kind", "AGENT")}})
	childDone := make(chan error, 1)
	go func() { _, _, captureErr := receiver.Capture(ctx, child); childDone <- captureErr }()
	time.Sleep(5 * time.Millisecond)
	capsules, rejected, err := receiver.Capture(ctx, root)
	if err != nil || rejected != 0 || len(capsules) != 1 {
		t.Fatalf("root capture: %#v %d %v", capsules, rejected, err)
	}
	if err := <-childDone; err != nil {
		t.Fatalf("child capture: %v", err)
	}
	if len(capsules[0].Operations) != 2 {
		t.Fatalf("late child was not assembled: %#v", capsules[0].Operations)
	}
	if capsules[0].Model.Provider != "openai" || capsules[0].Model.RequestedModel != "gpt-flight" {
		t.Fatalf("late child model metadata was not assembled: %#v", capsules[0].Model)
	}
	stored, err := repo.ListCapsules(ctx, "demo", 10)
	if err != nil || len(stored) != 1 {
		t.Fatalf("expected one durable capsule: %#v %v", stored, err)
	}
}

func TestAFRT07MalformedSpanIsIsolatedFromValidTrace(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, _ := service.NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1 << 20})
	receiver := NewReceiver(service.NewCapsuleService(repo, policy))
	traceID := make([]byte, 16)
	traceID[15] = 7
	validID := make([]byte, 8)
	validID[7] = 1
	request := &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{
		Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{stringKV("service.name", "agent"), stringKV("flight.project.id", "demo"), boolKV("flight.capture", true)}},
		ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{
			{TraceId: traceID, SpanId: validID, Name: "root", StartTimeUnixNano: uint64(time.Unix(1, 0).UnixNano()), EndTimeUnixNano: uint64(time.Unix(2, 0).UnixNano())},
			{TraceId: traceID, SpanId: []byte{1, 2, 3}, ParentSpanId: validID, Name: "malformed"},
		}}},
	}}}
	capsules, rejected, err := receiver.Capture(ctx, request)
	if err != nil || rejected != 1 || len(capsules) != 1 {
		t.Fatalf("partial acceptance: capsules=%#v rejected=%d err=%v", capsules, rejected, err)
	}
	playback := capsules[0].Completeness["playback"]
	if playback.State != types.CompletenessIncomplete || len(playback.Missing) != 1 || playback.Missing[0].Code != "ingest_spans_rejected" {
		t.Fatalf("partial loss not reflected in completeness: %#v", playback)
	}
}

func TestFailedFinalizationIsReportedAsRejectedForRetry(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	policy, _ := service.NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1 << 20})
	receiver := NewReceiver(service.NewCapsuleService(repo, policy))
	traceID := make([]byte, 16)
	traceID[15] = 8
	spanID := make([]byte, 8)
	spanID[7] = 2
	request := &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{
		Resource:   &resourcev1.Resource{Attributes: []*commonv1.KeyValue{boolKV("flight.capture", true)}},
		ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{TraceId: traceID, SpanId: spanID, ParentSpanId: []byte{0, 0, 0, 0, 0, 0, 0, 1}, Name: "orphan"}}}},
	}}}
	response, err := receiver.Export(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if response.PartialSuccess == nil || response.PartialSuccess.RejectedSpans != 1 || response.PartialSuccess.ErrorMessage == "" {
		t.Fatalf("non-durable batch was acknowledged: %#v", response.PartialSuccess)
	}
}

func TestAFRCAP002ProvenanceParsesRecordedFactsWithoutFabrication(t *testing.T) {
	span := keyValues([]*commonv1.KeyValue{
		stringKV("flight.project.id", "support"), stringKV("flight.repository.url", "https://example.invalid/agent.git"), stringKV("flight.git.commit", strings.Repeat("a", 40)), stringKV("flight.git.dirty_digest", "sha256:"+strings.Repeat("d", 64)),
		stringKV("flight.build.id", "build-1"), stringKV("flight.runtime", "python:3.13"), stringKV("flight.dependency_lock", `{"uv.lock":"sha256:aaa"}`), stringKV("flight.feature_flags", `{"retrieval":"v2"}`), stringKV("flight.environment.REGION.digest", "sha256:region"),
		stringKV("flight.entrypoint.command", `["python","agent.py"]`), stringKV("flight.entrypoint.image", "sha256:"+strings.Repeat("i", 64)), stringKV("flight.entrypoint.model_hosts", `["model.test.invalid"]`),
	})
	resource := keyValues([]*commonv1.KeyValue{stringKV("container.id", "container-1")})
	actual := provenance(span, resource)
	if len(actual.Missing) != 0 || actual.Runtime != "python:3.13" || actual.ContainerID != "container-1" || actual.DependencyLock["uv.lock"] != "sha256:aaa" || actual.FeatureFlags["retrieval"] != "v2" || actual.EnvironmentFacts["REGION"] != "sha256:region" || actual.Entrypoint == nil || !reflect.DeepEqual(actual.Entrypoint.Command, []string{"python", "agent.py"}) {
		t.Fatalf("complete provenance = %#v", actual)
	}
}

func TestAFRCAP002ProvenanceNamesMissingFactsAndNeverInventsRuntimeOrEntrypoint(t *testing.T) {
	actual := provenance(map[string]*commonv1.AnyValue{"flight.entrypoint.command": stringKV("command", "python agent.py").Value}, map[string]*commonv1.AnyValue{"process.runtime.version": stringKV("version", "3.13").Value})
	wantMissing := []string{"entrypoint_command_invalid", "source_identity", "commit_sha", "build_or_container_identity", "runtime", "dependency_lock", "feature_flags", "entrypoint"}
	if actual.Runtime != "" || actual.Entrypoint != nil || !reflect.DeepEqual(actual.Missing, wantMissing) {
		t.Fatalf("missing provenance = %#v, want missing=%v", actual, wantMissing)
	}
}

func TestAFRCAP002ProvenanceNamesInvalidStructuredFacts(t *testing.T) {
	actual := provenance(map[string]*commonv1.AnyValue{
		"flight.dependency_lock": stringKV("dependency", "null").Value,
		"flight.feature_flags":   stringKV("flags", "[]").Value,
	}, nil)
	if !slices.Contains(actual.Missing, "dependency_lock_invalid") || !slices.Contains(actual.Missing, "feature_flags_invalid") || actual.DependencyLock != nil || actual.FeatureFlags != nil {
		t.Fatalf("invalid structured provenance = %#v", actual)
	}
}

func stringKV(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}}
}
func boolKV(key string, value bool) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_BoolValue{BoolValue: value}}}
}
