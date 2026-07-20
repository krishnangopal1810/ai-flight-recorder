package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/gowebpki/jcs"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

const integrityDomain = "ai-flight-recorder:capsule:v1\x00"

func DigestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func CanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, types.NewError(types.ErrValidation, "cannot encode canonical JSON", "ensure values are JSON serializable", err)
	}
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return nil, types.NewError(types.ErrValidation, "cannot canonicalize JSON", "remove non-finite numbers and invalid JSON values", err)
	}
	return canonical, nil
}

func FinalizeCapsule(capsule *types.RunCapsule, payloads map[string][]byte) error {
	if capsule.SchemaVersion == "" {
		capsule.SchemaVersion = types.SchemaVersion
	}
	digests := make([]string, 0, len(payloads))
	for expected, data := range payloads {
		actual := DigestBytes(data)
		if expected != "" && expected != actual {
			return types.NewError(types.ErrValidation, "payload digest mismatch", "recompute the payload reference from post-policy bytes", nil)
		}
		digests = append(digests, actual)
	}
	sort.Strings(digests)
	capsule.Integrity = types.Integrity{Algorithm: "sha256", PayloadDigests: digests}
	capsule.CapsuleID = ""
	manifest, err := CanonicalJSON(capsule)
	if err != nil {
		return err
	}
	manifestDigest := DigestBytes(manifest)
	root := sha256.New()
	_, _ = root.Write([]byte(integrityDomain))
	_, _ = root.Write([]byte(manifestDigest))
	for _, digest := range digests {
		_, _ = root.Write([]byte{0})
		_, _ = root.Write([]byte(digest))
	}
	rootDigest := "sha256:" + hex.EncodeToString(root.Sum(nil))
	capsule.Integrity.ManifestDigest = manifestDigest
	capsule.Integrity.RootDigest = rootDigest
	capsule.CapsuleID = "afr_" + rootDigest[len("sha256:"):]
	return nil
}

func VerifyCapsule(capsule types.RunCapsule, loadPayload func(string) ([]byte, error)) error {
	if capsule.Integrity.Algorithm != "sha256" {
		return types.NewError(types.ErrValidation, "unsupported capsule digest algorithm", "use sha256", nil)
	}
	payloads := make(map[string][]byte, len(capsule.Integrity.PayloadDigests))
	for _, digest := range capsule.Integrity.PayloadDigests {
		data, err := loadPayload(digest)
		if err != nil {
			return types.NewError(types.ErrIncompleteCapsule, "capsule payload is unavailable", "restore the referenced payload or use playback with incomplete evidence", err)
		}
		if actual := DigestBytes(data); actual != digest {
			return types.NewError(types.ErrValidation, "capsule payload failed integrity verification", "restore a verified bundle; do not replay this capsule", fmt.Errorf("expected %s, got %s", digest, actual))
		}
		payloads[digest] = data
	}
	expectedID, expectedRoot, expectedManifest := capsule.CapsuleID, capsule.Integrity.RootDigest, capsule.Integrity.ManifestDigest
	if err := FinalizeCapsule(&capsule, payloads); err != nil {
		return err
	}
	if !bytes.Equal([]byte(capsule.CapsuleID), []byte(expectedID)) || capsule.Integrity.RootDigest != expectedRoot || capsule.Integrity.ManifestDigest != expectedManifest {
		return types.NewError(types.ErrValidation, "capsule manifest failed integrity verification", "restore a verified capsule or import the original bundle", nil)
	}
	return nil
}
