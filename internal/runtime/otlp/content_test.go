package otlp

import (
	"math"
	"testing"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRCAP003ProducerOmissionAttributesDecodeToTypedReference(t *testing.T) {
	values := keyValues([]*commonv1.KeyValue{
		stringKV("flight.output.omission.reason", types.OmissionSerialization),
		intKV("flight.output.omission.size", 417),
		stringKV("flight.output.omission.media_type", "application/json"),
	})
	data, mediaType, omission := operationContent(values, "output")
	if data != nil || mediaType != "application/json" || omission == nil || omission.State != types.PayloadStateOmitted || omission.Reason != types.OmissionSerialization || omission.Size != 417 {
		t.Fatalf("decoded omission: data=%q media=%q ref=%#v", data, mediaType, omission)
	}
}

func TestAFRCAP003StructuredToolDefinitionsBecomeCanonicalPayload(t *testing.T) {
	definition := &commonv1.AnyValue{Value: &commonv1.AnyValue_KvlistValue{KvlistValue: &commonv1.KeyValueList{Values: []*commonv1.KeyValue{
		stringKV("type", "function"), stringKV("name", "lookup_policy"),
	}}}}
	values := map[string]*commonv1.AnyValue{
		"gen_ai.tool.definitions": {Value: &commonv1.AnyValue_ArrayValue{ArrayValue: &commonv1.ArrayValue{Values: []*commonv1.AnyValue{definition}}}},
	}
	data, mediaType, omission := toolDefinitionContent(values)
	if string(data) != `[{"name":"lookup_policy","type":"function"}]` || mediaType != "application/json" || omission != nil {
		t.Fatalf("tool definitions: data=%s media=%q omission=%#v", data, mediaType, omission)
	}
}

func TestAFRCAP003NonCanonicalStructuredContentBecomesOmission(t *testing.T) {
	values := map[string]*commonv1.AnyValue{
		"gen_ai.output.messages": {Value: &commonv1.AnyValue_DoubleValue{DoubleValue: math.NaN()}},
	}
	data, _, omission := operationContent(values, "output")
	if data != nil || omission == nil || omission.State != types.PayloadStateOmitted || omission.Reason != types.OmissionSerialization || omission.Size <= 0 {
		t.Fatalf("noncanonical content: data=%q omission=%#v", data, omission)
	}
}

func intKV(key string, value int64) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: value}}}
}
