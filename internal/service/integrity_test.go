package service

import (
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestFinalizeAndVerifyCapsule(t *testing.T) {
	payload := []byte(`{"policy":"recorded"}`)
	digest := DigestBytes(payload)
	capsule := types.RunCapsule{
		SchemaVersion: types.SchemaVersion, TraceID: "trace", ProjectID: "project", ServiceName: "agent",
		StartTime: time.Unix(1, 0).UTC(), EndTime: time.Unix(2, 0).UTC(), TerminalStatus: "error",
		Completeness: map[string]types.Completeness{},
		Operations: []types.Operation{{
			SpanID: "root", Kind: "agent", Name: "run", StartTime: time.Unix(1, 0).UTC(), EndTime: time.Unix(2, 0).UTC(),
			Input: &types.PayloadRef{Digest: digest, Size: int64(len(payload)), State: "captured"},
		}},
	}
	if err := FinalizeCapsule(&capsule, map[string][]byte{digest: payload}); err != nil {
		t.Fatal(err)
	}
	if capsule.CapsuleID == "" || capsule.Integrity.RootDigest == "" {
		t.Fatal("capsule identity was not finalized")
	}
	if err := VerifyCapsule(capsule, func(string) ([]byte, error) { return payload, nil }); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := VerifyCapsuleManifest(capsule); err != nil {
		t.Fatalf("manifest-only verify: %v", err)
	}
	tampered := append([]byte(nil), payload...)
	tampered[0] = '['
	if err := VerifyCapsule(capsule, func(string) ([]byte, error) { return tampered, nil }); err == nil {
		t.Fatal("tampered payload passed verification")
	}
}

func TestAFRPLY001ManifestVerificationDoesNotRequirePayloadAvailability(t *testing.T) {
	payload := []byte(`{"policy":"recorded"}`)
	digest := DigestBytes(payload)
	capsule := types.RunCapsule{
		SchemaVersion: types.SchemaVersion, TraceID: "trace", ProjectID: "project", ServiceName: "agent",
		StartTime: time.Unix(1, 0).UTC(), EndTime: time.Unix(2, 0).UTC(), TerminalStatus: "error",
		Completeness: map[string]types.Completeness{}, Privacy: types.PrivacySummary{Mode: "strict"},
		Operations: []types.Operation{{SpanID: "root", Kind: "agent", Name: "run", StartTime: time.Unix(1, 0).UTC(), EndTime: time.Unix(2, 0).UTC(), Status: "error", Input: &types.PayloadRef{Digest: digest, Size: int64(len(payload)), State: types.PayloadStateCaptured}}},
	}
	if err := FinalizeCapsule(&capsule, map[string][]byte{digest: payload}); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCapsuleManifest(capsule); err != nil {
		t.Fatalf("verified manifest depends on payload bytes: %v", err)
	}
	tampered := capsule
	tampered.ServiceName = "tampered"
	if err := VerifyCapsuleManifest(tampered); err == nil {
		t.Fatal("tampered manifest passed verification")
	}
	unlinked := capsule
	unlinked.Operations[0].Input = &types.PayloadRef{Digest: "sha256:" + strings.Repeat("f", 64), State: types.PayloadStateCaptured}
	if err := finalizeCapsuleDigests(&unlinked, capsule.Integrity.PayloadDigests); err == nil {
		t.Fatal("finalization accepted a captured reference outside the integrity digest tree")
	}
	if err := VerifyCapsuleManifest(unlinked); err == nil {
		t.Fatal("captured reference outside the integrity digest tree passed verification")
	}
}

func TestCapsuleIdentityIsDeterministic(t *testing.T) {
	capsule := types.RunCapsule{TraceID: "trace", ProjectID: "project", ServiceName: "agent", StartTime: time.Unix(1, 0).UTC(), EndTime: time.Unix(2, 0).UTC(), TerminalStatus: "ok", Completeness: map[string]types.Completeness{}, Operations: []types.Operation{}}
	left, right := capsule, capsule
	if err := FinalizeCapsule(&left, map[string][]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := FinalizeCapsule(&right, map[string][]byte{}); err != nil {
		t.Fatal(err)
	}
	if left.CapsuleID != right.CapsuleID {
		t.Fatalf("identities differ: %s != %s", left.CapsuleID, right.CapsuleID)
	}
}

func TestRFC8785DigestMatchesPythonAndTypeScriptSDKs(t *testing.T) {
	canonical, err := CanonicalJSON(map[string]any{"b": 1.0, "a": "€"})
	if err != nil {
		t.Fatal(err)
	}
	const expected = "sha256:2c6fbbec1f6bfa5ea5d93187c7f6960ae2fbc3f05c442ebb923b9a6777b85c76"
	if digest := DigestBytes(canonical); digest != expected {
		t.Fatalf("cross-SDK digest changed: %s", digest)
	}
}
