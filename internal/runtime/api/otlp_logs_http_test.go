package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/otlp"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
)

func TestOTLPLogsHTTPProtobufJSONGzipAndPartialSuccess(t *testing.T) {
	server, receiver, closeRuntime := logsHTTPRuntime(t)
	defer closeRuntime()

	tests := []struct {
		name        string
		contentType string
		gzip        bool
		json        bool
	}{
		{name: "protobuf", contentType: "application/x-protobuf"},
		{name: "protobuf gzip", contentType: "application/x-protobuf", gzip: true},
		{name: "json", contentType: "application/json", json: true},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			traceID, spanID := httpLogIDs(byte(index + 1))
			request := httpLogRequest(traceID, spanID)
			var body []byte
			var err error
			if test.json {
				body, err = protojson.Marshal(request)
				body = bytes.ReplaceAll(body, []byte(base64.StdEncoding.EncodeToString(traceID)), []byte(hex.EncodeToString(traceID)))
				body = bytes.ReplaceAll(body, []byte(base64.StdEncoding.EncodeToString(spanID)), []byte(hex.EncodeToString(spanID)))
			} else {
				body, err = proto.Marshal(request)
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.gzip {
				var compressed bytes.Buffer
				writer := gzip.NewWriter(&compressed)
				if _, err := writer.Write(body); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				body = compressed.Bytes()
			}
			httpRequest := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(body))
			httpRequest.Header.Set("Content-Type", test.contentType)
			if test.gzip {
				httpRequest.Header.Set("Content-Encoding", "gzip")
			}
			response := httptest.NewRecorder()
			server.otlpLogsHTTP(response, httpRequest)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			captured, rejected, err := receiver.Capture(context.Background(), httpTraceRequest(traceID, spanID))
			if err != nil || rejected != 0 || len(captured) != 1 || len(captured[0].Logs) != 1 {
				t.Fatalf("capture=%#v rejected=%d err=%v", captured, rejected, err)
			}
		})
	}

	traceID, spanID := httpLogIDs(9)
	mixed := httpLogRequest(traceID, spanID)
	mixed.ResourceLogs[0].ScopeLogs[0].LogRecords = append(mixed.ResourceLogs[0].ScopeLogs[0].LogRecords,
		&logsv1.LogRecord{SpanId: spanID, Body: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "invalid"}}})
	body, err := proto.Marshal(mixed)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(body))
	httpRequest.Header.Set("Content-Type", "application/x-protobuf")
	response := httptest.NewRecorder()
	server.otlpLogsHTTP(response, httpRequest)
	var decoded collectorlogs.ExportLogsServiceResponse
	if err := proto.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || decoded.PartialSuccess == nil || decoded.PartialSuccess.RejectedLogRecords != 1 {
		t.Fatalf("mixed response status=%d response=%#v", response.Code, decoded)
	}
}

func TestOTLPLogsHTTPUsesIngestAuthorization(t *testing.T) {
	server, _, closeRuntime := logsHTTPRuntime(t)
	defer closeRuntime()
	server.config.Auth.LocalBypass = false

	request := httptest.NewRequest(http.MethodPost, "/v1/logs", bytes.NewReader(nil))
	request.Header.Set("Content-Type", "application/x-protobuf")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", response.Code, response.Body.String())
	}
}

func logsHTTPRuntime(t *testing.T) (*Server, *otlp.Receiver, func()) {
	t.Helper()
	repo, err := repository.OpenLocal(context.Background(), filepath.Join(t.TempDir(), "flight.db"), filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := service.NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	capsules := service.NewCapsuleService(repo, policy)
	receiver := otlp.NewReceiver(capsules)
	cfg := config.Default("")
	cfg.Auth.LocalBypass = true
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(cfg, capsules, nil, nil, service.NewAuthService(repo), nil, nil, nil, receiver, logger)
	return server, receiver, func() { _ = repo.Close() }
}

func httpLogIDs(suffix byte) ([]byte, []byte) {
	traceID, spanID := make([]byte, 16), make([]byte, 8)
	traceID[15], spanID[7] = suffix, suffix
	return traceID, spanID
}

func httpLogRequest(traceID, spanID []byte) *collectorlogs.ExportLogsServiceRequest {
	return &collectorlogs.ExportLogsServiceRequest{ResourceLogs: []*logsv1.ResourceLogs{{
		Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
			httpStringKV("service.name", "http-log-agent"), httpStringKV("flight.project.id", "logs"),
			{Key: "flight.capture", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_BoolValue{BoolValue: true}}},
		}},
		ScopeLogs: []*logsv1.ScopeLogs{{LogRecords: []*logsv1.LogRecord{{
			TraceId: traceID, SpanId: spanID, TimeUnixNano: uint64(time.Unix(5, 0).UnixNano()),
			SeverityNumber: logsv1.SeverityNumber_SEVERITY_NUMBER_INFO,
			Body:           &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: fmt.Sprintf("log-%s", hex.EncodeToString(traceID))}},
		}}}},
	}}}
}

func httpTraceRequest(traceID, spanID []byte) *collectortrace.ExportTraceServiceRequest {
	return &collectortrace.ExportTraceServiceRequest{ResourceSpans: []*tracev1.ResourceSpans{{
		Resource: &resourcev1.Resource{Attributes: []*commonv1.KeyValue{
			httpStringKV("service.name", "http-log-agent"), httpStringKV("flight.project.id", "logs"),
			{Key: "flight.capture", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_BoolValue{BoolValue: true}}},
		}},
		ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{TraceId: traceID, SpanId: spanID, Name: "root", StartTimeUnixNano: uint64(time.Unix(1, 0).UnixNano()), EndTimeUnixNano: uint64(time.Unix(6, 0).UnixNano())}}}},
	}}}
}

func httpStringKV(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}}
}
