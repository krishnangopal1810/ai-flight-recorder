//go:build security

package security_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/app"
)

const accessBootstrapToken = "frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

type accessService struct {
	application *app.Application
	cancel      context.CancelFunc
	done        chan error
	httpAddress string
	grpcAddress string
}

type testIdentity struct {
	certificate tls.Certificate
	certFile    string
	keyFile     string
	roots       *x509.CertPool
}

func TestAFRSEC003NativeTLSAndScopedAuthentication(t *testing.T) {
	identity := newTestIdentity(t)
	cfg, httpAddress, grpcAddress := sharedAccessConfig(t, config.StorageConfig{})
	cfg.Server.TLSCert, cfg.Server.TLSKey = identity.certFile, identity.keyFile
	service := startAccessService(t, cfg, httpAddress, grpcAddress)

	httpClient := trustedHTTPClient(identity.roots)
	waitForHTTP(t, service, httpClient, "https://"+httpAddress+"/healthz")
	assertHTTPStatus(t, httpClient, "https://"+httpAddress+"/api/v1/capsules?limit=1", "", http.StatusUnauthorized)
	assertHTTPStatus(t, httpClient, "https://"+httpAddress+"/metrics", "", http.StatusUnauthorized)
	assertHTTPStatus(t, httpClient, "https://"+httpAddress+"/api/v1/capsules?limit=1", accessBootstrapToken, http.StatusOK)
	assertHTTPStatus(t, httpClient, "https://"+httpAddress+"/metrics", accessBootstrapToken, http.StatusOK)
	assertOTLPHTTPStatus(t, httpClient, "https://"+httpAddress+"/v1/traces", "", http.StatusUnauthorized)
	assertCleartextHTTPBlocked(t, httpAddress)

	readToken, err := service.application.Auth.Create(context.Background(), "native-read", []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	ingestToken, err := service.application.Auth.Create(context.Background(), "native-ingest", []string{"ingest"})
	if err != nil {
		t.Fatal(err)
	}
	assertHTTPStatus(t, httpClient, "https://"+httpAddress+"/api/v1/capsules?limit=1", ingestToken.Value, http.StatusUnauthorized)
	assertOTLPHTTPStatus(t, httpClient, "https://"+httpAddress+"/v1/traces", readToken.Value, http.StatusUnauthorized)
	assertOTLPHTTPStatus(t, httpClient, "https://"+httpAddress+"/v1/traces", ingestToken.Value, http.StatusOK)

	grpcTLS := credentials.NewTLS(&tls.Config{RootCAs: identity.roots, MinVersion: tls.VersionTLS12})
	assertGRPCExport(t, grpcAddress, grpcTLS, "", codes.Unauthenticated)
	assertGRPCExport(t, grpcAddress, grpcTLS, readToken.Value, codes.Unauthenticated)
	assertGRPCExport(t, grpcAddress, grpcTLS, ingestToken.Value, codes.OK)
	assertGRPCExport(t, grpcAddress, insecure.NewCredentials(), ingestToken.Value, codes.Unavailable)
	t.Logf(`{"mode":"native_tls","http_unauthenticated":401,"grpc_unauthenticated":"Unauthenticated","minimum_tls":"1.2","scopes":"enforced"}`)
}

func TestAFRSEC003TLSProxyRejectsUnauthenticatedAndDirectRequests(t *testing.T) {
	storages := []struct {
		name    string
		storage config.StorageConfig
	}{{name: "local-sqlite-filesystem"}}
	hosted := config.HostedContractEnvironment("flight-access-" + uuid.NewString())
	if hosted.PostgresURL != "" && hosted.S3Endpoint != "" {
		hosted.Driver = "postgres"
		storages = append(storages, struct {
			name    string
			storage config.StorageConfig
		}{name: "hosted-postgresql-s3", storage: hosted})
	}
	for _, target := range storages {
		t.Run(target.name, func(t *testing.T) {
			identity := newTestIdentity(t)
			cfg, httpAddress, grpcAddress := sharedAccessConfig(t, target.storage)
			cfg.Auth.BehindTLSProxy = true
			cfg.Auth.TrustedProxyCIDRs = []string{"127.0.0.1/32"}
			service := startAccessService(t, cfg, httpAddress, grpcAddress)
			waitForTCP(t, service, httpAddress)

			front := startHTTPProxy(t, identity, httpAddress)
			proxyGRPCAddress := startGRPCTLSProxy(t, identity, grpcAddress)
			httpClient := trustedHTTPClient(identity.roots)
			waitForHTTP(t, service, httpClient, front.URL+"/healthz")

			assertHTTPStatus(t, httpClient, front.URL+"/api/v1/capsules?limit=1", "", http.StatusUnauthorized)
			assertHTTPStatus(t, httpClient, front.URL+"/api/v1/capsules?limit=1", accessBootstrapToken, http.StatusOK)
			assertOTLPHTTPStatus(t, httpClient, front.URL+"/v1/traces", "", http.StatusUnauthorized)
			assertOTLPHTTPStatus(t, httpClient, front.URL+"/v1/traces", accessBootstrapToken, http.StatusOK)
			assertDirectProxyBypassBlocked(t, httpAddress)

			grpcTLS := credentials.NewTLS(&tls.Config{RootCAs: identity.roots, MinVersion: tls.VersionTLS12})
			assertGRPCExport(t, proxyGRPCAddress, grpcTLS, "", codes.Unauthenticated)
			assertGRPCExport(t, proxyGRPCAddress, grpcTLS, accessBootstrapToken, codes.OK)
			t.Logf(`{"mode":"trusted_tls_proxy","storage":%q,"front_unauthenticated":401,"direct_http":401,"grpc_unauthenticated":"Unauthenticated"}`, target.name)
		})
	}
}

func sharedAccessConfig(t *testing.T, storage config.StorageConfig) (config.Config, string, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default(root)
	httpPort, grpcPort := reservePort(t), reservePort(t)
	httpAddress, grpcAddress := fmt.Sprintf("127.0.0.1:%d", httpPort), fmt.Sprintf("127.0.0.1:%d", grpcPort)
	cfg.Server.HTTPAddress = fmt.Sprintf("0.0.0.0:%d", httpPort)
	cfg.Server.OTLPHTTPAddress = cfg.Server.HTTPAddress
	cfg.Server.OTLPGRPCAddress = fmt.Sprintf("0.0.0.0:%d", grpcPort)
	cfg.Server.WebDir = filepath.Join(root, "no-inspector")
	cfg.Server.LateSpanGrace = 0
	cfg.Auth.LocalBypass = false
	cfg.Auth.BootstrapToken = accessBootstrapToken
	if storage.Driver == "postgres" {
		cfg.Storage = storage
	}
	return cfg, httpAddress, grpcAddress
}

func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func startAccessService(t *testing.T, cfg config.Config, httpAddress, grpcAddress string) *accessService {
	t.Helper()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("shared access config: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	application, err := app.New(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	service := &accessService{application: application, cancel: cancel, done: make(chan error, 1), httpAddress: httpAddress, grpcAddress: grpcAddress}
	go func() { service.done <- application.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-service.done:
			if err != nil {
				t.Errorf("shared service shutdown: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("shared service did not stop")
		}
		if err := application.Close(); err != nil {
			t.Errorf("close shared service: %v", err)
		}
	})
	return service
}

func newTestIdentity(t *testing.T) testIdentity {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AFR security contract"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateDER})
	root := t.TempDir()
	certFile, keyFile := filepath.Join(root, "server.crt"), filepath.Join(root, "server.key")
	if err := os.WriteFile(certFile, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, privatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(certificatePEM, privatePEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificatePEM) {
		t.Fatal("test TLS root was not accepted")
	}
	return testIdentity{certificate: certificate, certFile: certFile, keyFile: keyFile, roots: roots}
}

func startHTTPProxy(t *testing.T, identity testIdentity, backendAddress string) *httptest.Server {
	t.Helper()
	target, err := url.Parse("http://" + backendAddress)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director
	proxy.Director = func(request *http.Request) {
		originalDirector(request)
		request.Header.Del("Forwarded")
		request.Header.Set("X-Forwarded-Proto", "https")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{Proxy: nil, DialContext: dialer.DialContext}
	proxy.Transport = transport
	server := httptest.NewUnstartedServer(proxy)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{identity.certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	t.Cleanup(transport.CloseIdleConnections)
	return server
}

func startGRPCTLSProxy(t *testing.T, identity testIdentity, backendAddress string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsListener := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{identity.certificate}, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2"}})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = tlsListener.Close()
	})
	go func() {
		for {
			client, err := tlsListener.Accept()
			if err != nil {
				return
			}
			go proxyConnection(ctx, client, backendAddress)
		}
	}()
	return listener.Addr().String()
}

func proxyConnection(ctx context.Context, client net.Conn, backendAddress string) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	backend, err := dialer.DialContext(ctx, "tcp", backendAddress)
	if err != nil {
		_ = client.Close()
		return
	}
	defer client.Close()
	defer backend.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(backend, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, backend); done <- struct{}{} }()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func trustedHTTPClient(roots *x509.CertPool) *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: 5 * time.Second}
}

