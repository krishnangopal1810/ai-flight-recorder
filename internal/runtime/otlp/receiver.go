package otlp

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type Receiver struct {
	collectortrace.UnimplementedTraceServiceServer
	capsules  *service.CapsuleService
	grace     time.Duration
	mu        sync.Mutex
	pending   map[string]*pendingTrace
	finalized map[string]finalizedTrace
	slots     chan struct{}
}

type finalizedTrace struct {
	until   time.Time
	capsule types.RunCapsule
}

type pendingTrace struct {
	request  service.CaptureRequest
	rootSeen bool
	done     chan struct{}
	timer    *time.Timer
	capsule  *types.RunCapsule
	err      error
}

func NewReceiver(capsules *service.CapsuleService, grace ...time.Duration) *Receiver {
	lateGrace := time.Duration(0)
	if len(grace) > 0 {
		lateGrace = grace[0]
	}
	return &Receiver{capsules: capsules, grace: lateGrace, pending: map[string]*pendingTrace{}, finalized: map[string]finalizedTrace{}, slots: make(chan struct{}, 32)}
}

func (r *Receiver) Export(ctx context.Context, request *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	_, rejected, err := r.Capture(ctx, request)
	if err != nil && rejected == 0 {
		rejected = countRequestSpans(request)
	}
	response := &collectortrace.ExportTraceServiceResponse{}
	if rejected > 0 || err != nil {
		message := ""
		if err != nil {
			message = safeError(err)
		}
		response.PartialSuccess = &collectortrace.ExportTracePartialSuccess{RejectedSpans: int64(rejected), ErrorMessage: message}
	}
	return response, nil
}

