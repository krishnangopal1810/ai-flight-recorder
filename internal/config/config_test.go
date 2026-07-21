package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

const testBootstrapToken = "frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

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

func TestAFRSEC003DefaultServiceIsLoopbackOnly(t *testing.T) {
	cfg := Default(t.TempDir())
	if !allLoopback(cfg.Server.HTTPAddress, cfg.Server.OTLPHTTPAddress, cfg.Server.OTLPGRPCAddress) {
		t.Fatalf("default listeners are not loopback-only: %#v", cfg.Server)
	}
	if !cfg.Auth.LocalBypass {
		t.Fatal("default local service unexpectedly requires a shared-service token")
	}
}

func TestAFRSEC003RemoteStartupFailsClosedWithoutEveryExplicitControl(t *testing.T) {
	remote := func() Config {
		cfg := Default(t.TempDir())
		cfg.Server.HTTPAddress = "0.0.0.0:7777"
		cfg.Server.OTLPHTTPAddress = "0.0.0.0:4318"
		cfg.Server.OTLPGRPCAddress = "0.0.0.0:4317"
		return cfg
	}
	tests := []struct {
		name    string
		prepare func(*Config)
		want    string
	}{
		{name: "authentication bypass", prepare: func(*Config) {}, want: "authentication bypass requires loopback binding"},
		{name: "missing transport", prepare: func(cfg *Config) { cfg.Auth.LocalBypass, cfg.Auth.BootstrapToken = false, testBootstrapToken }, want: "remote listeners require transport security"},
		{name: "untrusted proxy", prepare: func(cfg *Config) {
			cfg.Auth.LocalBypass, cfg.Auth.BootstrapToken, cfg.Auth.BehindTLSProxy = false, testBootstrapToken, true
		}, want: "TLS proxy mode requires trusted proxy networks"},
		{name: "invalid proxy network", prepare: func(cfg *Config) {
			cfg.Auth.LocalBypass, cfg.Auth.BootstrapToken, cfg.Auth.BehindTLSProxy = false, testBootstrapToken, true
			cfg.Auth.TrustedProxyCIDRs = []string{"proxy.example.test"}
		}, want: "trusted proxy network is invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := remote()
			test.prepare(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestAFRSEC003RemoteStartupAcceptsNativeProxyOrDevelopmentTransport(t *testing.T) {
	base := func() Config {
		cfg := Default(t.TempDir())
		cfg.Server.HTTPAddress = "0.0.0.0:7777"
		cfg.Server.OTLPHTTPAddress = "0.0.0.0:4318"
		cfg.Server.OTLPGRPCAddress = "0.0.0.0:4317"
		cfg.Auth.LocalBypass = false
		cfg.Auth.BootstrapToken = testBootstrapToken
		return cfg
	}
	for _, prepare := range []func(*Config){
		func(cfg *Config) { cfg.Server.TLSCert, cfg.Server.TLSKey = "server.crt", "server.key" },
		func(cfg *Config) { cfg.Auth.BehindTLSProxy, cfg.Auth.TrustedProxyCIDRs = true, []string{"10.0.0.4/32"} },
		func(cfg *Config) { cfg.Auth.InsecureDevelopment = true },
	} {
		cfg := base()
		prepare(&cfg)
		if err := cfg.Validate(); err != nil {
			t.Fatalf("explicit remote configuration was rejected: %v", err)
		}
	}
	development := Default(t.TempDir())
	development.Server.HTTPAddress = "0.0.0.0:7777"
	development.Server.OTLPHTTPAddress = "0.0.0.0:4318"
	development.Server.OTLPGRPCAddress = "0.0.0.0:4317"
	development.Auth.InsecureDevelopment = true
	if err := development.Validate(); err != nil {
		t.Fatalf("explicit unauthenticated development override was rejected: %v", err)
	}
}

func TestAFRSEC003TransportEnvironmentIsParsedAtTheConfigBoundary(t *testing.T) {
	t.Setenv("FLIGHT_TLS_CERT", "/run/secrets/flight.crt")
	t.Setenv("FLIGHT_TLS_KEY", "/run/secrets/flight.key")
	t.Setenv("FLIGHT_TRUSTED_PROXY_CIDRS", "10.0.0.4/32, 2001:db8::/64")
	t.Setenv("FLIGHT_INSECURE_DEVELOPMENT", "true")
	cfg := Default(t.TempDir())
	applyEnvironment(&cfg)
	if cfg.Server.TLSCert != "/run/secrets/flight.crt" || cfg.Server.TLSKey != "/run/secrets/flight.key" || !cfg.Auth.InsecureDevelopment {
		t.Fatalf("transport environment was not parsed: %#v %#v", cfg.Server, cfg.Auth)
	}
	if len(cfg.Auth.TrustedProxyCIDRs) != 2 || !cfg.Auth.TrustsProxy("10.0.0.4:443") || cfg.Auth.TrustsProxy("10.0.0.5:443") {
		t.Fatalf("trusted proxy boundary = %#v", cfg.Auth.TrustedProxyCIDRs)
	}
}
