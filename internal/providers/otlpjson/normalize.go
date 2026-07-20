package otlpjson

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
)

// NormalizeIDs converts OTLP JSON's hexadecimal trace and span IDs into the
// base64 byte encoding expected by the standard protobuf JSON decoder.
func NormalizeIDs(data []byte) ([]byte, error) {
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	normalizeValue(document)
	return json.Marshal(document)
}

func normalizeValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if encoded, ok := child.(string); ok && isIDField(key) {
				if decoded, err := hex.DecodeString(encoded); err == nil {
					typed[key] = base64.StdEncoding.EncodeToString(decoded)
				}
				continue
			}
			normalizeValue(child)
		}
	case []any:
		for _, child := range typed {
			normalizeValue(child)
		}
	}
}

func isIDField(name string) bool {
	return name == "traceId" || name == "spanId" || name == "parentSpanId"
}
