package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestPolicyRedactsSecretSelectedByPathBeforePersistence(t *testing.T) {
	engine, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1024, Rules: []config.PolicyRule{{Name: "secret-name", Path: "*", Pattern: `(?i)(api[_-]?key|password|token)`, Action: "redact", Replacement: "[REDACTED]"}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.ApplyJSON("operation.input", []byte(`{"api_key":"super-secret","safe":"visible"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Data), "super-secret") {
		t.Fatalf("secret survived policy: %s", result.Data)
	}
	if !strings.Contains(string(result.Data), "[REDACTED]") || !strings.Contains(string(result.Data), "visible") {
		t.Fatalf("unexpected redaction result: %s", result.Data)
	}
}

func TestReplayOverrideIsRejectedWhenCapturePolicyWouldChangeIt(t *testing.T) {
	engine, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1024, Rules: []config.PolicyRule{{Name: "secret", Path: "*", Pattern: `AFR_SECRET_[A-Z0-9]+`, Action: "redact"}}})
	if err != nil {
		t.Fatal(err)
	}
	capsules := NewCapsuleService(nil, engine)
	if _, err := capsules.PrepareReplayOverride(json.RawMessage(`{"token":"AFR_SECRET_123"}`)); err == nil {
		t.Fatal("policy-changing replay override was accepted")
	}
	prepared, err := capsules.PrepareReplayOverride(json.RawMessage(`{"decision":"approve"}`))
	if err != nil || string(prepared) != `{"decision":"approve"}` {
		t.Fatalf("safe replay override = %s, %v", prepared, err)
	}
}

func TestAFRSEC004ProjectRetentionOverridesDefault(t *testing.T) {
	engine, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1024, DefaultRetention: 30 * 24 * time.Hour, ProjectRetention: map[string]time.Duration{"short-lived": time.Hour, "held": 0}})
	if err != nil {
		t.Fatal(err)
	}
	if engine.Retention("short-lived") != time.Hour || engine.Retention("default") != 30*24*time.Hour || engine.Retention("held") != 0 {
		t.Fatalf("project retention: short=%s default=%s held=%s", engine.Retention("short-lived"), engine.Retention("default"), engine.Retention("held"))
	}
}

func TestAFRSEC004PayloadLimitsAreOrderedByFieldAndArtifactClass(t *testing.T) {
	engine, err := NewPolicyEngine(config.CapturePolicy{
		Version: "1", Mode: "strict", MaxFieldSize: 4,
		PayloadLimits: []config.PayloadLimit{
			{Name: "keep-output", Field: "operations.output", Action: "keep"},
			{Name: "truncate-transcript", Field: "artifacts", ArtifactClass: "transcript", MaxBytes: 96, Action: "truncate"},
			{Name: "omit-artifact", Field: "artifacts", MaxBytes: 8, Action: "omit"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	kept, err := engine.ApplyPayload("operations.output", "", "text/plain", []byte("kept despite the global limit"))
	if err != nil || kept.Omitted || kept.Truncated || string(kept.Data) != "kept despite the global limit" {
		t.Fatalf("explicit keep = %#v, %v", kept, err)
	}
	structured := []byte(`{"content":"` + strings.Repeat("x", 200) + `"}`)
	truncated, err := engine.ApplyPayload("artifacts", "transcript", "application/json", structured)
	if err != nil || truncated.Omitted || !truncated.Truncated || len(truncated.Data) > 96 || truncated.OriginalSize != len(structured) || !json.Valid(truncated.Data) {
		t.Fatalf("class truncation = %#v, %v", truncated, err)
	}
	if truncated.Truncations["truncate-transcript"] != 1 || !strings.Contains(string(truncated.Data), "_flight_truncated_prefix_base64") {
		t.Fatalf("structured truncation evidence = %s %#v", truncated.Data, truncated.Truncations)
	}
	omitted, err := engine.ApplyPayload("artifacts", "screenshot", "image/png", []byte("oversized-image"))
	if err != nil || !omitted.Omitted || omitted.Reason != "payload_limit" || omitted.Data != nil {
		t.Fatalf("class omission = %#v, %v", omitted, err)
	}
}

func TestAFRSEC004ExplicitKeepStopsLaterOrderedRules(t *testing.T) {
	engine, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1024, Rules: []config.PolicyRule{
		{Name: "allow-safe", Path: "operations.input.value", Pattern: "^safe$", Action: "keep"},
		{Name: "exclude-everything", Path: "operations.input.value", Action: "exclude"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.ApplyJSON("operations.input", []byte(`{"value":"safe"}`))
	if err != nil || result.Omitted || string(result.Data) != `{"value":"safe"}` {
		t.Fatalf("ordered keep = %#v, %v", result, err)
	}
}

func TestAFRSEC004OrderedRuleTruncationIsVisible(t *testing.T) {
	engine, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 1024, Rules: []config.PolicyRule{{Name: "short-prompt", Path: "operations.input.prompt", Action: "truncate", Limit: 4}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.ApplyJSON("operations.input", []byte(`{"prompt":"abcdefgh"}`))
	if err != nil || !result.Truncated || result.Reason != types.PayloadTruncated || result.Truncations["short-prompt"] != 1 || string(result.Data) != `{"prompt":"abcd"}` {
		t.Fatalf("rule truncation = %#v, %v", result, err)
	}
}

func TestDefaultPolicyRedactsNamedSecretsAndEmailPII(t *testing.T) {
	engine, err := NewPolicyEngine(config.Default(t.TempDir()).Policy)
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.ApplyJSON("operation.input", []byte(`{"api_key":"afr_test_key_123","password":"afr_test_password_456","email":"alice.privacy@example.test"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"afr_test_key_123", "afr_test_password_456", "alice.privacy@example.test"} {
		if strings.Contains(string(result.Data), value) {
			t.Fatalf("default policy retained %q: %s", value, result.Data)
		}
	}
	if result.Redactions["authorization"] != 2 || result.Redactions["email"] != 1 {
		t.Fatalf("redaction summary = %#v", result.Redactions)
	}
}

func TestPolicyOmissionIsTyped(t *testing.T) {
	engine, err := NewPolicyEngine(config.CapturePolicy{Version: "1", Mode: "strict", MaxFieldSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.ApplyJSON("operation.input", []byte("four"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Omitted || result.Reason != "payload_limit" || result.Data != nil {
		t.Fatalf("unexpected omission: %#v", result)
	}
}
