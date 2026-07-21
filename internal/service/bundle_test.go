package service

import (
	"errors"
	"testing"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestCapsuleSchemaVersionCompatibility(t *testing.T) {
	for _, version := range []string{"1.0", "1.7", "1.000", "1.999999999999999999999999999999"} {
		if err := validateCapsuleSchemaVersion(version); err != nil {
			t.Fatalf("compatible version %q: %v", version, err)
		}
	}
	for _, version := range []string{"", "1", "1.", "1.x", "1.0.0", "01.0", "+1.0", "-1.0"} {
		err := validateCapsuleSchemaVersion(version)
		var flightErr *types.FlightError
		if !errors.As(err, &flightErr) || flightErr.Message != "invalid capsule schema version" {
			t.Fatalf("invalid version %q: %#v", version, err)
		}
	}
	for _, version := range []string{"0.9", "2.0", "27.4"} {
		err := validateCapsuleSchemaVersion(version)
		var flightErr *types.FlightError
		if !errors.As(err, &flightErr) || flightErr.Message != "unsupported capsule schema major version" {
			t.Fatalf("unsupported version %q: %#v", version, err)
		}
	}
}