func waitForHTTP(t *testing.T, service *accessService, client *http.Client, endpoint string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequest(http.MethodGet, endpoint, nil)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case err := <-service.done:
			t.Fatalf("shared service exited during startup: %v", err)
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("shared service was not ready at %s", endpoint)
}

func waitForTCP(t *testing.T, service *accessService, address string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		select {
		case err := <-service.done:
			t.Fatalf("shared service exited during startup: %v", err)
		default:
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("shared service was not reachable at %s", address)
}

func assertHTTPStatus(t *testing.T, client *http.Client, endpoint, token string, want int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("GET %s = %d %s, want %d", endpoint, response.StatusCode, body, want)
	}
	if want == http.StatusUnauthorized {
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code != "authentication" {
			t.Fatalf("authentication error = %s, %v", body, err)
		}
	}
}

func assertOTLPHTTPStatus(t *testing.T, client *http.Client, endpoint, token string, want int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-protobuf")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("POST %s = %d %s, want %d", endpoint, response.StatusCode, body, want)
	}
	if want == http.StatusUnauthorized {
		var envelope struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil || envelope.Error.Code != "authentication" {
			t.Fatalf("OTLP authentication error = %s, %v", body, err)
		}
	}
}

func assertCleartextHTTPBlocked(t *testing.T, address string) {
	t.Helper()
	response, err := (&http.Client{Timeout: time.Second}).Get("http://" + address + "/healthz")
	if err == nil {
		defer response.Body.Close()
		if response.StatusCode == http.StatusOK {
			t.Fatal("native TLS listener served a cleartext health request")
		}
	}
}

func assertDirectProxyBypassBlocked(t *testing.T, address string) {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, "http://"+address+"/api/v1/capsules?limit=1", nil)
	request.Header.Set("Authorization", "Bearer "+accessBootstrapToken)
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("direct trusted-header spoof = %d %s, want 401", response.StatusCode, strings.TrimSpace(string(body)))
	}
}

func assertGRPCExport(t *testing.T, address string, transport credentials.TransportCredentials, token string, want codes.Code) {
	t.Helper()
	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(transport))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	}
	_, err = collectortrace.NewTraceServiceClient(connection).Export(ctx, &collectortrace.ExportTraceServiceRequest{})
	got := status.Code(err)
	if got != want {
		t.Fatalf("gRPC export code = %s (%v), want %s", got, err, want)
	}
}
