package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
)

func TestAFRSEC003RecordPropagatesOneScopedOTLPToken(t *testing.T) {
	environment := recordEnvironment([]string{
		"PATH=/bin", "FLIGHT_CAPTURE=false", "OTEL_EXPORTER_OTLP_ENDPOINT=http://old", "FLIGHT_OTLP_TOKEN=old", "OTEL_EXPORTER_OTLP_HEADERS=x-tenant=alpha,Authorization=old", "OTEL_RESOURCE_ATTRIBUTES=service.name=agent,service.namespace=agent\\,worker,flight.capture=false,flight.project.id=old",
	}, "https://flight.example.test", "frt_token", "support", `{"schema_version":"1"}`)
	want := map[string]string{
		"PATH": "PATH=/bin", "FLIGHT_CAPTURE": "FLIGHT_CAPTURE=true", "OTEL_EXPORTER_OTLP_ENDPOINT": "OTEL_EXPORTER_OTLP_ENDPOINT=https://flight.example.test",
		"FLIGHT_OTLP_TOKEN": "FLIGHT_OTLP_TOKEN=frt_token", "OTEL_EXPORTER_OTLP_HEADERS": "OTEL_EXPORTER_OTLP_HEADERS=x-tenant=alpha,authorization=Bearer%20frt_token",
		"FLIGHT_PROJECT_ID": "FLIGHT_PROJECT_ID=support", "FLIGHT_ENTRYPOINT_JSON": `FLIGHT_ENTRYPOINT_JSON={"schema_version":"1"}`,
		"OTEL_RESOURCE_ATTRIBUTES": "OTEL_RESOURCE_ATTRIBUTES=service.name=agent,service.namespace=agent\\,worker,flight.capture=true,flight.project.id=support",
	}
	counts := map[string]int{}
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		counts[name]++
		if expected, ok := want[name]; ok && entry != expected {
			t.Fatalf("%s = %q, want %q", name, entry, expected)
		}
	}
	for name := range want {
		if counts[name] != 1 {
			t.Fatalf("%s occurs %d times in %#v", name, counts[name], environment)
		}
	}
}

func TestInitWritesExplicitReplayDefinitionAndRefusesOverwrite(t *testing.T) {
	t.Chdir(t.TempDir())
	command := newInit()
	command.SetArgs([]string{"--project", "support-agent", "--model-host", "API.OpenAI.COM.", "--credential", "OPENAI_API_KEY", "--accept-model-override", "--", "python", "/app/agent.py"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	file, err := config.LoadFlightFile("flight.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if file.ProjectID != "support-agent" || !file.Entrypoint.ModelOverride || file.Entrypoint.ModelHosts[0] != "api.openai.com" || file.Entrypoint.Environment[0] != "OPENAI_API_KEY" {
		t.Fatalf("generated flight file = %#v", file)
	}
	if err := command.Execute(); err == nil {
		t.Fatal("flight init overwrote an existing definition")
	}
	info, err := os.Stat(filepath.Join("flight.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("flight.yaml mode = %v", info.Mode().Perm())
	}
}

func TestRecordEnvironmentPreservesExistingAuthenticationWithoutAFlightToken(t *testing.T) {
	base := []string{"FLIGHT_OTLP_TOKEN=sdk-token", "OTEL_EXPORTER_OTLP_HEADERS=x-api-key=existing"}
	environment := recordEnvironment(base, "http://127.0.0.1:4318", "", "support", "")
	for _, expected := range base {
		found := false
		for _, entry := range environment {
			found = found || entry == expected
		}
		if !found {
			t.Fatalf("record environment removed %q: %#v", expected, environment)
		}
	}
}

func TestReplayReadyRecordValidatesBeforeStartingTheChild(t *testing.T) {
	directory := t.TempDir()
	flightFile := filepath.Join(directory, "flight.yaml")
	marker := filepath.Join(directory, "child-started")
	content := "version: \"1\"\nproject_id: support\nentrypoint:\n  command: [/agent]\n  image: agent:latest\n  input_mode: stdin_json\n  output_mode: stdout_json\n"
	if err := os.WriteFile(flightFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newRecord(&rootOptions{url: "http://127.0.0.1:4318"})
	command.SetArgs([]string{"--replay-ready", "--flight-file", flightFile, "--", "/bin/sh", "-c", "touch \"$1\"", "flight-test", marker})
	if err := command.Execute(); err == nil {
		t.Fatal("invalid replay configuration was accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("child started before replay validation: %v", err)
	}
}

func TestRecordProjectFlagOverridesFlightFileAndExportsEntrypointContract(t *testing.T) {
	directory := t.TempDir()
	flightFile := filepath.Join(directory, "flight.yaml")
	marker := filepath.Join(directory, "project")
	content := "version: \"1\"\nproject_id: file-project\nentrypoint:\n  command: [/agent]\n  image: sha256:" + strings.Repeat("a", 64) + "\n  input_mode: stdin_json\n  output_mode: stdout_json\n"
	if err := os.WriteFile(flightFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newRecord(&rootOptions{url: "http://127.0.0.1:4318"})
	command.SetArgs([]string{"--project", "flag-project", "--replay-ready", "--flight-file", flightFile, "--", "/bin/sh", "-c", "printf '%s\\n%s' \"$FLIGHT_PROJECT_ID\" \"$FLIGHT_ENTRYPOINT_JSON\" > \"$1\"", "flight-test", marker})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(actual), "flag-project\n") || !strings.Contains(string(actual), `"schema_version":"1"`) || strings.Contains(string(actual), "file-project") {
		t.Fatalf("recorded environment = %s", actual)
	}
}
