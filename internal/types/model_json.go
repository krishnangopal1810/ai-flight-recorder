package types

import "encoding/json"

// Capsule minor versions are forward-compatible: fields unknown to this
// binary are retained byte-for-byte at their original top-level key.
func (c RunCapsule) MarshalJSON() ([]byte, error) {
	type wire RunCapsule
	known, err := json.Marshal(wire(c))
	if err != nil {
		return nil, err
	}
	if len(c.UnknownFields) == 0 {
		return known, nil
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(known, &document); err != nil {
		return nil, err
	}
	for key, value := range c.UnknownFields {
		if _, reserved := capsuleKnownFields[key]; !reserved {
			document[key] = append(json.RawMessage(nil), value...)
		}
	}
	return json.Marshal(document)
}

func (c *RunCapsule) UnmarshalJSON(data []byte) error {
	type wire RunCapsule
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return err
	}
	*c = RunCapsule(decoded)
	for key := range capsuleKnownFields {
		delete(document, key)
	}
	if len(document) > 0 {
		c.UnknownFields = document
	}
	return nil
}

var capsuleKnownFields = map[string]struct{}{
	"schema_version": {}, "capsule_id": {}, "source_capsule_id": {}, "trace_id": {},
	"project_id": {}, "service_name": {}, "start_time": {}, "end_time": {},
	"expires_at": {}, "terminal_status": {}, "integrity": {}, "completeness": {}, "provenance": {},
	"model_configuration": {}, "artifacts": {}, "operations": {}, "boundaries": {},
	"privacy": {}, "evaluations": {}, "replay_history": {}, "extensions": {},
}
