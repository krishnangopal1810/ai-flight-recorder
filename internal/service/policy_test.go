package service

import (
	"strings"
	"testing"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
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
