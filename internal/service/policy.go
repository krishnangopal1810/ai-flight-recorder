package service

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type compiledRule struct {
	config.PolicyRule
	pattern *regexp.Regexp
}

type PolicyEngine struct {
	policy config.CapturePolicy
	rules  []compiledRule
}

type PolicyResult struct {
	Data         []byte
	Redactions   map[string]int
	Exclusions   map[string]int
	Truncations  map[string]int
	Omitted      bool
	Truncated    bool
	OriginalSize int
	Reason       string
}

func (p *PolicyEngine) Retention(project string) time.Duration {
	if value, ok := p.policy.ProjectRetention[project]; ok {
		return value
	}
	return p.policy.DefaultRetention
}

func NewPolicyEngine(policy config.CapturePolicy) (*PolicyEngine, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	engine := &PolicyEngine{policy: policy}
	for _, rule := range policy.Rules {
		compiled := compiledRule{PolicyRule: rule}
		if rule.Pattern != "" {
			pattern, err := regexp.Compile(rule.Pattern)
			if err != nil {
				return nil, types.NewError(types.ErrValidation, "invalid capture policy pattern", "fix rule "+rule.Name, err)
			}
			compiled.pattern = pattern
		}
		if rule.Action != "keep" && rule.Action != "redact" && rule.Action != "exclude" && rule.Action != "truncate" {
			return nil, types.NewError(types.ErrValidation, "invalid capture policy action", "use keep, redact, exclude, or truncate", nil)
		}
		engine.rules = append(engine.rules, compiled)
	}
	return engine, nil
}

func (p *PolicyEngine) ApplyJSON(name string, data []byte) (PolicyResult, error) {
	limit := config.PayloadLimit{Name: "max_field_size", MaxBytes: p.policy.MaxFieldSize, Action: "omit"}
	return p.apply(name, data, limit, true)
}

func (p *PolicyEngine) ApplyPayload(field, artifactClass, mediaType string, data []byte) (PolicyResult, error) {
	limit := config.PayloadLimit{Name: "max_field_size", MaxBytes: p.policy.MaxFieldSize, Action: "omit"}
	for _, candidate := range p.policy.PayloadLimits {
		if candidate.Field == field && (candidate.ArtifactClass == "" || candidate.ArtifactClass == artifactClass) {
			limit = candidate
			break
		}
	}
	return p.apply(field, data, limit, isJSONMediaType(mediaType))
}

func isJSONMediaType(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func (p *PolicyEngine) apply(name string, data []byte, limit config.PayloadLimit, structured bool) (PolicyResult, error) {
	result := newPolicyResult()
	result.OriginalSize = len(data)
	if limit.Action == "omit" && limit.MaxBytes > 0 && len(data) > limit.MaxBytes {
		result.Omitted, result.Reason = true, "payload_limit"
		return result, nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		updated, excluded, ruleErr := p.applyRules(name, string(data), &result)
		if ruleErr != nil {
			return PolicyResult{}, ruleErr
		}
		if excluded {
			result.Omitted, result.Reason = true, "policy_exclusion"
			return result, nil
		}
		result.Data = []byte(updated)
		return applyPayloadLimit(result, limit, structured)
	}
	sanitized, excluded, err := p.walk(name, value, &result)
	if err != nil {
		return PolicyResult{}, err
	}
	if excluded {
		result.Omitted, result.Reason = true, "policy_exclusion"
		return result, nil
	}
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		return PolicyResult{}, types.NewError(types.ErrValidation, "cannot serialize policy-safe payload", "omit the unsupported value or encode it as JSON", err)
	}
	result.Data = encoded
	return applyPayloadLimit(result, limit, structured)
}

func newPolicyResult() PolicyResult {
	return PolicyResult{Redactions: map[string]int{}, Exclusions: map[string]int{}, Truncations: map[string]int{}}
}

