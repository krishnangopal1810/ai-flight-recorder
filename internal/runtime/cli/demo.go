package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

const (
	demoAPIKey   = "AFR_TEST_API_KEY_7F3A91"
	demoToken    = "AFR_TEST_TOKEN_9C2B84"
	demoPassword = "AFR_TEST_PASSWORD_4D8E17"
	demoPII      = "alice.privacy@example.test"
)

func newDemo(options *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "demo", Short: "Manage deterministic local evidence fixtures"}
	seed := &cobra.Command{Use: "seed", Short: "Record baseline and divergent demo capsules", RunE: func(_ *cobra.Command, _ []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		ids := []string{}
		seen := map[string]bool{}
		for index, variant := range []string{"baseline", "candidate"} {
			capsules, err := client.IngestOTLP(context.Background(), demoTrace(byte(index+1), variant))
			if err != nil {
				return err
			}
			for _, capsule := range capsules {
				if !seen[capsule.CapsuleID] {
					ids = append(ids, capsule.CapsuleID)
					seen[capsule.CapsuleID] = true
				}
			}
		}
		return writeOutput(os.Stdout, map[string]any{"seeded": ids, "route": "/capsules/" + firstID(ids)}, options.json)
	}}
	incomplete := &cobra.Command{Use: "seed-incomplete", Short: "Record a capsule with one missing tool response", RunE: func(_ *cobra.Command, _ []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		capsules, err := client.IngestOTLP(context.Background(), demoTrace(3, "incomplete"))
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(capsules))
		for _, capsule := range capsules {
			if capsule.ServiceName == "incomplete-agent" {
				ids = append(ids, capsule.CapsuleID)
			}
		}
		if len(ids) != 1 {
			return types.NewError(types.ErrValidation, "incomplete demo fixture identity is ambiguous", "tear down this worktree stack and reseed deterministic demo data", nil)
		}
		return writeOutput(os.Stdout, map[string]any{"seeded": ids, "route": "/capsules/" + firstID(ids)}, options.json)
	}}
	sensitive := &cobra.Command{Use: "seed-sensitive", Short: "Record policy-redacted evidence and one safe quarantine diagnostic", RunE: func(_ *cobra.Command, _ []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		capsules, err := client.IngestOTLP(context.Background(), demoTrace(4, "sensitive"))
		if err != nil {
			return err
		}
		var selected *types.RunCapsule
		for index := range capsules {
			if capsules[index].ServiceName == "sensitive-agent" {
				selected = &capsules[index]
			}
		}
		if selected == nil || selected.Privacy.Redactions["authorization"] == 0 || selected.Privacy.Redactions["email"] == 0 {
			return types.NewError(types.ErrValidation, "sensitive demo fixture was not redacted", "restore the default capture policy and reseed deterministic evidence", nil)
		}
		_, quarantineErr := client.IngestOTLP(context.Background(), demoQuarantineTrace())
		var flightErr *types.FlightError
		if !errors.As(quarantineErr, &flightErr) || flightErr.Code != types.ErrValidation {
			return types.NewError(types.ErrValidation, "sensitive quarantine fixture was not rejected", "verify strict capture mode and the missing-root diagnostic path", quarantineErr)
		}
		return writeOutput(os.Stdout, map[string]any{"seeded": []string{selected.CapsuleID}, "route": "/capsules/" + selected.CapsuleID, "quarantine_code": flightErr.Code, "redactions": selected.Privacy.Redactions}, options.json)
	}}
	command.AddCommand(seed, incomplete, sensitive)
	return command
}

