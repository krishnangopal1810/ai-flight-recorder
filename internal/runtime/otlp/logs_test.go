package otlp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestLogsCorrelateWithSpanAndApplyPolicyBeforeStaging(t *testing.T) {
	receiver, capsules, closeRuntime := logTestRuntime(t)
	defer closeRuntime()
	traceID, spanID := logIDs(41)
	request := logRequest(traceID, spanID, &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "token AFR_SECRET_LOG"}})
	request.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Attributes = []*commonv1.KeyValue{{Key: "custom.bytes", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_BytesValue{BytesValue: append([]byte{0xff}, []byte("AFR_SECRET_ATTR")...)}}}}
	if rejected, err := receiver.CaptureLogs(context.Background(), request); err != nil || rejected != 0 {
		t.Fatalf("stage logs: rejected=%d err=%v", rejected, err)
	}
	retryAfterPolicy := logRequest(traceID, spanID, &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "token AFR_SECRET_OTHER"}})
	retryAfterPolicy.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Attributes = request.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Attributes
	if rejected, err := receiver.CaptureLogs(context.Background(), retryAfterPolicy); err != nil || rejected != 0 {
		t.Fatalf("stage post-policy retry: rejected=%d err=%v", rejected, err)
	}
	captured, rejected, err := receiver.Capture(context.Background(), traceRequest(traceID, spanID))
	if err != nil || rejected != 0 || len(captured) != 1 || len(captured[0].Logs) != 1 {
		t.Fatalf("capture trace: %#v rejected=%d err=%v", captured, rejected, err)
	}
	record := captured[0].Logs[0]
	if record.SpanID == "" || record.Body == nil || record.Attributes == nil || !strings.HasPrefix(record.ID, "log_") {
		t.Fatalf("unexpected correlated log: %#v", record)
	}
	body, err := capsules.Payload(context.Background(), record.Body.Digest)
	if err != nil || strings.Contains(string(body), "AFR_SECRET") || !strings.Contains(string(body), "[REDACTED]") {
		t.Fatalf("log body policy was not applied before persistence: %s err=%v", body, err)
	}
	attributeData, err := capsules.Payload(context.Background(), record.Attributes.Digest)
	if err != nil || strings.Contains(string(attributeData), "AFR_SECRET") {
		t.Fatalf("unsafe byte attribute persisted: %s err=%v", attributeData, err)
	}
	var attributes map[string]map[string]string
	if err := json.Unmarshal(attributeData, &attributes); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawStdEncoding.DecodeString(attributes["custom.bytes"]["_flight_otlp_bytes_base64"])
	if err != nil || strings.Contains(string(decoded), "AFR_SECRET") || !strings.Contains(string(decoded), "[REDACTED]") {
		t.Fatalf("byte policy was not applied before tagged base64: %q err=%v", decoded, err)
	}
}

func TestLogsDeduplicateRetriesAndKeepTraceLevelRecords(t *testing.T) {
	receiver, _, closeRuntime := logTestRuntime(t)
	defer closeRuntime()
	traceID, spanID := logIDs(42)
	request := logRequest(traceID, nil, &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "run-level"}})
	for index := 0; index < 2; index++ {
		if rejected, err := receiver.CaptureLogs(context.Background(), request); err != nil || rejected != 0 {
			t.Fatalf("stage retry %d: rejected=%d err=%v", index, rejected, err)
		}
	}
	unknownSpanID := []byte{0, 0, 0, 0, 0, 0, 0, 99}
	unknown := logRequest(traceID, unknownSpanID, &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "unknown span"}})
	unknown.ResourceLogs[0].ScopeLogs[0].LogRecords[0].TimeUnixNano = uint64(time.Unix(4, 0).UnixNano())
	if rejected, err := receiver.CaptureLogs(context.Background(), unknown); err != nil || rejected != 0 {
		t.Fatalf("stage unknown-span record: rejected=%d err=%v", rejected, err)
	}
	captured, _, err := receiver.Capture(context.Background(), traceRequest(traceID, spanID))
	if err != nil || len(captured) != 1 {
		t.Fatalf("capture trace: %#v err=%v", captured, err)
	}
	if len(captured[0].Logs) != 2 || captured[0].Logs[0].SpanID != "0000000000000063" || captured[0].Logs[1].SpanID != "" || !captured[0].Logs[0].Timestamp.Before(captured[0].Logs[1].Timestamp) {
		t.Fatalf("retry or trace-level correlation mismatch: %#v", captured[0].Logs)
	}
}