func applyPayloadLimit(result PolicyResult, limit config.PayloadLimit, structured bool) (PolicyResult, error) {
	if limit.Action == "keep" || limit.MaxBytes <= 0 || len(result.Data) <= limit.MaxBytes {
		return result, nil
	}
	if limit.Action == "omit" {
		result.Data, result.Omitted, result.Reason = nil, true, "payload_limit"
		return result, nil
	}
	truncated, ok := truncatePolicyData(result.Data, limit.MaxBytes, structured)
	if !ok {
		result.Data, result.Omitted, result.Reason = nil, true, "payload_limit"
		return result, nil
	}
	result.Data, result.Truncated, result.Reason = truncated, true, types.PayloadTruncated
	result.Truncations[limit.Name]++
	return result, nil
}

func truncatePolicyData(data []byte, maximum int, structured bool) ([]byte, bool) {
	if !structured {
		end := min(maximum, len(data))
		if utf8.Valid(data) {
			for end > 0 && !utf8.Valid(data[:end]) {
				end--
			}
		}
		return append([]byte(nil), data[:end]...), true
	}
	encode := func(size int) []byte {
		marker, _ := json.Marshal(map[string]string{"_flight_truncated_prefix_base64": base64.RawStdEncoding.EncodeToString(data[:size])})
		return marker
	}
	low, high := 0, len(data)
	for low < high {
		middle := (low + high + 1) / 2
		if len(encode(middle)) <= maximum {
			low = middle
		} else {
			high = middle - 1
		}
	}
	encoded := encode(low)
	return encoded, len(encoded) <= maximum
}

func (p *PolicyEngine) walk(current string, value any, result *PolicyResult) (any, bool, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			clean, excluded, err := p.walk(current+"."+key, child, result)
			if err != nil {
				return nil, false, err
			}
			if !excluded {
				out[key] = clean
			}
		}
		return out, false, nil
	case []any:
		out := make([]any, 0, len(typed))
		for index, child := range typed {
			clean, excluded, err := p.walk(fmt.Sprintf("%s[%d]", current, index), child, result)
			if err != nil {
				return nil, false, err
			}
			if !excluded {
				out = append(out, clean)
			}
		}
		return out, false, nil
	case string:
		return p.applyRules(current, typed, result)
	default:
		return value, false, nil
	}
}

func (p *PolicyEngine) applyRules(current, value string, result *PolicyResult) (string, bool, error) {
	updated := value
	for _, rule := range p.rules {
		matched, err := path.Match(rule.Path, current)
		if err != nil {
			return "", false, types.NewError(types.ErrValidation, "invalid capture policy path", "fix rule "+rule.Name, err)
		}
		if !matched && rule.Path != "*" {
			continue
		}
		pathPatternMatch := rule.pattern != nil && rule.pattern.MatchString(current)
		valuePatternMatch := rule.pattern != nil && rule.pattern.MatchString(updated)
		if rule.pattern != nil && !pathPatternMatch && !valuePatternMatch {
			continue
		}
		switch rule.Action {
		case "keep":
			return updated, false, nil
		case "exclude":
			result.Exclusions[rule.Name]++
			return "", true, nil
		case "redact":
			replacement := rule.Replacement
			if replacement == "" {
				replacement = "[REDACTED]"
			}
			if valuePatternMatch {
				updated = rule.pattern.ReplaceAllString(updated, replacement)
			} else {
				updated = replacement
			}
			result.Redactions[rule.Name]++
		case "truncate":
			limit := rule.Limit
			if limit <= 0 {
				limit = 256
			}
			if len(updated) > limit {
				truncated, _ := truncatePolicyData([]byte(updated), limit, false)
				updated = string(truncated)
				result.Truncated, result.Reason = true, types.PayloadTruncated
				result.Truncations[rule.Name]++
			}
		}
	}
	if strings.ContainsRune(updated, '\x00') && p.policy.Mode == "strict" {
		return "", false, types.NewError(types.ErrValidation, "policy-safe value contains a NUL byte", "exclude or encode the field", nil)
	}
	if strings.ContainsRune(updated, '\x00') {
		result.Exclusions["invalid_value"]++
		return "", true, nil
	}
	return updated, false, nil
}