func (r *Receiver) Capture(ctx context.Context, request *collectortrace.ExportTraceServiceRequest) ([]types.RunCapsule, int, error) {
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return nil, countRequestSpans(request), types.NewError(types.ErrStorage, "capture backpressure deadline exceeded", "retry the OTLP batch with exponential backoff", ctx.Err())
	}
	type traceGroup struct {
		request  service.CaptureRequest
		capture  bool
		rootSeen bool
	}
	groups := map[string]*traceGroup{}
	rejected := 0
	for _, resourceSpans := range request.ResourceSpans {
		resourceAttributes := attributes(resourceSpans.Resource)
		serviceName := first(resourceAttributes, "service.name", "flight.service.name")
		projectID := first(resourceAttributes, "flight.project.id", "service.namespace")
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			semanticVersion := ""
			if scopeSpans.SchemaUrl != "" {
				semanticVersion = scopeSpans.SchemaUrl
			} else if resourceSpans.SchemaUrl != "" {
				semanticVersion = resourceSpans.SchemaUrl
			}
			for _, span := range scopeSpans.Spans {
				if len(span.TraceId) != 16 {
					rejected++
					continue
				}
				traceID := hex.EncodeToString(span.TraceId)
				groupKey := projectID + "\x00" + traceID
				group := groups[groupKey]
				if group == nil {
					group = &traceGroup{request: service.CaptureRequest{TraceID: traceID, SourceCapsuleID: first(resourceAttributes, "flight.source_capsule_id"), ProjectID: projectID, ServiceName: serviceName, TerminalStatus: "ok"}}
					groups[groupKey] = group
				}
				if len(span.SpanId) != 8 {
					rejected++
					group.request.IngestRejected++
					continue
				}
				spanAttributes := keyValues(span.Attributes)
				if boolValue(spanAttributes["flight.capture"]) || boolValue(resourceAttributes["flight.capture"]) {
					group.capture = true
				}
				root := len(span.ParentSpanId) == 0
				if root {
					group.rootSeen = true
					if span.Status != nil && span.Status.Code == tracev1.Status_STATUS_CODE_ERROR {
						group.request.TerminalStatus = "error"
					}
					group.request.Provenance = provenance(spanAttributes, resourceAttributes)
					group.request.Model = modelConfiguration(spanAttributes, resourceAttributes)
				}
				operation := types.Operation{
					SpanID: hex.EncodeToString(span.SpanId), ParentSpanID: hex.EncodeToString(span.ParentSpanId), LogicalKey: stringValue(spanAttributes["flight.logical_key"]),
					Kind: operationKind(spanAttributes), Name: span.Name, StartTime: time.Unix(0, int64(span.StartTimeUnixNano)).UTC(), EndTime: time.Unix(0, int64(span.EndTimeUnixNano)).UTC(),
					Status: statusName(span.Status), SemanticConventionVersion: semanticVersion, Attributes: safeAttributes(spanAttributes),
				}
				captured := service.CaptureOperation{Operation: operation}
				if input := firstValue(spanAttributes, "input.value", "openinference.span.input.value", "flight.input", "gen_ai.input.messages"); input != "" {
					captured.Input, captured.InputType = []byte(input), firstString(spanAttributes, "input.mime_type", "openinference.span.input.mime_type", "flight.input.mime_type")
					if captured.InputType == "" {
						captured.InputType = "application/json"
					}
				}
				if output := firstValue(spanAttributes, "output.value", "openinference.span.output.value", "flight.output", "gen_ai.output.messages"); output != "" {
					captured.Output, captured.OutputType = []byte(output), firstString(spanAttributes, "output.mime_type", "openinference.span.output.mime_type", "flight.output.mime_type")
					if captured.OutputType == "" {
						captured.OutputType = "application/json"
					}
				}
				if key := firstString(spanAttributes, "flight.boundary.logical_key"); key != "" {
					ignorePaths := []string{}
					_ = json.Unmarshal([]byte(firstString(spanAttributes, "flight.boundary.ignore_paths")), &ignorePaths)
					captured.Boundary = &types.Boundary{LogicalKey: key, ParentLogicalKey: firstString(spanAttributes, "flight.boundary.parent_logical_key"), Class: firstString(spanAttributes, "flight.boundary.class"), RequestDigest: firstString(spanAttributes, "flight.boundary.request_digest"), SideEffect: defaultString(firstString(spanAttributes, "flight.boundary.side_effect"), "unknown"), ReplayPolicy: defaultString(firstString(spanAttributes, "flight.boundary.replay_policy"), "substitute"), MatchingRule: types.MatchingRule{Mode: defaultString(firstString(spanAttributes, "flight.boundary.matching_mode"), "exact"), IgnorePaths: ignorePaths}, Sandbox: firstString(spanAttributes, "flight.boundary.sandbox")}
				}
				group.request.Operations = append(group.request.Operations, captured)
				for eventIndex, event := range span.Events {
					eventAttributes := keyValues(event.Attributes)
					switch event.Name {
					case "flight.artifact":
						artifact, data := artifactEvent(operation.SpanID, eventIndex, eventAttributes)
						group.request.Artifacts = append(group.request.Artifacts, struct {
							Artifact types.Artifact
							Data     []byte
						}{Artifact: artifact, Data: data})
					case "flight.sandbox":
						if root && group.request.Provenance.Entrypoint != nil {
							if group.request.Provenance.Entrypoint.Sandboxes == nil {
								group.request.Provenance.Entrypoint.Sandboxes = map[string]string{}
							}
							name, destination := firstString(eventAttributes, "flight.sandbox.name"), firstString(eventAttributes, "flight.sandbox.destination")
							if name != "" && destination != "" {
								group.request.Provenance.Entrypoint.Sandboxes[name] = destination
							}
						}
					}
				}
			}
		}
	}
	result := []types.RunCapsule{}
	var captureErr error
	for key, group := range groups {
		if !group.capture {
			continue
		}
		capsule, lateRejected, err := r.assemble(ctx, key, group.request, group.rootSeen)
		rejected += lateRejected
		if err != nil {
			if lateRejected == 0 {
				rejected += len(group.request.Operations)
			}
			captureErr = err
			continue
		}
		if capsule != nil {
			result = append(result, *capsule)
		}
	}
	return result, rejected, captureErr
}

func countRequestSpans(request *collectortrace.ExportTraceServiceRequest) int {
	count := 0
	for _, resource := range request.ResourceSpans {
		for _, scope := range resource.ScopeSpans {
			count += len(scope.Spans)
		}
	}
	return count
}