func TestLogsRejectMissingTraceAndLateRecordsWithPartialSuccess(t *testing.T) {
	receiver, _, closeRuntime := logTestRuntime(t)
	defer closeRuntime()
	traceID, spanID := logIDs(43)
	invalid := logRequest(nil, nil, &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "uncorrelated"}})
	response, err := NewLogsService(receiver).Export(context.Background(), invalid)
	if err != nil || response.PartialSuccess == nil || response.PartialSuccess.RejectedLogRecords != 1 {
		t.Fatalf("missing trace was not partially rejected: %#v err=%v", response, err)
	}
	if _, _, err := receiver.Capture(context.Background(), traceRequest(traceID, spanID)); err != nil {
		t.Fatal(err)
	}
	late := logRequest(traceID, spanID, &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "late"}})
	response, err = NewLogsService(receiver).Export(context.Background(), late)
	if err != nil || response.PartialSuccess == nil || response.PartialSuccess.RejectedLogRecords != 1 {
		t.Fatalf("late log was not partially rejected: %#v err=%v", response, err)
	}
}

func TestLogsGRPCServiceTransport(t *testing.T) {
	receiver, _, closeRuntime := logTestRuntime(t)
	defer closeRuntime()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	collectorlogs.RegisterLogsServiceServer(server, NewLogsService(receiver))
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	traceID, spanID := logIDs(44)
	response, err := collectorlogs.NewLogsServiceClient(connection).Export(context.Background(), logRequest(traceID, spanID,
		&commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "over grpc"}}))
	if err != nil || response.PartialSuccess != nil {
		t.Fatalf("gRPC log export response=%#v err=%v", response, err)
	}
	if receiver.LogStats().Accepted != 1 {
		t.Fatalf("accepted counter=%d", receiver.LogStats().Accepted)
	}
}

func TestLogsArrivingDuringTraceSettleWindowAreSealedIntoCapsule(t *testing.T) {
	receiver, _, closeRuntime := logTestRuntime(t)
	defer closeRuntime()
	receiver.grace = 50 * time.Millisecond
	traceID, spanID := logIDs(45)
	type result struct {
		capsules []types.RunCapsule
		err      error
	}
	done := make(chan result, 1)
	go func() {
		captured, _, err := receiver.Capture(context.Background(), traceRequest(traceID, spanID))
		done <- result{capsules: captured, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		receiver.mu.Lock()
		pending := len(receiver.pending) == 1
		receiver.mu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("trace never entered the settle window")
		}
		time.Sleep(time.Millisecond)
	}
	if rejected, err := receiver.CaptureLogs(context.Background(), logRequest(traceID, spanID,
		&commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "during settle"}})); err != nil || rejected != 0 {
		t.Fatalf("settle-window log rejected=%d err=%v", rejected, err)
	}
	select {
	case captured := <-done:
		if captured.err != nil || len(captured.capsules) != 1 || len(captured.capsules[0].Logs) != 1 {
			t.Fatalf("settled capsule=%#v err=%v", captured.capsules, captured.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("trace did not finalize after the shared settle window")
	}
}

func logTestRuntime(t *testing.T) (*Receiver, *service.CapsuleService, func()) {
	t.Helper()
	ctx := context.Background()
	directory := t.TempDir()
	repo, err := repository.OpenLocal(ctx, filepath.Join(directory, "flight.db"), filepath.Join(directory, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := service.NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1 << 20, Rules: []config.PolicyRule{{Name: "secret", Path: "*", Pattern: `AFR_SECRET_[A-Z]+`, Action: "redact", Replacement: "[REDACTED]"}}})
	if err != nil {
		t.Fatal(err)
	}
	capsules := service.NewCapsuleService(repo, policy)
	return NewReceiver(capsules), capsules, func() { _ = repo.Close() }
}

func logIDs(suffix byte) ([]byte, []byte) {
	traceID, spanID := make([]byte, 16), make([]byte, 8)
	traceID[15], spanID[7] = suffix, suffix
	return traceID, spanID
}

func logRequest(traceID, spanID []byte, body *commonv1.AnyValue) *collectorlogs.ExportLogsServiceRequest {
	return &collectorlogs.ExportLogsServiceRequest{ResourceLogs: []*logsv1.ResourceLogs{{
		Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{stringKV("service.name", "log-agent"), stringKV("flight.project.id", "logs"), boolKV("flight.capture", true)}},
		ScopeLogs: []*logsv1.ScopeLogs{{Scope: &commonv1.InstrumentationScope{Name: "test.logs", Version: "1.0"}, LogRecords: []*logsv1.LogRecord{{
			TraceId: traceID, SpanId: spanID, TimeUnixNano: uint64(time.Unix(5, 0).UnixNano()), SeverityNumber: logsv1.SeverityNumber_SEVERITY_NUMBER_INFO, SeverityText: "INFO", Body: body,
		}}}},
	}}}
}

func traceRequest(traceID, spanID []byte) *collectortrace.ExportTraceServiceRequest {
	return &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{
		Resource:   &resourcev1.Resource{Attributes: []*commonv1.KeyValue{stringKV("service.name", "log-agent"), stringKV("flight.project.id", "logs"), boolKV("flight.capture", true)}},
		ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{TraceId: traceID, SpanId: spanID, Name: "root", StartTimeUnixNano: uint64(time.Unix(1, 0).UnixNano()), EndTimeUnixNano: uint64(time.Unix(6, 0).UnixNano())}}}},
	}}}
}
