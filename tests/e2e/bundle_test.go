//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestAFRT04BundleRoundTripPreservesIdentityOffline(t *testing.T) {
	ctx := context.Background()
	sourceRoot, targetRoot := t.TempDir(), t.TempDir()
	source, err := repository.OpenLocal(ctx, filepath.Join(sourceRoot, "flight.db"), filepath.Join(sourceRoot, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := repository.OpenLocal(ctx, filepath.Join(targetRoot, "flight.db"), filepath.Join(targetRoot, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	policy, _ := service.NewPolicyEngine(config.CapturePolicy{Version: "test", Mode: "strict", MaxFieldSize: 1 << 20})
	capsules := service.NewCapsuleService(source, policy)
	capsule, err := capsules.Capture(ctx, service.CaptureRequest{TraceID: strings.Repeat("a", 32), ProjectID: "e2e", ServiceName: "reference", StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0), Operations: []service.CaptureOperation{{Operation: types.Operation{SpanID: strings.Repeat("b", 16), Name: "answer", Kind: "agent", Status: "ok"}, Input: []byte(`{"question":"why"}`), Output: []byte(`{"answer":"evidence"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	var bundle bytes.Buffer
	if err := service.NewBundleService(source).Export(ctx, capsule, &bundle); err != nil {
		t.Fatal(err)
	}
	imported, err := service.NewBundleService(target).Import(ctx, bytes.NewReader(bundle.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if imported.CapsuleID != capsule.CapsuleID || imported.Integrity.RootDigest != capsule.Integrity.RootDigest {
		t.Fatalf("identity changed: %s != %s", imported.CapsuleID, capsule.CapsuleID)
	}
	if err := service.VerifyCapsule(imported, func(digest string) ([]byte, error) { return target.LoadPayload(ctx, digest) }); err != nil {
		t.Fatal(err)
	}
}
