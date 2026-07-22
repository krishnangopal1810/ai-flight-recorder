//go:build security

package security_test

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/klauspost/compress/zstd"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/repository"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

var privacySeeds = []string{
	"AFR_TEST_API_KEY_7F3A91",
	"AFR_TEST_TOKEN_9C2B84",
	"AFR_TEST_PASSWORD_4D8E17",
	"alice.privacy@example.test",
	"AFR_TEST_EXCLUDE_6A1C55",
}

type privacyEvidence struct {
	quarantineHash string
}

func TestAFRT08SeededSecretsNeverReachLocalSinks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "flight.db")
	repo, err := repository.OpenLocal(ctx, databasePath, filepath.Join(root, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	evidence := exercisePrivacySinks(t, repo, "08080808080808080808080808080808")
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	assertLocalQuarantine(t, databasePath, evidence.quarantineHash)
	assertDirectorySafe(t, "local SQLite/filesystem store", root)
}

func TestAFRT08SeededSecretsNeverReachHostedSinks(t *testing.T) {
	storage := config.HostedContractEnvironment("flight-privacy-" + uuid.NewString())
	if storage.PostgresURL == "" || storage.S3Endpoint == "" {
		t.Skip("hosted privacy contract requires FLIGHT_TEST_POSTGRES_URL and FLIGHT_TEST_S3_ENDPOINT")
	}
	ctx := context.Background()
	repo, err := repository.OpenHosted(ctx, storage)
	if err != nil {
		t.Fatal(err)
	}
	testTrace := strings.ReplaceAll(uuid.NewString(), "-", "")
	evidence := exercisePrivacySinks(t, repo, testTrace)
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	assertHostedDatabaseSafe(t, storage.PostgresURL, evidence.quarantineHash)
	assertHostedObjectsSafe(t, storage)
}

func exercisePrivacySinks(t *testing.T, repo repository.Repository, traceID string) privacyEvidence {
	t.Helper()
	ctx := context.Background()
	policy, err := service.NewPolicyEngine(config.CapturePolicy{
		Version: "privacy-contract", Mode: "strict", MaxFieldSize: 1 << 20,
		Rules: []config.PolicyRule{
			{Name: "secret", Path: "*", Pattern: `AFR_TEST_(API_KEY|TOKEN|PASSWORD)_[A-Z0-9]+`, Action: "redact", Replacement: "[REDACTED]"},
			{Name: "pii", Path: "*", Pattern: `(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`, Action: "redact", Replacement: "[REDACTED]"},
			{Name: "excluded", Path: "*", Pattern: `AFR_TEST_EXCLUDE_[A-Z0-9]+`, Action: "exclude"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := mustJSON(t, map[string]any{"api_key": privacySeeds[0], "email": privacySeeds[3], "drop": privacySeeds[4]})
	output := mustJSON(t, map[string]any{"token": privacySeeds[1], "password": privacySeeds[2]})
	toolDefinition := mustJSON(t, []any{map[string]any{"name": "lookup", "description": privacySeeds[3]}})
	artifactData := mustJSON(t, map[string]any{"password": privacySeeds[2], "token": privacySeeds[1]})
	request := service.CaptureRequest{
		TraceID: traceID, ProjectID: "privacy-" + traceID, ServiceName: privacySeeds[1], TerminalStatus: "ok",
		Provenance:      types.Provenance{RepositoryURL: privacySeeds[3], CommitSHA: "revision", Runtime: "go", EnvironmentFacts: map[string]string{"PASSWORD": privacySeeds[2]}, Entrypoint: &types.Entrypoint{Command: []string{"/agent", privacySeeds[1]}, Image: "sha256:" + strings.Repeat("a", 64)}},
		Model:           types.ModelConfiguration{Provider: privacySeeds[2], Parameters: map[string]any{"api_key": privacySeeds[0]}},
		ToolDefinitions: []service.CapturePayload{{Data: toolDefinition, MediaType: "application/json"}},
		Operations: []service.CaptureOperation{
			{Operation: types.Operation{SpanID: "0808080808080801", LogicalKey: "agent", Name: privacySeeds[0], Kind: "agent", Status: "ok", StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0), Attributes: map[string]string{"token": privacySeeds[1]}}, Input: input, InputType: "application/json", Output: output, OutputType: "application/json"},
			{Operation: types.Operation{SpanID: "0808080808080802", ParentSpanID: "0808080808080801", LogicalKey: "tool", Name: "lookup", Kind: "tool", Status: "ok", StartTime: time.Unix(1, 1), EndTime: time.Unix(1, 2)}, Input: input, InputType: "application/json", Output: output, OutputType: "application/json", Boundary: &types.Boundary{LogicalKey: privacySeeds[1], Class: "tool", RequestDigest: service.DigestBytes(input), SideEffect: "read_only", ReplayPolicy: "substitute", MatchingRule: types.MatchingRule{Mode: "exact"}}, Evaluation: &types.Evaluation{ID: privacySeeds[3], Version: "1"}},
		},
		Artifacts: []struct {
			Artifact types.Artifact
			Data     []byte
		}{{Artifact: types.Artifact{ID: "privacy-artifact", Type: "fixture", LogicalName: privacySeeds[3], Version: "1", MediaType: "application/json"}, Data: artifactData}},
		StartTime: time.Unix(1, 0), EndTime: time.Unix(2, 0),
	}
	capsules := service.NewCapsuleService(repo, policy)
	logBody := mustJSON(t, map[string]any{"message": privacySeeds[0], "nested": map[string]any{"email": privacySeeds[3]}})
	logAttributes := mustJSON(t, map[string]any{"token": privacySeeds[1], "password": privacySeeds[2], "drop": privacySeeds[4]})
	if err := capsules.StageLog(ctx, request.ProjectID, request.TraceID, service.CaptureLog{
		Record: types.LogRecord{SpanID: "0808080808080802", ServiceName: privacySeeds[1], Timestamp: time.Unix(1, 1), SeverityNumber: 17, SeverityText: "ERROR", EventName: privacySeeds[0], ScopeName: privacySeeds[3]},
		Body:   logBody, BodyType: "application/json", Attributes: logAttributes, AttributesType: "application/json",
	}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	capsule, err := capsules.Capture(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if capsule.Privacy.Redactions["secret"] == 0 || capsule.Privacy.Redactions["pii"] == 0 || capsule.Privacy.Exclusions["excluded"] == 0 {
		t.Fatalf("manifest policy summary = %#v", capsule.Privacy)
	}
	manifest, err := json.Marshal(capsule)
	if err != nil {
		t.Fatal(err)
	}
	assertNoPrivacySeeds(t, "capsule manifest and searchable identity", manifest)
	for _, raw := range [][]byte{input, output, toolDefinition, artifactData, logBody, logAttributes} {
		if bytes.Contains(manifest, []byte(service.DigestBytes(raw))) {
			t.Fatalf("capsule retained a pre-policy digest for %q", raw)
		}
	}
	listed, err := repo.ListCapsules(ctx, capsule.ProjectID, 10)
	if err != nil || len(listed) != 1 || listed[0].CapsuleID != capsule.CapsuleID {
		t.Fatalf("sanitized index round trip: %#v %v", listed, err)
	}
	for _, digest := range capsule.Integrity.PayloadDigests {
		payload, err := repo.LoadPayload(ctx, digest)
		if err != nil {
			t.Fatal(err)
		}
		assertNoPrivacySeeds(t, "content-addressed payload "+digest, payload)
	}
	var bundle bytes.Buffer
	if err := service.NewBundleService(repo).Export(ctx, capsule, &bundle); err != nil {
		t.Fatal(err)
	}
	assertBundleSafe(t, bundle.Bytes())
	quarantineTrace := strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = capsules.Capture(ctx, service.CaptureRequest{TraceID: quarantineTrace, ProjectID: "privacy", ServiceName: "unsafe\x00" + privacySeeds[1], Operations: []service.CaptureOperation{{Operation: types.Operation{SpanID: "0909090909090901", Kind: "agent"}}}})
	if err == nil {
		t.Fatal("strict policy failure was not rejected")
	}
	assertNoPrivacySeeds(t, "strict-mode diagnostic", []byte(err.Error()))
	return privacyEvidence{quarantineHash: service.DigestBytes([]byte(quarantineTrace))}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertNoPrivacySeeds(t *testing.T, label string, data []byte) {
	t.Helper()
	for _, seed := range privacySeeds {
		if bytes.Contains(data, []byte(seed)) {
			t.Fatalf("%s retained seeded value %q", label, seed)
		}
	}
}

func assertBundleSafe(t *testing.T, bundle []byte) {
	t.Helper()
	decoder, err := zstd.NewReader(bytes.NewReader(bundle))
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	archive := tar.NewReader(decoder)
	entries := 0
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		assertNoPrivacySeeds(t, "bundle entry name", []byte(header.Name))
		assertNoPrivacySeeds(t, "bundle entry "+header.Name, data)
		entries++
	}
	if entries < 2 {
		t.Fatalf("privacy bundle has only %d entries", entries)
	}
}

func assertDirectorySafe(t *testing.T, label, root string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		assertNoPrivacySeeds(t, label+" file "+path, data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertLocalQuarantine(t *testing.T, databasePath, traceHash string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM quarantine_records WHERE reason='policy_failure' AND json_extract(safe_metadata,'$.trace_id_hash')=?`, traceHash).Scan(&count); err != nil || count != 1 {
		t.Fatalf("local safe quarantine record: count=%d err=%v", count, err)
	}
}

func assertHostedDatabaseSafe(t *testing.T, postgresURL, traceHash string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, postgresURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM quarantine_records WHERE reason='policy_failure' AND safe_metadata->>'trace_id_hash'=$1`, traceHash).Scan(&count); err != nil || count != 1 {
		t.Fatalf("hosted safe quarantine record: count=%d err=%v", count, err)
	}
	rows, err := pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public'`)
	if err != nil {
		t.Fatal(err)
	}
	tables := []string{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	for _, table := range tables {
		query := `SELECT row_to_json(row_value)::text FROM ` + pgx.Identifier{table}.Sanitize() + ` AS row_value`
		dataRows, err := pool.Query(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		for dataRows.Next() {
			var data string
			if err := dataRows.Scan(&data); err != nil {
				dataRows.Close()
				t.Fatal(err)
			}
			assertNoPrivacySeeds(t, "hosted PostgreSQL table "+table, []byte(data))
		}
		if err := dataRows.Err(); err != nil {
			dataRows.Close()
			t.Fatal(err)
		}
		dataRows.Close()
	}
}

func assertHostedObjectsSafe(t *testing.T, storage config.StorageConfig) {
	t.Helper()
	endpoint, secure := strings.TrimRight(storage.S3Endpoint, "/"), storage.S3UseTLS
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" {
			t.Fatalf("parse hosted object endpoint: %v", err)
		}
		endpoint, secure = parsed.Host, parsed.Scheme == "https"
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(storage.S3AccessKey, storage.S3SecretKey, ""), Secure: secure, Region: storage.S3Region})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	objects := 0
	for object := range client.ListObjects(ctx, storage.S3Bucket, minio.ListObjectsOptions{Recursive: true}) {
		if object.Err != nil {
			t.Fatal(object.Err)
		}
		assertNoPrivacySeeds(t, "hosted object key", []byte(object.Key))
		reader, err := client.GetObject(ctx, storage.S3Bucket, object.Key, minio.GetObjectOptions{})
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		assertNoPrivacySeeds(t, fmt.Sprintf("hosted object %s", object.Key), data)
		objects++
	}
	if objects == 0 {
		t.Fatal("hosted privacy contract stored no objects")
	}
}
