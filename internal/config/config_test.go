package config

import (
	"errors"
	"testing"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

func TestRuntimeEgressNetworkComesFromTheConfigurationBoundary(t *testing.T) {
	t.Setenv("FLIGHT_OCI_EGRESS_NETWORK", "flight_worktree_default")
	cfg := Default(t.TempDir())
	applyEnvironment(&cfg)
	if cfg.Runtime.OCIEgressNetwork != "flight_worktree_default" {
		t.Fatalf("egress network was not loaded: %#v", cfg.Runtime)
	}
}

func TestRuntimeRejectsInvalidEgressNetworkName(t *testing.T) {
	cfg := Default(t.TempDir())
	cfg.Runtime.OCIEgressNetwork = "network with spaces"
	err := cfg.Validate()
	var flightErr *types.FlightError
	if !errors.As(err, &flightErr) || flightErr.Code != types.ErrValidation {
		t.Fatalf("expected validation error, got %v", err)
	}
}
