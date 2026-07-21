package otlp

import (
	"encoding/base64"
	"encoding/json"
	"strconv"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func operationContent(values map[string]*commonv1.AnyValue, direction string) ([]byte, string, *types.PayloadRef) {
	keys := []string{"input.value", "openinference.span.input.value", "flight.input", "gen_ai.input.messages", "gen_ai.system_instructions", "gen_ai.tool.call.arguments", "gen_ai.retrieval.query.text"}
	mimeKeys := []string{"input.mime_type", "openinference.span.input.mime_type", "flight.input.mime_type"}
	if direction == "output" {
		keys = []string{"output.value", "openinference.span.output.value", "flight.output", "gen_ai.output.messages", "gen_ai.tool.call.result", "gen_ai.retrieval.documents"}
		mimeKeys = []string{"output.mime_type", "openinference.span.output.mime_type", "flight.output.mime_type"}
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		data, structured, err := encodeContentValue(value)
		mediaType := firstString(values, mimeKeys...)
		if mediaType == "" {
			if structured || looksLikeJSON(data) {
				mediaType = "application/json"
			} else {
				mediaType = "text/plain"
			}
		}
		if err != nil {
			return nil, mediaType, &types.PayloadRef{MediaType: mediaType, Size: int64(proto.Size(value)), State: types.PayloadStateOmitted, Reason: types.OmissionSerialization}
		}
		return data, mediaType, nil
	}
	prefix := "flight." + direction + ".omission."
	reason, present := values[prefix+"reason"]
	if !present {
		return nil, "", nil
	}
	ref := &types.PayloadRef{
		Digest:    firstString(values, prefix+"digest"),
		MediaType: firstString(values, prefix+"media_type"),
		State:     types.PayloadStateOmitted,
		Reason:    anyString(reason),
	}
	if size, err := strconv.ParseInt(firstString(values, prefix+"size"), 10, 64); err == nil {
		ref.Size = size
	} else {
		ref.Size = -1
	}
	return nil, ref.MediaType, ref
}

func toolDefinitionContent(values map[string]*commonv1.AnyValue) ([]byte, string, *types.PayloadRef) {
	for _, key := range []string{"gen_ai.tool.definitions", "flight.tool.definitions"} {
		if value, ok := values[key]; ok {
			data, _, err := encodeContentValue(value)
			if err != nil {
				return nil, "application/json", &types.PayloadRef{MediaType: "application/json", Size: int64(proto.Size(value)), State: types.PayloadStateOmitted, Reason: types.OmissionSerialization}
			}
			return data, "application/json", nil
		}
	}
	prefix := "flight.tool_definitions.omission."
	if reason := firstString(values, prefix+"reason"); reason != "" {
		size, err := strconv.ParseInt(firstString(values, prefix+"size"), 10, 64)
		if err != nil {
			size = -1
		}
		return nil, "application/json", &types.PayloadRef{Digest: firstString(values, prefix+"digest"), MediaType: "application/json", Size: size, State: types.PayloadStateOmitted, Reason: reason}
	}
	return nil, "", nil
}

func encodeContentValue(value *commonv1.AnyValue) ([]byte, bool, error) {
	if value == nil {
		return []byte("null"), true, nil
	}
	if text, ok := value.Value.(*commonv1.AnyValue_StringValue); ok {
		return []byte(text.StringValue), false, nil
	}
	encoded, err := service.CanonicalJSON(typedContentValue(value))
	if err != nil {
		return nil, true, err
	}
	return encoded, true, nil
}

func typedContentValue(value *commonv1.AnyValue) any {
	if value == nil {
		return nil
	}
	switch typed := value.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return typed.StringValue
	case *commonv1.AnyValue_BoolValue:
		return typed.BoolValue
	case *commonv1.AnyValue_IntValue:
		return typed.IntValue
	case *commonv1.AnyValue_DoubleValue:
		return typed.DoubleValue
	case *commonv1.AnyValue_BytesValue:
		return base64.StdEncoding.EncodeToString(typed.BytesValue)
	case *commonv1.AnyValue_ArrayValue:
		values := make([]any, 0, len(typed.ArrayValue.Values))
		for _, item := range typed.ArrayValue.Values {
			values = append(values, typedContentValue(item))
		}
		return values
	case *commonv1.AnyValue_KvlistValue:
		values := make(map[string]any, len(typed.KvlistValue.Values))
		for _, item := range typed.KvlistValue.Values {
			values[item.Key] = typedContentValue(item.Value)
		}
		return values
	default:
		return nil
	}
}

func looksLikeJSON(data []byte) bool {
	return json.Valid(data)
}
