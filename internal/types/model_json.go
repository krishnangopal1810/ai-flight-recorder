package types

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Capsule minor versions are forward-compatible. Run capsules are immutable,
// so the original document is retained to merge unknown fields back at every
// nested object and array position during storage or export.
func (c RunCapsule) MarshalJSON() ([]byte, error) {
	type wire RunCapsule
	known, err := json.Marshal(wire(c))
	if err != nil {
		return nil, err
	}
	if len(c.unknownDocument) == 0 && len(c.UnknownFields) == 0 {
		return known, nil
	}
	var document any
	if err := decodeJSONDocument(known, &document); err != nil {
		return nil, err
	}
	if len(c.unknownDocument) > 0 {
		var original any
		if err := decodeJSONDocument(c.unknownDocument, &original); err != nil {
			return nil, err
		}
		document = mergeUnknownJSON(document, original)
	}
	root, ok := document.(map[string]any)
	if !ok {
		return nil, errors.New("capsule root must be an object")
	}
	for key, value := range c.UnknownFields {
		if _, reserved := capsuleKnownFields[key]; reserved {
			continue
		}
		var decoded any
		if err := decodeJSONDocument(value, &decoded); err != nil {
			return nil, err
		}
		root[key] = decoded
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
	c.unknownDocument = append(json.RawMessage(nil), data...)
	for key := range capsuleKnownFields {
		delete(document, key)
	}
	if len(document) > 0 {
		c.UnknownFields = document
	}
	return nil
}

func mergeUnknownJSON(current, original any) any {
	switch typed := current.(type) {
	case map[string]any:
		previous, ok := original.(map[string]any)
		if !ok {
			return current
		}
		for key, oldValue := range previous {
			newValue, exists := typed[key]
			if !exists {
				typed[key] = oldValue
				continue
			}
			typed[key] = mergeUnknownJSON(newValue, oldValue)
		}
	case []any:
		previous, ok := original.([]any)
		if !ok {
			return current
		}
		for index := 0; index < len(typed) && index < len(previous); index++ {
			typed[index] = mergeUnknownJSON(typed[index], previous[index])
		}
	}
	return current
}

func decodeJSONDocument(data []byte, destination any) error {
	if !json.Valid(data) {
		return errors.New("invalid JSON document")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(destination)
}

var capsuleKnownFields = map[string]struct{}{
	"schema_version": {}, "capsule_id": {}, "source_capsule_id": {}, "trace_id": {},
	"project_id": {}, "service_name": {}, "start_time": {}, "end_time": {},
	"expires_at": {}, "terminal_status": {}, "integrity": {}, "completeness": {}, "provenance": {},
	"model_configuration": {}, "artifacts": {}, "operations": {}, "boundaries": {},
	"privacy": {}, "evaluations": {}, "replay_history": {}, "extensions": {},
}
