package repository

import (
	"encoding/json"
	"reflect"
)

// jsonDocumentsEqual compares decoded documents so PostgreSQL JSONB
// normalization cannot turn an idempotent capsule retry into a conflict.
func jsonDocumentsEqual(left, right []byte) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}
