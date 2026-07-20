package main

import (
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	environment := config.LoadGatewayEnvironment()
	allowed := parseAllowed(environment.AllowedHosts)
	output := environment.OutputPath
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second}, Timeout: 70 * time.Second}
	client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		if !allowed[hostname(request.URL.Host)] {
			return http.ErrUseLastResponse
		}
		return nil
	}
	proxy := &gateway{allowed: allowed, output: output, client: client, logger: logger}
	server := &http.Server{Addr: ":8080", Handler: proxy, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 70 * time.Second, WriteTimeout: 70 * time.Second, IdleTimeout: 2 * time.Minute}
	logger.Info("replay gateway started", "allowed_host_count", len(allowed))
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("gateway stopped", "error_type", fmt.Sprintf("%T", err))
		os.Exit(1)
	}
}

type gateway struct {
	allowed map[string]bool
	output  string
	client  *http.Client
	logger  *slog.Logger
}

func (g *gateway) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.URL.Path == "/healthz":
		writer.WriteHeader(http.StatusNoContent)
	case request.URL.Path == "/v1/traces" && request.Method == http.MethodPost:
		g.captureOTLP(writer, request)
	case request.Method == http.MethodConnect:
		g.connect(writer, request)
	default:
		g.forward(writer, request)
	}
}

func (g *gateway) captureOTLP(writer http.ResponseWriter, request *http.Request) {
	var body io.Reader = http.MaxBytesReader(writer, request.Body, 64<<20)
	if strings.EqualFold(request.Header.Get("Content-Encoding"), "gzip") {
		compressed, err := gzip.NewReader(body)
		if err != nil {
			http.Error(writer, "invalid OTLP gzip batch", http.StatusBadRequest)
			return
		}
		defer compressed.Close()
		body = io.LimitReader(compressed, 64<<20)
	}
	data, err := io.ReadAll(body)
	if err != nil {
		http.Error(writer, "invalid OTLP batch", http.StatusBadRequest)
		return
	}
	var export collectortrace.ExportTraceServiceRequest
	jsonEncoding := strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json")
	if jsonEncoding {
		err = protojson.Unmarshal(data, &export)
	} else {
		err = proto.Unmarshal(data, &export)
	}
	if err != nil {
		http.Error(writer, "malformed OTLP batch", http.StatusBadRequest)
		return
	}
	data, err = proto.Marshal(&export)
	if err != nil {
		http.Error(writer, "capture unavailable", http.StatusServiceUnavailable)
		return
	}
	nameBytes := make([]byte, 8)
	_, _ = rand.Read(nameBytes)
	filename := filepath.Join(g.output, hex.EncodeToString(nameBytes)+".otlp.pb")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		http.Error(writer, "capture unavailable", http.StatusServiceUnavailable)
		return
	}
	if jsonEncoding {
		writer.Header().Set("Content-Type", "application/json")
	} else {
		writer.Header().Set("Content-Type", "application/x-protobuf")
	}
	writer.WriteHeader(http.StatusOK)
	if jsonEncoding {
		_, _ = writer.Write([]byte("{}"))
	}
}

func (g *gateway) connect(writer http.ResponseWriter, request *http.Request) {
	host := hostname(request.Host)
	if !g.allowed[host] {
		http.Error(writer, "egress destination is not declared", http.StatusForbidden)
		return
	}
	destination, err := net.DialTimeout("tcp", request.Host, 10*time.Second)
	if err != nil {
		http.Error(writer, "declared destination unavailable", http.StatusBadGateway)
		return
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		_ = destination.Close()
		http.Error(writer, "CONNECT unsupported", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		_ = destination.Close()
		return
	}
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	go tunnel(destination, client)
	go tunnel(client, destination)
}

func (g *gateway) forward(writer http.ResponseWriter, request *http.Request) {
	target := request.URL
	if !target.IsAbs() {
		parsed, _ := url.Parse("http://" + request.Host + request.URL.RequestURI())
		target = parsed
	}
	if !g.allowed[hostname(target.Host)] {
		http.Error(writer, "egress destination is not declared", http.StatusForbidden)
		return
	}
	forward, err := http.NewRequestWithContext(request.Context(), request.Method, target.String(), request.Body)
	if err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	forward.Header = request.Header.Clone()
	response, err := g.client.Do(forward)
	if err != nil {
		http.Error(writer, "declared destination unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
}

func parseAllowed(value string) map[string]bool {
	result := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(strings.ToLower(item)); item != "" {
			result[item] = true
		}
	}
	return result
}
func hostname(value string) string {
	host, _, err := net.SplitHostPort(value)
	if err == nil {
		return strings.ToLower(host)
	}
	return strings.ToLower(strings.Trim(value, "[]"))
}
func tunnel(destination net.Conn, source net.Conn) {
	defer destination.Close()
	defer source.Close()
	_, _ = io.Copy(destination, source)
}
