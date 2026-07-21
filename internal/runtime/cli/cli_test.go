package cli

import (
	"strings"
	"testing"
)

func TestAFRSEC003RecordPropagatesOneScopedOTLPToken(t *testing.T) {
	environment := recordEnvironment([]string{
		"PATH=/bin", "FLIGHT_CAPTURE=false", "OTEL_EXPORTER_OTLP_ENDPOINT=http://old", "FLIGHT_OTLP_TOKEN=old", "OTEL_EXPORTER_OTLP_HEADERS=x-tenant=alpha,Authorization=old",
	}, "https://flight.example.test", "frt_token")
	want := map[string]string{
		"PATH": "PATH=/bin", "FLIGHT_CAPTURE": "FLIGHT_CAPTURE=true", "OTEL_EXPORTER_OTLP_ENDPOINT": "OTEL_EXPORTER_OTLP_ENDPOINT=https://flight.example.test",
		"FLIGHT_OTLP_TOKEN": "FLIGHT_OTLP_TOKEN=frt_token", "OTEL_EXPORTER_OTLP_HEADERS": "OTEL_EXPORTER_OTLP_HEADERS=x-tenant=alpha,authorization=Bearer%20frt_token",
	}
	counts := map[string]int{}
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		counts[name]++
		if expected, ok := want[name]; ok && entry != expected {
			t.Fatalf("%s = %q, want %q", name, entry, expected)
		}
	}
	for name := range want {
		if counts[name] != 1 {
			t.Fatalf("%s occurs %d times in %#v", name, counts[name], environment)
		}
	}
}

func TestRecordEnvironmentPreservesExistingAuthenticationWithoutAFlightToken(t *testing.T) {
	base := []string{"FLIGHT_OTLP_TOKEN=sdk-token", "OTEL_EXPORTER_OTLP_HEADERS=x-api-key=existing"}
	environment := recordEnvironment(base, "http://127.0.0.1:4318", "")
	for _, expected := range base {
		found := false
		for _, entry := range environment {
			found = found || entry == expected
		}
		if !found {
			t.Fatalf("record environment removed %q: %#v", expected, environment)
		}
	}
}
