package otlp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type LogsService struct {
	collectorlogs.UnimplementedLogsServiceServer
	receiver *Receiver
}

func NewLogsService(receiver *Receiver) *LogsService { return &LogsService{receiver: receiver} }

func (s *LogsService) Export(ctx context.Context, request *collectorlogs.ExportLogsServiceRequest) (*collectorlogs.ExportLogsServiceResponse, error) {
	rejected, captureErr := s.receiver.CaptureLogs(ctx, request)
	response := &collectorlogs.ExportLogsServiceResponse{}
	if rejected > 0 || captureErr != nil {
		response.PartialSuccess = &collectorlogs.ExportLogsPartialSuccess{RejectedLogRecords: int64(rejected), ErrorMessage: safeError(captureErr)}
	}
	return response, nil
}

func (r *Receiver) CaptureLogs(ctx context.Context, request *collectorlogs.ExportLogsServiceRequest) (int, error) {
	if request == nil {
		return 0, nil
	}
	rejected := 0
	invalid := 0
	late := 0
	var captureErr error
	now := time.Now().UTC()
	expiresAt := now.Add(maxDuration(2*r.grace, time.Minute))
	for _, resourceLogs := range request.ResourceLogs {
		resourceAttributes := attributes(resourceLogs.Resource)
		projectID := stringValue(resourceAttributes["flight.project.id"])
		serviceName := first(resourceAttributes, "service.name", "flight.service.name")
		captureEnabled := boolValue(resourceAttributes["flight.capture"])
		for _, scopeLogs := range resourceLogs.ScopeLogs {
			for _, record := range scopeLogs.LogRecords {
				if !captureEnabled || projectID == "" || !validOTLPID(record.TraceId, 16) || (len(record.SpanId) != 0 && !validOTLPID(record.SpanId, 8)) {
					rejected++
					invalid++
					continue
				}
				traceID := hex.EncodeToString(record.TraceId)
				captured, err := captureLogRecord(record, serviceName, scopeLogs, resourceLogs.SchemaUrl, now)
				if err != nil {
					rejected++
					invalid++
					captureErr = err
					continue
				}
				if err := r.capsules.StageLog(ctx, projectID, traceID, captured, expiresAt); err != nil {
					rejected++
					if errors.Is(err, repository.ErrTraceLogsSealed) {
						late++
					} else {
						invalid++
						captureErr = err
					}
					continue
				}
				r.acceptedLogs.Add(1)
				r.touchLogs(projectID, traceID)
			}
		}
	}
	if rejected > 0 {
		r.rejectedLogs.Add(int64(rejected))
		if invalid > 0 {
			_ = r.capsules.QuarantineLogDiagnostic(context.Background(), "rejected_log", invalid)
		}
		if late > 0 {
			_ = r.capsules.QuarantineLogDiagnostic(context.Background(), "late_log", late)
		}
		if captureErr == nil {
			captureErr = types.NewError(types.ErrValidation, "one or more OTLP log records could not be correlated", "include flight.capture, flight.project.id, a valid trace ID, and an optional valid span ID", nil)
		}
	}
	return rejected, captureErr
}

func validOTLPID(value []byte, size int) bool {
	if len(value) != size {
		return false
	}
	for _, item := range value {
		if item != 0 {
			return true
		}
	}
	return false
}

func countRequestLogs(request *collectorlogs.ExportLogsServiceRequest) int {
	count := 0
	if request == nil {
		return count
	}
	for _, resource := range request.ResourceLogs {
		for _, scope := range resource.ScopeLogs {
			count += len(scope.LogRecords)
		}
	}
	return count
}

func captureLogRecord(record *logsv1.LogRecord, serviceName string, scope *logsv1.ScopeLogs, resourceSchemaURL string, fallback time.Time) (service.CaptureLog, error) {
	timestamp := fallback
	if record.TimeUnixNano != 0 {
		timestamp = time.Unix(0, int64(record.TimeUnixNano)).UTC()
	} else if record.ObservedTimeUnixNano != 0 {
		timestamp = time.Unix(0, int64(record.ObservedTimeUnixNano)).UTC()
	}
	var observed *time.Time
	if record.ObservedTimeUnixNano != 0 {
		value := time.Unix(0, int64(record.ObservedTimeUnixNano)).UTC()
		observed = &value
	}
	result := service.CaptureLog{Record: types.LogRecord{
		SpanID: hex.EncodeToString(record.SpanId), ServiceName: serviceName, Timestamp: timestamp,
		ObservedTimestamp: observed, SeverityNumber: int32(record.SeverityNumber), SeverityText: record.SeverityText,
		EventName: record.EventName, DroppedAttributesCount: record.DroppedAttributesCount,
		ScopeSchemaURL: defaultString(scope.SchemaUrl, resourceSchemaURL),
	}}
	if scope.Scope != nil {
		result.Record.ScopeName = scope.Scope.Name
		result.Record.ScopeVersion = scope.Scope.Version
	}
	if record.Body != nil {
		value, err := logAnyValue(record.Body)
		if err != nil {
			return service.CaptureLog{}, err
		}
		result.Body, err = json.Marshal(value)
		if err != nil {
			return service.CaptureLog{}, types.NewError(types.ErrValidation, "OTLP log body is not JSON-compatible", "emit a supported OTLP AnyValue", err)
		}
		result.BodyType = "application/json"
	}
	if len(record.Attributes) > 0 {
		values := map[string]any{}
		for _, attribute := range record.Attributes {
			if attribute == nil || attribute.Key == "" {
				continue
			}
			value, err := logAnyValue(attribute.Value)
			if err != nil {
				return service.CaptureLog{}, err
			}
			values[attribute.Key] = value
		}
		var err error
		result.Attributes, err = json.Marshal(values)
		if err != nil {
			return service.CaptureLog{}, types.NewError(types.ErrValidation, "OTLP log attributes are not JSON-compatible", "emit supported OTLP AnyValues", err)
		}
		result.AttributesType = "application/json"
	}
	return result, nil
}

func logAnyValue(value *commonv1.AnyValue) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch typed := value.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return typed.StringValue, nil
	case *commonv1.AnyValue_BoolValue:
		return typed.BoolValue, nil
	case *commonv1.AnyValue_IntValue:
		return typed.IntValue, nil
	case *commonv1.AnyValue_DoubleValue:
		return typed.DoubleValue, nil
	case *commonv1.AnyValue_BytesValue:
		return map[string]any{"_flight_otlp_bytes_base64": base64.RawStdEncoding.EncodeToString(typed.BytesValue)}, nil
	case *commonv1.AnyValue_ArrayValue:
		items := make([]any, 0, len(typed.ArrayValue.Values))
		for _, item := range typed.ArrayValue.Values {
			converted, err := logAnyValue(item)
			if err != nil {
				return nil, err
			}
			items = append(items, converted)
		}
		return items, nil
	case *commonv1.AnyValue_KvlistValue:
		items := map[string]any{}
		for _, item := range typed.KvlistValue.Values {
			if item == nil || item.Key == "" {
				continue
			}
			converted, err := logAnyValue(item.Value)
			if err != nil {
				return nil, err
			}
			items[item.Key] = converted
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unsupported OTLP log AnyValue %T", typed)
	}
}
