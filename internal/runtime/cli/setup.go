package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/source"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
	flightversion "github.com/krishnangopal1810/ai-flight-recorder/internal/version"
)

func newInit() *cobra.Command {
	var project string
	var modelHosts, credentials []string
	var modelOverride bool
	command := &cobra.Command{Use: "init --project <id> [flags] -- <container-entrypoint argv...>", Short: "Create a safe replay application definition", Args: cobra.MinimumNArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		filename := "flight.yaml"
		if _, err := os.Stat(filename); err == nil {
			return types.NewError(types.ErrConflict, "flight.yaml already exists", "review the existing file instead of replacing it implicitly", nil)
		}
		file := config.FlightFile{Version: "1", ProjectID: project, Entrypoint: types.Entrypoint{
			Command: args, WorkingDir: "/workspace", Dockerfile: "Dockerfile", Context: ".",
			InputMode: "stdin_json", OutputMode: "stdout_json", Timeout: 2 * time.Minute,
			CPULimit: 1, MemoryLimitMB: 512, Environment: credentials, ModelHosts: modelHosts,
			ModelOverride: modelOverride, Sandboxes: map[string]string{},
		}}
		if err := config.ValidateFlightFile(&file); err != nil {
			return err
		}
		content, err := yaml.Marshal(file)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if os.IsExist(err) {
				return types.NewError(types.ErrConflict, "flight.yaml already exists", "review the existing file instead of replacing it implicitly", err)
			}
			return err
		}
		defer output.Close()
		if _, err := output.Write(content); err != nil {
			return err
		}
		return output.Sync()
	}}
	command.Flags().StringVar(&project, "project", "", "stable project ID")
	command.Flags().StringArrayVar(&modelHosts, "model-host", nil, "live model hostname (repeatable)")
	command.Flags().StringArrayVar(&credentials, "credential", nil, "replay credential environment name (repeatable; values are never stored)")
	command.Flags().BoolVar(&modelOverride, "accept-model-override", false, "declare that the agent reads FLIGHT_REPLAY_MODEL")
	_ = command.MarkFlagRequired("project")
	return command
}

func newRecord(options *rootOptions) *cobra.Command {
	var project, flightFile string
	var replayReady bool
	command := &cobra.Command{Use: "record [flags] -- <application command>", Short: "Run an application with Flight capture enabled", Args: cobra.MinimumNArgs(1)}
	command.RunE = func(_ *cobra.Command, args []string) error {
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			return types.NewError(types.ErrValidation, "record command is missing an application", "pass an argv command after flight record --", nil)
		}
		var file config.FlightFile
		filePresent := false
		if _, err := os.Stat(flightFile); err == nil {
			loaded, loadErr := config.LoadFlightFile(flightFile)
			if loadErr != nil {
				return loadErr
			}
			file, filePresent = loaded, true
		} else if replayReady {
			return types.NewError(types.ErrValidation, "replay-ready recording requires flight.yaml", "run flight init before flight record --replay-ready", err)
		} else if !os.IsNotExist(err) {
			return err
		}
		if project == "" && filePresent {
			project = file.ProjectID
		}
		if project == "" {
			return types.NewError(types.ErrValidation, "recording project is required", "pass --project or set project_id in flight.yaml", nil)
		}
		if err := config.ValidateProjectID(project); err != nil {
			return err
		}
		entrypointJSON := ""
		if filePresent {
			if replayReady && file.Entrypoint.Image == "" {
				built, err := source.NewBuilder("git", config.OCIEnvironment()).Build(context.Background(), "", file.Entrypoint)
				if err != nil {
					return err
				}
				file.Entrypoint.Image = built.ImageDigest
				if err := config.ValidateFlightFile(&file); err != nil {
					return err
				}
			}
			encoded, err := json.Marshal(map[string]any{"schema_version": "1", "entrypoint": file.Entrypoint})
			if err != nil {
				return types.NewError(types.ErrValidation, "cannot encode replay entrypoint", "fix flight.yaml and retry", err)
			}
			entrypointJSON = string(encoded)
		}
		mode := "inspect-only"
		if replayReady {
			mode = "replay-ready"
		}
		_, _ = fmt.Fprintf(os.Stderr, "flight: recording %s run for project %s\n", mode, project)
		child := exec.Command(args[0], args[1:]...)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		child.Env = recordEnvironment(os.Environ(), options.url, options.token, project, entrypointJSON)
		if err := child.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return types.NewError(types.ErrBuild, "recorded application exited unsuccessfully", fmt.Sprintf("application exit code: %d", exit.ExitCode()), err)
			}
			return err
		}
		return nil
	}
	command.Flags().StringVar(&project, "project", "", "stable project ID (overrides flight.yaml)")
	command.Flags().BoolVar(&replayReady, "replay-ready", false, "resolve an immutable replay image before starting the application")
	command.Flags().StringVar(&flightFile, "flight-file", "flight.yaml", "replay application definition")
	return command
}

