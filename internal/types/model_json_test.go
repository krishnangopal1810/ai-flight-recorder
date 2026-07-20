package types

import (
	"bytes"
	"encoding/json"
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