func (r *Receiver) assemble(ctx context.Context, key string, fragment service.CaptureRequest, rootSeen bool) (*types.RunCapsule, int, error) {
	r.mu.Lock()
	now := time.Now()
	for finalizedKey, finalized := range r.finalized {
		if now.After(finalized.until) {
			delete(r.finalized, finalizedKey)
		}
	}
	if finalized, late := r.finalized[key]; late {
		if containsOnlyRecordedSpans(finalized.capsule, fragment) {
			capsule := finalized.capsule
			r.mu.Unlock()
			return &capsule, 0, nil
		}
		r.mu.Unlock()
		_ = r.capsules.QuarantineDiagnostic(context.Background(), "late_span", fragment.TraceID, len(fragment.Operations))
		return nil, len(fragment.Operations), types.NewError(types.ErrValidation, "spans arrived after capsule finalization", "increase late_span_grace if the producer exports descendants slowly", nil)
	}
	pending := r.pending[key]
	if pending == nil {
		pending = &pendingTrace{request: fragment, rootSeen: rootSeen, done: make(chan struct{})}
		r.pending[key] = pending
		pending.timer = time.AfterFunc(r.grace, func() { r.finalize(key, pending) })
	} else {
		mergeCaptureRequest(&pending.request, fragment)
		if rootSeen && !pending.rootSeen {
			pending.rootSeen = true
			if pending.timer.Stop() {
				pending.timer.Reset(r.grace)
			}
		}
	}
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	case <-pending.done:
		return pending.capsule, 0, pending.err
	}
}

func (r *Receiver) finalize(key string, pending *pendingTrace) {
	r.mu.Lock()
	if r.pending[key] != pending {
		r.mu.Unlock()
		return
	}
	delete(r.pending, key)
	request := pending.request
	if !pending.rootSeen {
		request.TraceID = ""
	}
	r.mu.Unlock()
	sort.SliceStable(request.Operations, func(i, j int) bool {
		return request.Operations[i].Operation.StartTime.Before(request.Operations[j].Operation.StartTime)
	})
	if len(request.Operations) > 0 {
		request.StartTime = request.Operations[0].Operation.StartTime
		request.EndTime = request.Operations[len(request.Operations)-1].Operation.EndTime
	}
	capsule, err := r.capsules.Capture(context.Background(), request)
	r.mu.Lock()
	if err == nil {
		pending.capsule = &capsule
		r.finalized[key] = finalizedTrace{until: time.Now().Add(maxDuration(2*r.grace, time.Minute)), capsule: capsule}
	}
	pending.err = err
	close(pending.done)
	r.mu.Unlock()
}

func mergeCaptureRequest(target *service.CaptureRequest, fragment service.CaptureRequest) {
	if fragment.Provenance.Entrypoint != nil || fragment.Provenance.CommitSHA != "" {
		target.Provenance = fragment.Provenance
		target.Model = fragment.Model
	}
	if fragment.SourceCapsuleID != "" {
		target.SourceCapsuleID = fragment.SourceCapsuleID
	}
	if fragment.TerminalStatus == "error" {
		target.TerminalStatus = "error"
	}
	target.IngestRejected += fragment.IngestRejected
	seen := map[string]bool{}
	for _, operation := range target.Operations {
		seen[operation.Operation.SpanID] = true
	}
	for _, operation := range fragment.Operations {
		if !seen[operation.Operation.SpanID] {
			target.Operations = append(target.Operations, operation)
			seen[operation.Operation.SpanID] = true
		}
	}
	artifactSeen := map[string]bool{}
	for _, artifact := range target.Artifacts {
		artifactSeen[artifact.Artifact.ID] = true
	}
	for _, artifact := range fragment.Artifacts {
		if !artifactSeen[artifact.Artifact.ID] {
			target.Artifacts = append(target.Artifacts, artifact)
			artifactSeen[artifact.Artifact.ID] = true
		}
	}
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}

