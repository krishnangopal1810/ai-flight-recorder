package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
	"gopkg.in/yaml.v3"
)

type StorageConfig struct {
	Driver      string `yaml:"driver"`
	SQLitePath  string `yaml:"sqlite_path"`
	BlobPath    string `yaml:"blob_path"`
	PostgresURL string `yaml:"postgres_url"`
	S3Endpoint  string `yaml:"s3_endpoint"`
	S3Bucket    string `yaml:"s3_bucket"`
	S3Region    string `yaml:"s3_region"`
	S3AccessKey string `yaml:"s3_access_key"`
	S3SecretKey string `yaml:"s3_secret_key"`
	S3UseTLS    bool   `yaml:"s3_use_tls"`
}

type AuthConfig struct {
	LocalBypass    bool   `yaml:"local_bypass"`
	BehindTLSProxy bool   `yaml:"behind_tls_proxy"`
	BootstrapToken string `yaml:"bootstrap_token"`
}

type ServerConfig struct {
	HTTPAddress     string        `yaml:"http_address"`
	OTLPHTTPAddress string        `yaml:"otlp_http_address"`
	OTLPGRPCAddress string        `yaml:"otlp_grpc_address"`
	WebDir          string        `yaml:"web_dir"`
	TLSCert         string        `yaml:"tls_cert"`
	TLSKey          string        `yaml:"tls_key"`
	LateSpanGrace   time.Duration `yaml:"late_span_grace"`
}

type PolicyRule struct {
	Name        string `yaml:"name"`
	Path        string `yaml:"path"`
	Pattern     string `yaml:"pattern,omitempty"`
	Action      string `yaml:"action"`
	Replacement string `yaml:"replacement,omitempty"`
	Limit       int    `yaml:"limit,omitempty"`
}

type CapturePolicy struct {
	Version          string                   `yaml:"version"`
	Mode             string                   `yaml:"mode"`
	MaxFieldSize     int                      `yaml:"max_field_size"`
	DefaultRetention time.Duration            `yaml:"default_retention"`
	ProjectRetention map[string]time.Duration `yaml:"project_retention,omitempty"`
	Rules            []PolicyRule             `yaml:"rules"`
}

type Config struct {
	Server  ServerConfig  `yaml:"server"`
	Storage StorageConfig `yaml:"storage"`
	Auth    AuthConfig    `yaml:"auth"`
	Policy  CapturePolicy `yaml:"policy"`
	Runtime RuntimeConfig `yaml:"runtime"`
}

type RuntimeConfig struct {
	OCIBinary         string            `yaml:"oci_binary"`
	GatewayImage      string            `yaml:"gateway_image"`
	OCIEgressNetwork  string            `yaml:"oci_egress_network"`
	ReplayCredentials map[string]string `yaml:"-"`
}

var ociNetworkName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)

type FlightFile struct {
	Version    string           `yaml:"version"`
	Entrypoint types.Entrypoint `yaml:"entrypoint"`
}

func Default(dataDir string) Config {
	return Config{
		Server: ServerConfig{
			HTTPAddress: "127.0.0.1:7777", OTLPHTTPAddress: "127.0.0.1:4318",
			OTLPGRPCAddress: "127.0.0.1:4317", WebDir: "web/dist", LateSpanGrace: 30 * time.Second,
		},
		Storage: StorageConfig{
			Driver: "sqlite", SQLitePath: filepath.Join(dataDir, "flight.db"), BlobPath: filepath.Join(dataDir, "blobs"),
		},
		Auth:    AuthConfig{LocalBypass: true},
		Runtime: RuntimeConfig{OCIBinary: "docker", GatewayImage: "ai-flight-recorder:local"},
		Policy: CapturePolicy{
			Version: "1", Mode: "strict", MaxFieldSize: 1 << 20, DefaultRetention: 30 * 24 * time.Hour,
			Rules: []PolicyRule{
				{Name: "authorization", Path: "*", Pattern: `(?i)(authorization|api[_-]?key|password|token)`, Action: "redact", Replacement: "[REDACTED]"},
			},
		},
	}
}

