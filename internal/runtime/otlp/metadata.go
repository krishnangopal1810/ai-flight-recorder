package otlp

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func provenance(span, resource map[string]*commonv1.AnyValue) types.Provenance {
	read := func(keys ...string) string {
		if value := firstString(span, keys...); value != "" {
			return value
		}
		return firstString(resource, keys...)
	}
	lookup := func(key string) (*commonv1.AnyValue, bool) {
		if value, ok := span[key]; ok {
			return value, true
		}
		value, ok := resource[key]
		return value, ok
	}
	runtimeName, runtimeVersion := read("process.runtime.name"), read("process.runtime.version")
	runtimeIdentity := read("flight.runtime")
	if runtimeIdentity == "" && runtimeName != "" {
		runtimeIdentity = runtimeName
		if runtimeVersion != "" {
			runtimeIdentity += ":" + runtimeVersion
		}
	}
	provenance := types.Provenance{RepositoryURL: read("flight.repository.url"), ProjectID: read("flight.project.id"), CommitSHA: read("flight.git.commit"), DirtyDigest: read("flight.git.dirty_digest"), BuildID: read("flight.build.id"), ContainerID: read("container.id"), Runtime: runtimeIdentity}
	dependencyLockSeen := false
	provenance.DependencyLock = map[string]string{}
	if encoded, ok := lookup("flight.dependency_lock"); ok {
		dependencyLockSeen = true
		decoded := map[string]string{}
		if err := json.Unmarshal([]byte(anyString(encoded)), &decoded); err != nil || decoded == nil {
			provenance.Missing = append(provenance.Missing, "dependency_lock_invalid")
		} else {
			provenance.DependencyLock = decoded
		}
	}
	for _, values := range []map[string]*commonv1.AnyValue{resource, span} {
		for key, value := range values {
			if strings.HasPrefix(key, "flight.dependency_lock.") && strings.HasSuffix(key, ".digest") {
				name := strings.TrimSuffix(strings.TrimPrefix(key, "flight.dependency_lock."), ".digest")
				if name != "" {
					dependencyLockSeen = true
					provenance.DependencyLock[name] = anyString(value)
				}
			}
		}
	}
	if len(provenance.DependencyLock) == 0 {
		provenance.DependencyLock = nil
	}
	featureFlagsSeen := false
	if encoded, ok := lookup("flight.feature_flags"); ok {
		featureFlagsSeen = true
		decoded := map[string]any{}
		if err := json.Unmarshal([]byte(anyString(encoded)), &decoded); err != nil || decoded == nil {
			provenance.Missing = append(provenance.Missing, "feature_flags_invalid")
		} else {
			provenance.FeatureFlags = decoded
		}
	}
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
		if err := json.Unmarshal([]byte(command), &argv); err != nil || len(argv) == 0 {
			provenance.Missing = append(provenance.Missing, "entrypoint_command_invalid")
		} else {
			decodeStrings := func(attribute, missing string) []string {
				values := []string{}
				if encoded := read(attribute); encoded != "" {
					if err := json.Unmarshal([]byte(encoded), &values); err != nil {
						provenance.Missing = append(provenance.Missing, missing)
						return nil
					}
				}
				return values
			}
			timeout, timeoutErr := strconv.ParseInt(defaultString(read("flight.entrypoint.timeout_ns"), "0"), 10, 64)
			cpuLimit, cpuErr := strconv.ParseFloat(defaultString(read("flight.entrypoint.cpu_limit"), "0"), 64)
			memoryLimit, memoryErr := strconv.ParseInt(defaultString(read("flight.entrypoint.memory_limit_mb"), "0"), 10, 64)
			modelOverride, modelOverrideErr := strconv.ParseBool(defaultString(read("flight.entrypoint.model_override"), "false"))
			if timeoutErr != nil || timeout < 0 {
				provenance.Missing = append(provenance.Missing, "entrypoint_timeout_invalid")
				timeout = 0
			}
			if cpuErr != nil || cpuLimit < 0 {
				provenance.Missing = append(provenance.Missing, "entrypoint_cpu_limit_invalid")
				cpuLimit = 0
			}
			if memoryErr != nil || memoryLimit < 0 {
				provenance.Missing = append(provenance.Missing, "entrypoint_memory_limit_invalid")
				memoryLimit = 0
			}
			if modelOverrideErr != nil {
				provenance.Missing = append(provenance.Missing, "entrypoint_model_override_invalid")
				modelOverride = false
			}
			provenance.Entrypoint = &types.Entrypoint{
				Command: argv, WorkingDir: read("flight.entrypoint.working_dir"), Image: read("flight.entrypoint.image"),
				Dockerfile: read("flight.entrypoint.dockerfile"), Context: read("flight.entrypoint.context"),
				InputMode: defaultString(read("flight.entrypoint.input_mode"), "stdin_json"), OutputMode: defaultString(read("flight.entrypoint.output_mode"), "stdout_json"),
				Timeout: time.Duration(timeout), Environment: decodeStrings("flight.entrypoint.environment", "entrypoint_environment_invalid"),
				ModelHosts: decodeStrings("flight.entrypoint.model_hosts", "entrypoint_model_hosts_invalid"), ModelOverride: modelOverride,
				CPULimit: cpuLimit, MemoryLimitMB: memoryLimit,
			}
		}
	}
	if provenance.RepositoryURL == "" && provenance.ProjectID == "" {
		provenance.Missing = append(provenance.Missing, "source_identity")
	}
	if provenance.CommitSHA == "" {
		provenance.Missing = append(provenance.Missing, "commit_sha")
	}
	if provenance.BuildID == "" && provenance.ContainerID == "" {
		provenance.Missing = append(provenance.Missing, "build_or_container_identity")
	}
	if provenance.Runtime == "" {
		provenance.Missing = append(provenance.Missing, "runtime")
	}
	if !dependencyLockSeen {
		provenance.Missing = append(provenance.Missing, "dependency_lock")
	}
	if !featureFlagsSeen {
		provenance.Missing = append(provenance.Missing, "feature_flags")
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
	model := types.ModelConfiguration{Provider: read("gen_ai.provider.name", "gen_ai.system", "llm.provider", "llm.system"), RequestedModel: read("gen_ai.request.model", "llm.model_name"), ProviderReportedModel: read("gen_ai.response.model"), Parameters: map[string]any{}, ResponseMetadata: map[string]any{}}
	if invocation := read("llm.invocation_parameters"); invocation != "" {
		_ = json.Unmarshal([]byte(invocation), &model.Parameters)
	}
	for _, key := range []string{"gen_ai.request.temperature", "gen_ai.request.max_tokens", "gen_ai.request.top_p", "gen_ai.response.finish_reasons", "gen_ai.usage.input_tokens", "gen_ai.usage.output_tokens"} {
		if value := read(key); value != "" {
			if strings.HasPrefix(key, "gen_ai.request.") {
				model.Parameters[strings.TrimPrefix(key, "gen_ai.request.")] = value
			} else {
				model.ResponseMetadata[key] = value
			}
		}
	}
	for _, values := range []map[string]*commonv1.AnyValue{resource, span} {
		for key, value := range values {
			if strings.HasPrefix(key, "llm.token_count.") || strings.HasPrefix(key, "llm.cost.") {
				model.ResponseMetadata[key] = anyString(value)
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

func mergeModelConfiguration(current, observed types.ModelConfiguration) types.ModelConfiguration {
	if observed.Provider != "" {
		current.Provider = observed.Provider
	}
	if observed.RequestedModel != "" {
		current.RequestedModel = observed.RequestedModel
	}
	if observed.ProviderReportedModel != "" {
		current.ProviderReportedModel = observed.ProviderReportedModel
	}
	if len(observed.Parameters) > 0 {
		if current.Parameters == nil {
			current.Parameters = map[string]any{}
		}
		for key, value := range observed.Parameters {
			current.Parameters[key] = value
		}
	}
	if len(observed.ResponseMetadata) > 0 {
		if current.ResponseMetadata == nil {
			current.ResponseMetadata = map[string]any{}
		}
		for key, value := range observed.ResponseMetadata {
			current.ResponseMetadata[key] = value
		}
	}
	return current
}

func evaluationForOperation(operation types.Operation, values map[string]*commonv1.AnyValue) *types.Evaluation {
	id := firstString(values, "flight.evaluation.id", "flight.logical_key")
	if id == "" {
		id = operation.Name
	}
	return &types.Evaluation{
		ID:        id,
		Version:   firstString(values, "flight.evaluation.version"),
		Score:     finiteFloat(values["flight.evaluation.score"]),
		Threshold: finiteFloat(values["flight.evaluation.threshold"]),
	}
}

func finiteFloat(value *commonv1.AnyValue) *float64 {
	if value == nil {
		return nil
	}
	parsed, err := strconv.ParseFloat(anyString(value), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil
	}
	return &parsed
}