func containsOnlyRecordedSpans(capsule types.RunCapsule, fragment service.CaptureRequest) bool {
	seen := map[string]bool{}
	for _, operation := range capsule.Operations {
		seen[operation.SpanID] = true
	}
	for _, operation := range fragment.Operations {
		if !seen[operation.Operation.SpanID] {
			return false
		}
	}
	return true
}

func attributes(resource *resourcev1.Resource) map[string]*commonv1.AnyValue {
	if resource == nil {
		return map[string]*commonv1.AnyValue{}
	}
	return keyValues(resource.Attributes)
}
func keyValues(values []*commonv1.KeyValue) map[string]*commonv1.AnyValue {
	result := map[string]*commonv1.AnyValue{}
	for _, item := range values {
		result[item.Key] = item.Value
	}
	return result
}

func anyString(value *commonv1.AnyValue) string {
	if value == nil {
		return ""
	}
	switch typed := value.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return typed.StringValue
	case *commonv1.AnyValue_BoolValue:
		return strconv.FormatBool(typed.BoolValue)
	case *commonv1.AnyValue_IntValue:
		return strconv.FormatInt(typed.IntValue, 10)
	case *commonv1.AnyValue_DoubleValue:
		return strconv.FormatFloat(typed.DoubleValue, 'g', -1, 64)
	case *commonv1.AnyValue_BytesValue:
		return hex.EncodeToString(typed.BytesValue)
	case *commonv1.AnyValue_ArrayValue:
		parts := []string{}
		for _, item := range typed.ArrayValue.Values {
			parts = append(parts, anyString(item))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *commonv1.AnyValue_KvlistValue:
		parts := []string{}
		for _, item := range typed.KvlistValue.Values {
			parts = append(parts, fmt.Sprintf("%q:%q", item.Key, anyString(item.Value)))
		}
		sort.Strings(parts)
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return ""
	}
}

func stringValue(value *commonv1.AnyValue) string { return anyString(value) }
func boolValue(value *commonv1.AnyValue) bool {
	parsed, _ := strconv.ParseBool(anyString(value))
	return parsed
}
func first(values map[string]*commonv1.AnyValue, keys ...string) string {
	return firstString(values, keys...)
}
func firstString(values map[string]*commonv1.AnyValue, keys ...string) string {
	for _, key := range keys {
		if value := anyString(values[key]); value != "" {
			return value
		}
	}
	return ""
}
func firstValue(values map[string]*commonv1.AnyValue, keys ...string) string {
	return firstString(values, keys...)
}
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func operationKind(values map[string]*commonv1.AnyValue) string {
	kind := firstString(values, "openinference.span.kind", "gen_ai.operation.name", "flight.operation.kind")
	if kind == "" {
		return "internal"
	}
	switch normalized := strings.ToLower(kind); normalized {
	case "invoke_agent", "create_agent":
		return "agent"
	case "chat", "text_completion", "generate_content":
		return "llm"
	case "execute_tool", "tool_call":
		return "tool"
	default:
		return normalized
	}
}

func statusName(status *tracev1.Status) string {
	if status == nil {
		return "unset"
	}
	switch status.Code {
	case tracev1.Status_STATUS_CODE_OK:
		return "ok"
	case tracev1.Status_STATUS_CODE_ERROR:
		return "error"
	default:
		return "unset"
	}
}