func Load(path string) (Config, error) {
	dataDir, err := defaultDataDir()
	if err != nil {
		return Config{}, err
	}
	cfg := Default(dataDir)
	if path != "" {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return Config{}, types.NewError(types.ErrValidation, "cannot read configuration", "check --config and file permissions", readErr)
		}
		if unmarshalErr := yaml.Unmarshal(data, &cfg); unmarshalErr != nil {
			return Config{}, types.NewError(types.ErrValidation, "invalid configuration", "fix the YAML fields reported by the parser", unmarshalErr)
		}
	}
	applyEnvironment(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadFlightFile(path string) (FlightFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FlightFile{}, types.NewError(types.ErrValidation, "cannot read flight.yaml", "run flight init or provide --flight-file", err)
	}
	var file FlightFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return FlightFile{}, types.NewError(types.ErrValidation, "invalid flight.yaml", "fix the YAML fields reported by the parser", err)
	}
	if file.Version != "1" {
		return FlightFile{}, types.NewError(types.ErrValidation, "unsupported flight.yaml version", "set version: 1", nil)
	}
	if len(file.Entrypoint.Command) == 0 {
		return FlightFile{}, types.NewError(types.ErrValidation, "entrypoint command is required", "set entrypoint.command to an argv list", nil)
	}
	if file.Entrypoint.InputMode != "stdin_json" || file.Entrypoint.OutputMode != "stdout_json" {
		return FlightFile{}, types.NewError(types.ErrValidation, "unsupported entrypoint I/O mode", "V1 requires stdin_json and stdout_json", nil)
	}
	return file, nil
}

func (c Config) Validate() error {
	for name, address := range map[string]string{"http": c.Server.HTTPAddress, "otlp_http": c.Server.OTLPHTTPAddress, "otlp_grpc": c.Server.OTLPGRPCAddress} {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return types.NewError(types.ErrValidation, fmt.Sprintf("invalid %s address", name), "use host:port", err)
		}
		if c.Auth.LocalBypass && !isLoopback(host) {
			return types.NewError(types.ErrValidation, "authentication bypass requires loopback binding", "bind every listener to 127.0.0.1/::1 or disable local_bypass", nil)
		}
	}
	if c.Storage.Driver != "sqlite" && c.Storage.Driver != "postgres" {
		return types.NewError(types.ErrValidation, "unsupported storage driver", "use sqlite or postgres", nil)
	}
	if c.Runtime.OCIBinary != "docker" && c.Runtime.OCIBinary != "podman" {
		return types.NewError(types.ErrValidation, "unsupported OCI runtime", "use docker or podman", nil)
	}
	if c.Runtime.OCIEgressNetwork != "" && !ociNetworkName.MatchString(c.Runtime.OCIEgressNetwork) {
		return types.NewError(types.ErrValidation, "invalid OCI egress network", "use an existing Docker or Podman network name containing only letters, digits, dot, underscore, or hyphen", nil)
	}
	if c.Storage.Driver == "postgres" && (c.Storage.PostgresURL == "" || c.Storage.S3Endpoint == "" || c.Storage.S3Bucket == "") {
		return types.NewError(types.ErrValidation, "hosted storage configuration is incomplete", "set PostgreSQL URL, S3 endpoint, and S3 bucket", nil)
	}
	if c.Policy.Mode != "strict" && c.Policy.Mode != "permissive" {
		return types.NewError(types.ErrValidation, "invalid policy mode", "use strict or permissive", nil)
	}
	if c.Policy.MaxFieldSize <= 0 || c.Policy.DefaultRetention < 0 {
		return types.NewError(types.ErrValidation, "capture limits are invalid", "set a positive max_field_size and a non-negative retention", nil)
	}
	if c.Server.LateSpanGrace < 0 {
		return types.NewError(types.ErrValidation, "late-span grace is invalid", "set a non-negative duration such as 500ms or 30s", nil)
	}
	for project, retention := range c.Policy.ProjectRetention {
		if project == "" || retention < 0 {
			return types.NewError(types.ErrValidation, "project retention is invalid", "use non-empty project IDs and non-negative durations", nil)
		}
	}
	if (c.Server.TLSCert == "") != (c.Server.TLSKey == "") {
		return types.NewError(types.ErrValidation, "TLS certificate and key must be configured together", "set both tls_cert and tls_key", nil)
	}
	if !allLoopback(c.Server.HTTPAddress, c.Server.OTLPHTTPAddress, c.Server.OTLPGRPCAddress) && c.Server.TLSCert == "" && !c.Auth.BehindTLSProxy {
		return types.NewError(types.ErrValidation, "remote listeners require transport security", "configure tls_cert/tls_key or set behind_tls_proxy when a trusted reverse proxy terminates TLS", nil)
	}
	return nil
}

