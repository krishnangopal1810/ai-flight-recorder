package main

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestExportCapturesStreamsOnlyBoundedOTLPFiles(t *testing.T) {
	root := t.TempDir()
	want := []byte("candidate telemetry")
	if err := os.WriteFile(filepath.Join(root, "candidate.otlp.pb"), want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ignored.txt"), []byte("not telemetry"), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := exportCaptures(root, &output); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(&output)
	header, err := reader.Next()
	if err != nil {
		t.Fatal(err)
	}
	if header.Name != "candidate.otlp.pb" || header.Size != int64(len(want)) {
		t.Fatalf("unexpected archive header %#v", header)
	}
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("candidate telemetry changed: got %q want %q", got, want)
	}
	if _, err := reader.Next(); err != io.EOF {
		t.Fatalf("expected one exported file, got %v", err)
	}
}
