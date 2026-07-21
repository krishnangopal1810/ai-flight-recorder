package types

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestRunCapsulePreservesUnknownMinorFields(t *testing.T) {
	input := []byte(`{"schema_version":"1.7","capsule_id":"afr_x","future_contract":{"mode":"new"}}`)
	var capsule RunCapsule
	if err := json.Unmarshal(input, &capsule); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(capsule)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"future_contract":{"mode":"new"}`)) {
		t.Fatalf("unknown field was not preserved: %s", encoded)
	}
}

func TestRunCapsulePreservesUnknownMinorFieldsAtNestedDepth(t *testing.T) {
	input := []byte(`{
		"schema_version":"1.7","capsule_id":"afr_x","project_id":"original",
		"future_contract":{"large":900719925474099312345},
		"integrity":{"future_integrity":"kept"},
		"completeness":{"replay":{"state":"ready","future_completeness":"kept","missing":[{"code":"future","message":"future","future_missing":"kept"}]}},
		"provenance":{"commit_sha":"old","future_provenance":"kept","entrypoint":{"future_entrypoint":"kept"}},
		"model_configuration":{"future_model":"kept"},
		"artifacts":[{"future_artifact":"kept","payload":{"future_payload":"kept"}}],
		"operations":[{"name":"old","future_operation":"kept","input":{"future_input":"kept"}}],
		"boundaries":[{"future_boundary":"kept","response":{"future_response":"kept"},"matching_rule":{"future_matching":"kept"}}],
		"privacy":{"future_privacy":"kept"},
		"evaluations":[{"future_evaluation":"kept","evidence":{"future_evidence":"kept"}}],
		"replay_history":[{"future_replay":"kept"}]
	}`)
	var capsule RunCapsule
	if err := json.Unmarshal(input, &capsule); err != nil {
		t.Fatal(err)
	}
	capsule.ProjectID = "updated"
	capsule.Provenance.CommitSHA = "new"
	capsule.Operations[0].Name = "new"
	encoded, err := json.Marshal(capsule)
	if err != nil {
		t.Fatal(err)
	}

	assertJSONPath(t, encoded, []any{"project_id"}, "updated")
	assertJSONPath(t, encoded, []any{"provenance", "commit_sha"}, "new")
	assertJSONPath(t, encoded, []any{"operations", 0, "name"}, "new")
	for _, path := range [][]any{
		{"integrity", "future_integrity"},
		{"completeness", "replay", "future_completeness"},
		{"completeness", "replay", "missing", 0, "future_missing"},
		{"provenance", "future_provenance"},
		{"provenance", "entrypoint", "future_entrypoint"},
		{"model_configuration", "future_model"},
		{"artifacts", 0, "future_artifact"},
		{"artifacts", 0, "payload", "future_payload"},
		{"operations", 0, "future_operation"},
		{"operations", 0, "input", "future_input"},
		{"boundaries", 0, "future_boundary"},
		{"boundaries", 0, "response", "future_response"},
		{"boundaries", 0, "matching_rule", "future_matching"},
		{"privacy", "future_privacy"},
		{"evaluations", 0, "future_evaluation"},
		{"evaluations", 0, "evidence", "future_evidence"},
		{"replay_history", 0, "future_replay"},
	} {
		assertJSONPath(t, encoded, path, "kept")
	}
	assertJSONPath(t, encoded, []any{"future_contract", "large"}, json.Number("900719925474099312345"))
}

func assertJSONPath(t *testing.T, data []byte, path []any, expected any) {
	t.Helper()
	var current any
	if err := decodeJSONDocument(data, &current); err != nil {
		t.Fatal(err)
	}
	for _, component := range path {
		switch value := component.(type) {
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				t.Fatalf("path %v reached non-object %#v", path, current)
			}
			current = object[value]
		case int:
			array, ok := current.([]any)
			if !ok || value >= len(array) {
				t.Fatalf("path %v reached invalid array %#v", path, current)
			}
			current = array[value]
		}
	}
	if !reflect.DeepEqual(current, expected) {
		t.Fatalf("path %v = %#v, want %#v; document=%s", path, current, expected, data)
	}
}