func applyEnvironment(c *Config) {
	if value := os.Getenv("FLIGHT_HTTP_ADDRESS"); value != "" {
		c.Server.HTTPAddress = value
	}
	if value := os.Getenv("FLIGHT_OTLP_HTTP_ADDRESS"); value != "" {
		c.Server.OTLPHTTPAddress = value
	}
	if value := os.Getenv("FLIGHT_OTLP_GRPC_ADDRESS"); value != "" {
		c.Server.OTLPGRPCAddress = value
	}
	if value := os.Getenv("FLIGHT_WEB_DIR"); value != "" {
		c.Server.WebDir = value
	}
	if value := os.Getenv("FLIGHT_LATE_SPAN_GRACE"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			c.Server.LateSpanGrace = -1
		} else {
			c.Server.LateSpanGrace = parsed
		}
	}
	if value := os.Getenv("FLIGHT_DATABASE_URL"); value != "" {
		c.Storage.Driver, c.Storage.PostgresURL = "postgres", value
	}
	if value := os.Getenv("FLIGHT_S3_ENDPOINT"); value != "" {
		c.Storage.S3Endpoint = value
	}
	if value := os.Getenv("FLIGHT_S3_BUCKET"); value != "" {
		c.Storage.S3Bucket = value
	}
	if value := os.Getenv("FLIGHT_S3_REGION"); value != "" {
		c.Storage.S3Region = value
	}
	if value := os.Getenv("FLIGHT_S3_ACCESS_KEY"); value != "" {
		c.Storage.S3AccessKey = value
	}
	if value := os.Getenv("FLIGHT_S3_SECRET_KEY"); value != "" {
		c.Storage.S3SecretKey = value
	}
	if value := os.Getenv("FLIGHT_S3_USE_TLS"); value != "" {
		c.Storage.S3UseTLS, _ = strconv.ParseBool(value)
	}
	if value := os.Getenv("FLIGHT_LOCAL_BYPASS"); value != "" {
		c.Auth.LocalBypass, _ = strconv.ParseBool(value)
	}
	if value := os.Getenv("FLIGHT_BEHIND_TLS_PROXY"); value != "" {
		c.Auth.BehindTLSProxy, _ = strconv.ParseBool(value)
	}
	if value := os.Getenv("FLIGHT_BOOTSTRAP_TOKEN"); value != "" {
		c.Auth.BootstrapToken = value
	}
	if value := os.Getenv("FLIGHT_OCI_RUNTIME"); value != "" {
		c.Runtime.OCIBinary = value
	}
	if value := os.Getenv("FLIGHT_GATEWAY_IMAGE"); value != "" {
		c.Runtime.GatewayImage = value
	}
	if value := os.Getenv("FLIGHT_OCI_EGRESS_NETWORK"); value != "" {
		c.Runtime.OCIEgressNetwork = value
	}
	if c.Runtime.ReplayCredentials == nil {
		c.Runtime.ReplayCredentials = map[string]string{}
	}
	for _, item := range os.Environ() {
		name, value, found := strings.Cut(item, "=")
		if !found || !strings.HasPrefix(name, "FLIGHT_REPLAY_CREDENTIAL_") {
			continue
		}
		credentialName := strings.TrimPrefix(name, "FLIGHT_REPLAY_CREDENTIAL_")
		if credentialName != "" {
			c.Runtime.ReplayCredentials[credentialName] = value
		}
	}
}

func defaultDataDir() (string, error) {
	if value := os.Getenv("FLIGHT_DATA_DIR"); value != "" {
		return value, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", types.NewError(types.ErrValidation, "cannot determine data directory", "set FLIGHT_DATA_DIR", err)
	}
	return filepath.Join(dir, "ai-flight-recorder"), nil
}

func isLoopback(host string) bool {
	host = strings.Trim(host, "[]")
	return host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()
}

func allLoopback(addresses ...string) bool {
	for _, address := range addresses {
		host, _, err := net.SplitHostPort(address)
		if err != nil || !isLoopback(host) {
			return false
		}
	}
	return true
}