func provenance(span, resource map[string]*commonv1.AnyValue) types.Provenance {
	read := func(keys ...string) string {
		if value := firstString(span, keys...); value != "" {
			return value
		}
		return firstString(resource, keys...)
	}
	provenance := types.Provenance{RepositoryURL: read("flight.repository.url"), ProjectID: read("flight.project.id"), CommitSHA: read("flight.git.commit"), DirtyDigest: read("flight.git.dirty_digest"), BuildID: read("flight.build.id"), ContainerID: read("container.id"), Runtime: read("process.runtime.name") + ":" + read("process.runtime.version")}
	provenance.EnvironmentFacts = map[string]string{}
	for key, value := range resource {
		if strings.HasPrefix(key, "flight.environment.") && strings.HasSuffix(key, ".digest") {
			provenance.EnvironmentFacts[strings.TrimSuffix(strings.TrimPrefix(key, "flight.environment."), ".digest")] = anyString(value)
		}
	}
	for key, value := range span {
		if strings.HasPrefix(key, "flight.environment.") && strings.HasSuffix(key, ".digest") {
			provenance.EnvironmentFacts[strings.TrimSuffix(strings.TrimPrefix(key, "flight.environment."), ".digest")] = anyString(value)
		}
	}
	if len(provenance.EnvironmentFacts) == 0 {
		provenance.EnvironmentFacts = nil
	}
	if command := read("flight.entrypoint.command"); command != "" {
		argv := []string{}
		if json.Unmarshal([]byte(command), &argv) != nil {
			argv = strings.Fields(command)
		}
		modelHosts := []string{}
		_ = json.Unmarshal([]byte(read("flight.entrypoint.model_hosts")), &modelHosts)
		provenance.Entrypoint = &types.Entrypoint{Command: argv, Image: read("flight.entrypoint.image"), InputMode: defaultString(read("flight.entrypoint.input_mode"), "stdin_json"), OutputMode: defaultString(read("flight.entrypoint.output_mode"), "stdout_json"), ModelHosts: modelHosts}
	}
	if provenance.CommitSHA == "" {
		provenance.Missing = append(provenance.Missing, "commit_sha")
	}
	if provenance.Entrypoint == nil {
		provenance.Missing = append(provenance.Missing, "entrypoint")
	}
	return provenance
}

func modelConfiguration(span, resource map[string]*commonv1.AnyValue) types.ModelConfiguration {
	read := func(keys ...string) string {
		if value := firstString(span, keys...); value != "" {
			return value
		}
		return firstString(resource, keys...)
	}
	model := types.ModelConfiguration{Provider: read("gen_ai.system"), RequestedModel: read("gen_ai.request.model"), ProviderReportedModel: read("gen_ai.response.model"), Parameters: map[string]any{}, ResponseMetadata: map[string]any{}}
	for _, key := range []string{"gen_ai.request.temperature", "gen_ai.request.max_tokens", "gen_ai.request.top_p", "gen_ai.response.finish_reasons", "gen_ai.usage.input_tokens", "gen_ai.usage.output_tokens"} {
		if value := read(key); value != "" {
			if strings.HasPrefix(key, "gen_ai.request.") {
				model.Parameters[strings.TrimPrefix(key, "gen_ai.request.")] = value
			} else {
				model.ResponseMetadata[key] = value
			}
		}
	}
	if len(model.Parameters) == 0 {
		model.Parameters = nil
	}
	if len(model.ResponseMetadata) == 0 {
		model.ResponseMetadata = nil
	}
	return model
}

func artifactEvent(spanID string, index int, values map[string]*commonv1.AnyValue) (types.Artifact, []byte) {
	artifact := types.Artifact{ID: fmt.Sprintf("artifact_%s_%d", spanID, index), Type: firstString(values, "flight.artifact.type"), LogicalName: firstString(values, "flight.artifact.logical_name"), Version: firstString(values, "flight.artifact.version"), MediaType: firstString(values, "flight.artifact.media_type"), State: "omitted"}
	data, err := base64.StdEncoding.DecodeString(firstString(values, "flight.artifact.content_base64"))
	if err != nil || len(data) == 0 {
		return artifact, nil
	}
	artifact.State = "captured"
	return artifact, data
}

func safeAttributes(values map[string]*commonv1.AnyValue) map[string]string {
	allowedPrefixes := []string{"flight.", "openinference.span.kind", "gen_ai.operation.name", "gen_ai.system", "gen_ai.request.model", "gen_ai.response.model"}
	blocked := map[string]bool{"flight.input": true, "flight.output": true, "flight.feature_flags": true, "flight.repository.url": true, "flight.artifact.content_base64": true}
	result := map[string]string{}
	for key, value := range values {
		if blocked[key] {
			continue
		}
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(key, prefix) {
				result[key] = anyString(value)
				break
			}
		}
	}
	return result
}

func safeError(err error) string {
	if typed, ok := err.(*types.FlightError); ok {
		return string(typed.Code) + ": " + typed.Message
	}
	return "internal: capture failed"
}
