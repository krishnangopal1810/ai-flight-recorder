package otlpjson

import (
	"encoding/hex"
	"testing"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestNormalizeHexIDsForProtoDecoder(t *testing.T) {
	input := []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"5B8EFFF798038103D269B633813FC60C","spanId":"EEE19B7EC3C1B174","parentSpanId":"AAA19B7EC3C1B111","links":[{"traceId":"11111111111111111111111111111111","spanId":"2222222222222222"}]}]}]}]}`)
	normalized, err := NormalizeIDs(input)
	if err != nil {
		t.Fatal(err)
	}
	var request collectortrace.ExportTraceServiceRequest
	if err := protojson.Unmarshal(normalized, &request); err != nil {
		t.Fatal(err)
	}
	span := request.ResourceSpans[0].ScopeSpans[0].Spans[0]
	if hex.EncodeToString(span.TraceId) != "5b8efff798038103d269b633813fc60c" || hex.EncodeToString(span.SpanId) != "eee19b7ec3c1b174" || hex.EncodeToString(span.ParentSpanId) != "aaa19b7ec3c1b111" {
		t.Fatalf("OTLP IDs were not decoded from hexadecimal: %#v", span)
	}
	link := span.Links[0]
	if hex.EncodeToString(link.TraceId) != "11111111111111111111111111111111" || hex.EncodeToString(link.SpanId) != "2222222222222222" {
		t.Fatalf("linked OTLP IDs were not decoded from hexadecimal: %#v", link)
	}
}
