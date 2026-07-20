package service

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

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
	Data       []byte
	Redactions map[string]int
	Exclusions map[string]int
	Omitted    bool
	Reason     string
}

func (p *PolicyEngine) Retention(project string) time.Duration {
	if value, ok := p.policy.ProjectRetention[project]; ok {
		return value
	}
	return p.policy.DefaultRetention
}

func NewPolicyEngine(policy config.CapturePolicy) (*PolicyEngine, error) {
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
	result := PolicyResult{Redactions: map[string]int{}, Exclusions: map[string]int{}}
	if p.policy.MaxFieldSize > 0 && len(data) > p.policy.MaxFieldSize {
		result.Omitted, result.Reason = true, "payload_limit"
		return result, nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return p.applyScalar(name, string(data))
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
	return result, nil
}

func (p *PolicyEngine) applyScalar(name, value string) (PolicyResult, error) {
	result := PolicyResult{Redactions: map[string]int{}, Exclusions: map[string]int{}}
	updated, excluded, err := p.applyRules(name, value, &result)
	if err != nil {
		return PolicyResult{}, err
	}
	if excluded {
		result.Omitted, result.Reason = true, "policy_exclusion"
		return result, nil
	}
	result.Data = []byte(updated)
	return result, nil
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
			continue
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
				updated = updated[:limit]
				result.Redactions[rule.Name]++
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
