package service

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

const maxBundleSize = 2 << 30

type BundleService struct{ repository repository.Repository }

func NewBundleService(repo repository.Repository) *BundleService {
	return &BundleService{repository: repo}
}

func (s *BundleService) Export(ctx context.Context, capsule types.RunCapsule, destination io.Writer) error {
	if err := VerifyCapsule(capsule, func(digest string) ([]byte, error) { return s.repository.LoadPayload(ctx, digest) }); err != nil {
		return err
	}
	encoder, err := zstd.NewWriter(destination, zstd.WithEncoderLevel(zstd.SpeedBetterCompression), zstd.WithEncoderCRC(true))
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot initialize bundle compressor", "check the output destination", err)
	}
	tarWriter := tar.NewWriter(encoder)
	manifest, err := CanonicalJSON(capsule)
	if err != nil {
		return err
	}
	if err := writeTarFile(tarWriter, "manifest.json", manifest); err != nil {
		return err
	}
	digests := append([]string(nil), capsule.Integrity.PayloadDigests...)
	sort.Strings(digests)
	for _, digest := range digests {
		data, err := s.repository.LoadPayload(ctx, digest)
		if err != nil {
			return err
		}
		name := "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:")
		if err := writeTarFile(tarWriter, name, data); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return types.NewError(types.ErrStorage, "cannot finalize bundle archive", "check the output destination", err)
	}
	if err := encoder.Close(); err != nil {
		return types.NewError(types.ErrStorage, "cannot finalize bundle compression", "check the output destination", err)
	}
	return nil
}

func (s *BundleService) Import(ctx context.Context, source io.Reader) (types.RunCapsule, error) {
	decoder, err := zstd.NewReader(io.LimitReader(source, maxBundleSize))
	if err != nil {
		return types.RunCapsule{}, types.NewError(types.ErrValidation, "invalid capsule bundle compression", "provide a .tar.zst capsule bundle", err)
	}
	defer decoder.Close()
	tarReader := tar.NewReader(decoder)
	var capsule types.RunCapsule
	payloads := map[string][]byte{}
	manifestSeen := false
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return types.RunCapsule{}, types.NewError(types.ErrValidation, "invalid capsule archive", "export the capsule again", nextErr)
		}
		clean := path.Clean(header.Name)
		if strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
			return types.RunCapsule{}, types.NewError(types.ErrValidation, "unsafe capsule archive path", "remove traversal paths from the bundle", nil)
		}
		if header.Size < 0 || header.Size > maxBundleSize {
			return types.RunCapsule{}, types.NewError(types.ErrValidation, "capsule archive entry is too large", "configure a supported payload limit", nil)
		}
		data, readErr := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if readErr != nil || int64(len(data)) != header.Size {
			return types.RunCapsule{}, types.NewError(types.ErrValidation, "cannot read capsule archive entry", "export the capsule again", readErr)
		}
		switch {
		case clean == "manifest.json":
			if manifestSeen {
				return types.RunCapsule{}, types.NewError(types.ErrValidation, "bundle contains multiple manifests", "include exactly one manifest.json", nil)
			}
			if err := json.Unmarshal(data, &capsule); err != nil {
				return types.RunCapsule{}, types.NewError(types.ErrValidation, "invalid capsule manifest", "fix the JSON manifest", err)
			}
			manifestSeen = true
		case strings.HasPrefix(clean, "blobs/sha256/"):
			hex := strings.TrimPrefix(clean, "blobs/sha256/")
			digest := "sha256:" + hex
			if DigestBytes(data) != digest {
				return types.RunCapsule{}, types.NewError(types.ErrValidation, "bundle payload digest mismatch", "export the capsule again from a verified source", fmt.Errorf("entry %s", clean))
			}
			payloads[digest] = data
		}
	}
	if !manifestSeen {
		return types.RunCapsule{}, types.NewError(types.ErrValidation, "bundle manifest is missing", "include manifest.json", nil)
	}
	if !strings.HasPrefix(capsule.SchemaVersion, "1.") {
		return types.RunCapsule{}, types.NewError(types.ErrValidation, "unsupported capsule schema major version", "use a V1-compatible exporter", nil)
	}
	if err := VerifyCapsule(capsule, func(digest string) ([]byte, error) {
		data, ok := payloads[digest]
		if !ok {
			return nil, fmt.Errorf("missing %s", digest)
		}
		return data, nil
	}); err != nil {
		return types.RunCapsule{}, err
	}
	if err := s.repository.PutCapsule(ctx, capsule, payloads); err != nil {
		return types.RunCapsule{}, err
	}
	return capsule, nil
}

func writeTarFile(writer *tar.Writer, name string, data []byte) error {
	header := &tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC(), AccessTime: time.Unix(0, 0).UTC(), ChangeTime: time.Unix(0, 0).UTC(), Uid: 0, Gid: 0, Format: tar.FormatPAX}
	if err := writer.WriteHeader(header); err != nil {
		return types.NewError(types.ErrStorage, "cannot write bundle header", "check the output destination", err)
	}
	if _, err := io.Copy(writer, bytes.NewReader(data)); err != nil {
		return types.NewError(types.ErrStorage, "cannot write bundle payload", "check the output destination", err)
	}
	return nil
}