func recordEnvironment(base []string, endpoint, token, project, entrypointJSON string) []string {
	replaced := map[string]bool{"FLIGHT_CAPTURE": true, "FLIGHT_PROJECT_ID": true, "FLIGHT_ENTRYPOINT_JSON": true, "OTEL_EXPORTER_OTLP_ENDPOINT": true, "OTEL_RESOURCE_ATTRIBUTES": true}
	if token != "" {
		replaced["FLIGHT_OTLP_TOKEN"] = true
		replaced["OTEL_EXPORTER_OTLP_HEADERS"] = true
	}
	environment := make([]string, 0, len(base)+4)
	existingHeaders, existingResourceAttributes := "", ""
	for _, entry := range base {
		name, value, found := strings.Cut(entry, "=")
		if token != "" && name == "OTEL_EXPORTER_OTLP_HEADERS" {
			existingHeaders = value
		}
		if name == "OTEL_RESOURCE_ATTRIBUTES" {
			existingResourceAttributes = value
		}
		if !found || !replaced[name] {
			environment = append(environment, entry)
		}
	}
	environment = append(environment,
		"FLIGHT_CAPTURE=true",
		"FLIGHT_PROJECT_ID="+project,
		"OTEL_EXPORTER_OTLP_ENDPOINT="+endpoint,
		"OTEL_RESOURCE_ATTRIBUTES="+mergeResourceAttributes(existingResourceAttributes, project),
	)
	if entrypointJSON != "" {
		environment = append(environment, "FLIGHT_ENTRYPOINT_JSON="+entrypointJSON)
	}
	if token != "" {
		environment = append(environment, "FLIGHT_OTLP_TOKEN="+token, "OTEL_EXPORTER_OTLP_HEADERS="+mergeOTLPAuthorization(existingHeaders, token))
	}
	return environment
}

func mergeResourceAttributes(attributes, project string) string {
	kept := make([]string, 0)
	for _, attribute := range splitEscapedList(attributes) {
		name, _, found := strings.Cut(strings.TrimSpace(attribute), "=")
		if found && name != "flight.capture" && name != "flight.project.id" {
			kept = append(kept, strings.TrimSpace(attribute))
		}
	}
	kept = append(kept, "flight.capture=true", "flight.project.id="+project)
	return strings.Join(kept, ",")
}

func splitEscapedList(value string) []string {
	items := []string{}
	start, escaped := 0, false
	for index, character := range value {
		if character == ',' && !escaped {
			items = append(items, value[start:index])
			start = index + 1
		}
		if escaped {
			escaped = false
		} else if character == '\\' {
			escaped = true
		}
	}
	items = append(items, value[start:])
	return items
}

func newVersion(options *rootOptions) *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Show CLI, release, and schema versions", RunE: func(_ *cobra.Command, _ []string) error {
		if options.json {
			return writeOutput(os.Stdout, map[string]string{"release_version": flightversion.Release, "schema_version": types.SchemaVersion}, true)
		}
		_, err := fmt.Fprintf(os.Stdout, "flight %s (schema %s)\n", flightversion.Release, types.SchemaVersion)
		return err
	}}
}

func mergeOTLPAuthorization(headers, token string) string {
	kept := make([]string, 0)
	for _, header := range strings.Split(headers, ",") {
		name, _, found := strings.Cut(strings.TrimSpace(header), "=")
		if found && !strings.EqualFold(strings.TrimSpace(name), "authorization") {
			kept = append(kept, strings.TrimSpace(header))
		}
	}
	kept = append(kept, "authorization=Bearer%20"+token)
	return strings.Join(kept, ",")
}
