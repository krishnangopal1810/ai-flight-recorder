package access

import (
	"context"
	"crypto/tls"
	"net"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/peer"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
)

func TestBearerTokenRequiresOneExplicitScheme(t *testing.T) {
	if value, err := BearerToken([]string{"bEaReR token-value"}); err != nil || value != "token-value" {
		t.Fatalf("valid bearer token = %q, %v", value, err)
	}
	for _, values := range [][]string{nil, {"Basic token-value"}, {"Bearer"}, {"Bearer one", "Bearer two"}} {
		if _, err := BearerToken(values); err == nil {
			t.Fatalf("authorization values %#v were accepted", values)
		}
	}
}

func TestAFRSEC003GRPCProxyTrustUsesTheConnectionPeer(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Auth.BehindTLSProxy = true
	cfg.Auth.TrustedProxyCIDRs = []string{"10.0.0.4/32"}
	untrusted := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.5"), Port: 4317}})
	if err := requireGRPCTransport(untrusted, cfg); err == nil {
		t.Fatal("untrusted direct gRPC peer was accepted in TLS proxy mode")
	}
	trusted := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("10.0.0.4"), Port: 4317}})
	if err := requireGRPCTransport(trusted, cfg); err != nil {
		t.Fatalf("trusted gRPC proxy peer was rejected: %v", err)
	}
}

func TestAFRSEC003ProxyHeadersAreTrustedOnlyFromConfiguredNetworks(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Auth.BehindTLSProxy = true
	cfg.Auth.TrustedProxyCIDRs = []string{"127.0.0.2/32"}
	request := httptest.NewRequest("GET", "http://flight.test/api/v1/capsules", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.RemoteAddr = "127.0.0.1:41000"
	if err := RequireHTTPTransport(cfg, request); err == nil {
		t.Fatal("an untrusted client spoofed the TLS proxy header")
	}
	request.RemoteAddr = "127.0.0.2:41000"
	if err := RequireHTTPTransport(cfg, request); err != nil {
		t.Fatalf("trusted TLS proxy request was rejected: %v", err)
	}
	request.Header.Add("X-Forwarded-Proto", "http")
	if err := RequireHTTPTransport(cfg, request); err == nil {
		t.Fatal("an ambiguous forwarded-protocol chain was accepted")
	}
}

func TestAFRSEC003NativeHTTPRequiresATLSConnection(t *testing.T) {
	cfg := config.Default(t.TempDir())
	cfg.Server.TLSCert, cfg.Server.TLSKey = "server.crt", "server.key"
	request := httptest.NewRequest("GET", "http://flight.test/healthz", nil)
	if err := RequireHTTPTransport(cfg, request); err == nil {
		t.Fatal("cleartext request reached a native-TLS handler")
	}
	request.TLS = &tls.ConnectionState{Version: tls.VersionTLS13}
	if err := RequireHTTPTransport(cfg, request); err != nil {
		t.Fatalf("native TLS request was rejected: %v", err)
	}
}