func demoTrace(suffix byte, variant string) *collectortrace.ExportTraceServiceRequest {
	traceID := make([]byte, 16)
	traceID[15] = suffix
	rootID := []byte{0, 0, 0, 0, 0, 0, suffix, 1}
	toolID := []byte{0, 0, 0, 0, 0, 0, suffix, 2}
	start := time.Date(2026, 7, 21, 9, 0, int(suffix), 0, time.UTC)
	prompt, tool := "Answer with the verified policy", "policy.lookup"
	if variant == "candidate" {
		prompt, tool = "Answer from memory", "email.send"
	}
	serviceName := "policy-agent"
	if variant == "incomplete" {
		serviceName = "incomplete-agent"
	} else if variant == "sensitive" {
		serviceName = "sensitive-agent"
	}
	resource := &resourcev1.Resource{Attributes: []*commonv1.KeyValue{demoString("service.name", serviceName), demoString("flight.project.id", "demo"), demoBool("flight.capture", true)}}
	root := &tracev1.Span{TraceId: traceID, SpanId: rootID, Name: "answer-policy", StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(start.Add(2 * time.Second).UnixNano()), Status: &tracev1.Status{Code: tracev1.Status_STATUS_CODE_OK}, Attributes: []*commonv1.KeyValue{demoString("openinference.span.kind", "AGENT"), demoString("flight.logical_key", "agent.answer"), demoString("input.value", fmt.Sprintf(`{"prompt":%q}`, prompt)), demoString("output.value", `{"answer":"policy result"}`), demoString("flight.git.commit", "demo-"+variant), demoString("flight.entrypoint.command", `["/agent"]`), demoString("flight.entrypoint.image", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}}
	toolAttributes := []*commonv1.KeyValue{demoString("openinference.span.kind", "TOOL"), demoString("flight.logical_key", "tool.policy"), demoString("flight.boundary.logical_key", "tool.policy"), demoString("flight.boundary.class", "tool"), demoString("flight.boundary.side_effect", "read_only"), demoString("flight.boundary.replay_policy", "substitute"), demoString("flight.boundary.request_digest", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), demoString("input.value", `{"policy_id":"refund"}`)}
	if variant != "incomplete" {
		toolAttributes = append(toolAttributes, demoString("output.value", `{"rule":"refunds within 30 days"}`))
	}
	if variant == "sensitive" {
		root.Attributes = append(root.Attributes,
			demoString("input.value", fmt.Sprintf(`{"api_key":%q,"email":%q,"password":%q}`, demoAPIKey, demoPII, demoPassword)),
			demoString("output.value", fmt.Sprintf(`{"token":%q}`, demoToken)),
			demoString("flight.environment.PASSWORD.digest", demoPassword),
			demoString("llm.invocation_parameters", fmt.Sprintf(`{"api_key":%q}`, demoAPIKey)),
			demoString("gen_ai.tool.definitions", fmt.Sprintf(`[{"name":"lookup","description":%q}]`, demoPII)),
		)
		artifact := []byte(fmt.Sprintf(`{"password":%q,"email":%q}`, demoPassword, demoPII))
		root.Events = []*tracev1.Span_Event{{Name: "flight.artifact", Attributes: []*commonv1.KeyValue{
			demoString("flight.artifact.type", "fixture"), demoString("flight.artifact.logical_name", "sensitive-policy"), demoString("flight.artifact.version", "1"),
			demoString("flight.artifact.media_type", "application/json"), demoString("flight.artifact.content_base64", base64.StdEncoding.EncodeToString(artifact)),
		}}}
		toolAttributes = append(toolAttributes,
			demoString("input.value", fmt.Sprintf(`{"api_key":%q}`, demoAPIKey)),
			demoString("output.value", fmt.Sprintf(`{"token":%q,"email":%q}`, demoToken, demoPII)),
		)
	}
	toolSpan := &tracev1.Span{TraceId: traceID, SpanId: toolID, ParentSpanId: rootID, Name: tool, StartTimeUnixNano: uint64(start.Add(time.Second).UnixNano()), EndTimeUnixNano: uint64(start.Add(1500 * time.Millisecond).UnixNano()), Status: &tracev1.Status{Code: tracev1.Status_STATUS_CODE_OK}, Attributes: toolAttributes}
	return &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{
			{
				Resource: resource,
				ScopeSpans: []*tracev1.ScopeSpans{
					{
						SchemaUrl: "https://opentelemetry.io/schemas/1.30.0",
						Spans:     []*tracev1.Span{root, toolSpan},
					},
				},
			},
		},
	}
}

func demoQuarantineTrace() *collectortrace.ExportTraceServiceRequest {
	request := demoTrace(5, "sensitive")
	request.ResourceSpans[0].Resource.Attributes[0] = demoString("service.name", "quarantine-"+demoToken)
	for _, span := range request.ResourceSpans[0].ScopeSpans[0].Spans {
		if len(span.ParentSpanId) == 0 {
			span.ParentSpanId = []byte{0, 0, 0, 0, 0, 0, 5, 9}
		}
	}
	return request
}

func demoString(key, value string) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: value}}}
}

func demoBool(key string, value bool) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_BoolValue{BoolValue: value}}}
}

func firstID(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
