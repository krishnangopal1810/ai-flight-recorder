package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/krishnangopal1810/ai-flight-recorder/internal/config"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/providers/source"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/app"
	flightclient "github.com/krishnangopal1810/ai-flight-recorder/internal/runtime/client"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/service"
	"github.com/krishnangopal1810/ai-flight-recorder/internal/types"
)

type rootOptions struct {
	url, token, config string
	json               bool
}

func Execute() int {
	options := &rootOptions{}
	root := newRoot(options)
	if err := root.Execute(); err != nil {
		var reported *reportedError
		if !errors.As(err, &reported) {
			writeCLIError(os.Stderr, err, options.json)
		}
		return exitCode(err)
	}
	return 0
}

func newRoot(options *rootOptions) *cobra.Command {
	defaultServiceURL, defaultToken := config.ClientEnvironment()
	command := &cobra.Command{Use: "flight", Short: "Reproduce and isolate AI agent failures", SilenceUsage: true, SilenceErrors: true}
	command.PersistentFlags().StringVar(&options.url, "url", defaultServiceURL, "Flight service URL")
	command.PersistentFlags().StringVar(&options.token, "token", defaultToken, "scoped API token")
	command.PersistentFlags().StringVar(&options.config, "config", "", "service configuration YAML")
	command.PersistentFlags().BoolVar(&options.json, "json", false, "emit machine-readable JSON")
	command.AddCommand(
		newServe(options), newInit(), newRecord(options), newCapsule(options), newPlayback(options), newReplay(options), newDiff(options), newToken(options), newEvaluator(options), newSchema(), newHelpMarkdown(command),
		newBisect(options), newDemo(options),
	)
	return command
}

func newServe(options *rootOptions) *cobra.Command {
	return &cobra.Command{Use: "serve", Short: "Run the local or single-tenant service", RunE: func(_ *cobra.Command, _ []string) error {
		cfg, err := config.Load(options.config)
		if err != nil {
			return err
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		application, err := app.New(ctx, cfg)
		if err != nil {
			return err
		}
		defer application.Close()
		return application.Serve(ctx)
	}}
}

func newInit() *cobra.Command {
	var force bool
	command := &cobra.Command{Use: "init", Short: "Create a safe replay application definition", RunE: func(_ *cobra.Command, _ []string) error {
		filename := "flight.yaml"
		if !force {
			if _, err := os.Stat(filename); err == nil {
				return types.NewError(types.ErrConflict, "flight.yaml already exists", "use --force to replace it", nil)
			}
		}
		content := "version: \"1\"\nentrypoint:\n  command: [\"python\", \"app.py\"]\n  working_dir: /workspace\n  dockerfile: Dockerfile\n  context: .\n  input_mode: stdin_json\n  output_mode: stdout_json\n  timeout: 2m\n  cpu_limit: 1\n  memory_limit_mb: 512\n  environment: []\n  model_hosts: []\n  sandboxes: {}\n"
		return os.WriteFile(filename, []byte(content), 0o600)
	}}
	command.Flags().BoolVar(&force, "force", false, "replace an existing flight.yaml")
	return command
}

func newRecord(options *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "record -- <application command>", Short: "Run an application with Flight capture enabled", Args: cobra.MinimumNArgs(1), DisableFlagParsing: true}
	command.RunE = func(_ *cobra.Command, args []string) error {
		if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
			return command.Help()
		}
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) == 0 {
			return types.NewError(types.ErrValidation, "record command is missing an application", "pass an argv command after flight record --", nil)
		}
		if len(args) == 0 {
			return types.NewError(types.ErrValidation, "application command is required", "use flight record -- <command>", nil)
		}
		child := exec.Command(args[0], args[1:]...)
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		child.Env = recordEnvironment(os.Environ(), options.url, options.token)
		if err := child.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return types.NewError(types.ErrBuild, "recorded application exited unsuccessfully", fmt.Sprintf("application exit code: %d", exit.ExitCode()), err)
			}
			return err
		}
		return nil
	}
	return command
}

func recordEnvironment(base []string, endpoint, token string) []string {
	replaced := map[string]bool{"FLIGHT_CAPTURE": true, "OTEL_EXPORTER_OTLP_ENDPOINT": true}
	if token != "" {
		replaced["FLIGHT_OTLP_TOKEN"] = true
		replaced["OTEL_EXPORTER_OTLP_HEADERS"] = true
	}
	environment := make([]string, 0, len(base)+4)
	existingHeaders := ""
	for _, entry := range base {
		name, value, found := strings.Cut(entry, "=")
		if token != "" && name == "OTEL_EXPORTER_OTLP_HEADERS" {
			existingHeaders = value
		}
		if !found || !replaced[name] {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, "FLIGHT_CAPTURE=true", "OTEL_EXPORTER_OTLP_ENDPOINT="+endpoint)
	if token != "" {
		environment = append(environment, "FLIGHT_OTLP_TOKEN="+token, "OTEL_EXPORTER_OTLP_HEADERS="+mergeOTLPAuthorization(existingHeaders, token))
	}
	return environment
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

func newCapsule(options *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "capsule", Short: "List, inspect, import, and export Run Capsules"}
	var project string
	var limit int
	list := &cobra.Command{Use: "list", Short: "List capsules", RunE: func(_ *cobra.Command, _ []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		items, err := client.ListCapsules(context.Background(), project, limit)
		if err != nil {
			return err
		}
		return writeOutput(os.Stdout, items, options.json)
	}}
	list.Flags().StringVar(&project, "project", "", "filter by project")
	list.Flags().IntVar(&limit, "limit", 100, "maximum results")
	show := &cobra.Command{Use: "show <capsule-id>", Args: cobra.ExactArgs(1), Short: "Show and verify a capsule", RunE: func(_ *cobra.Command, args []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		capsule, err := client.GetCapsule(context.Background(), args[0])
		if err != nil {
			return err
		}
		return writeOutput(os.Stdout, capsule, options.json)
	}}
	completeness := &cobra.Command{Use: "completeness <capsule-id>", Args: cobra.ExactArgs(1), Short: "Report playback, replay, diff, and bisect prerequisites", RunE: func(_ *cobra.Command, args []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		report, err := client.Completeness(context.Background(), args[0])
		if err != nil {
			return err
		}
		return writeOutput(os.Stdout, report, options.json)
	}}
	export := &cobra.Command{Use: "export <capsule-id> <file.tar.zst>", Args: cobra.ExactArgs(2), Short: "Export a portable capsule bundle", RunE: func(_ *cobra.Command, args []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		if err := client.Export(context.Background(), args[0], args[1]); err != nil {
			return err
		}
		return writeOutput(os.Stdout, map[string]string{"capsule_id": args[0], "file": args[1]}, options.json)
	}}
	importCommand := &cobra.Command{Use: "import <file.tar.zst>", Args: cobra.ExactArgs(1), Short: "Import and verify a capsule bundle", RunE: func(_ *cobra.Command, args []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		capsule, err := client.Import(context.Background(), args[0])
		if err != nil {
			return err
		}
		return writeOutput(os.Stdout, capsule, options.json)
	}}
	command.AddCommand(list, show, completeness, export, importCommand)
	return command
}

func newPlayback(options *rootOptions) *cobra.Command {
	return &cobra.Command{Use: "playback <capsule-id>", Args: cobra.ExactArgs(1), Short: "Render recorded evidence without execution", RunE: func(_ *cobra.Command, args []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		playback, err := client.Playback(context.Background(), args[0])
		if err != nil {
			return err
		}
		return writeOutput(os.Stdout, playback, options.json)
	}}
}

func newReplay(options *rootOptions) *cobra.Command {
	var request types.CreateReplayRequest
	command := &cobra.Command{Use: "replay <capsule-id>", Args: cobra.ExactArgs(1), Short: "Run an application under a recorded replay plan", RunE: func(_ *cobra.Command, args []string) error {
		request.SourceCapsuleID = args[0]
		if _, statErr := os.Stat("flight.yaml"); statErr == nil || request.Revision != "" {
			file, err := config.LoadFlightFile("flight.yaml")
			if err != nil {
				return err
			}
			built, err := source.NewBuilder("git", config.OCIEnvironment()).Build(context.Background(), request.Revision, file.Entrypoint)
			if err != nil {
				return err
			}
			request.CandidateImage, request.CandidateCommit = built.ImageDigest, built.CommitSHA
			request.CandidateDirtyDigest, request.CandidateSourceDigest = built.DirtyDigest, built.SourceDigest
		}
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		job, err := client.CreateReplay(context.Background(), request)
		if err != nil {
			return err
		}
		job, err = waitForJob(context.Background(), client, job)
		if err != nil {
			return err
		}
		if job.Error != nil {
			return job.Error
		}
		return writeOutput(os.Stdout, job, options.json)
	}}
	command.Flags().StringVar(&request.Revision, "revision", "", "candidate Git revision")
	command.Flags().StringVar(&request.Model, "model", "", "candidate model name exposed as FLIGHT_REPLAY_MODEL")
	command.Flags().BoolVar(&request.SubstituteLLM, "substitute-llm", false, "substitute recorded model responses")
	command.Flags().BoolVar(&request.AllowSandboxedSideEffects, "allow-sandboxed-side-effects", false, "allow declared mutating boundaries only inside their sandbox")
	command.Flags().IntVar(&request.Repetitions, "repetitions", 1, "number of replay trials")
	command.Flags().Float64Var(&request.PassThreshold, "pass-threshold", 1, "required evaluator pass ratio")
	return command
}

func newDiff(options *rootOptions) *cobra.Command {
	return &cobra.Command{Use: "diff <baseline-capsule> <candidate-capsule>", Args: cobra.ExactArgs(2), Short: "Find the first meaningful divergence", RunE: func(_ *cobra.Command, args []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		result, err := client.Diff(context.Background(), args[0], args[1])
		if err != nil {
			return err
		}
		return writeOutput(os.Stdout, result, options.json)
	}}
}

func newToken(options *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "token", Short: "Manage scoped API tokens"}
	var scopes []string
	create := &cobra.Command{Use: "create <name>", Args: cobra.ExactArgs(1), Short: "Create a token; its value is shown once", RunE: func(_ *cobra.Command, args []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		result, err := client.CreateToken(context.Background(), args[0], scopes)
		if err != nil {
			return err
		}
		return writeOutput(os.Stdout, result, true)
	}}
	create.Flags().StringSliceVar(&scopes, "scope", []string{"read"}, "token scope (repeatable)")
	revoke := &cobra.Command{Use: "revoke <token-id>", Args: cobra.ExactArgs(1), Short: "Revoke a token by its non-secret ID", RunE: func(_ *cobra.Command, args []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		if err := client.RevokeToken(context.Background(), args[0]); err != nil {
			return err
		}
		return writeOutput(os.Stdout, map[string]any{"id": args[0], "revoked": true}, options.json)
	}}
	command.AddCommand(create, revoke)
	return command
}

func newEvaluator(options *rootOptions) *cobra.Command {
	command := &cobra.Command{Use: "evaluator", Short: "Validate and execute versioned incident evaluators"}
	validate := &cobra.Command{Use: "validate <definition.yaml>", Args: cobra.ExactArgs(1), Short: "Validate an evaluator definition", RunE: func(_ *cobra.Command, args []string) error {
		definition, err := readEvaluator(args[0])
		if err != nil {
			return err
		}
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		if err := client.ValidateEvaluator(context.Background(), definition); err != nil {
			return err
		}
		return writeOutput(os.Stdout, map[string]any{"valid": true, "definition": definition}, options.json)
	}}
	var baseline, candidate string
	run := &cobra.Command{Use: "run <definition.yaml>", Args: cobra.ExactArgs(1), Short: "Run an evaluator in an isolated OCI container", RunE: func(_ *cobra.Command, args []string) error {
		if baseline == "" || candidate == "" {
			return types.NewError(types.ErrValidation, "baseline and candidate capsules are required", "set --baseline and --candidate", nil)
		}
		definition, err := readEvaluator(args[0])
		if err != nil {
			return err
		}
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		result, err := client.RunEvaluator(context.Background(), types.EvaluatorRunRequest{Definition: definition, BaselineID: baseline, CandidateID: candidate})
		if err != nil {
			return err
		}
		return writeOutput(os.Stdout, result, options.json)
	}}
	run.Flags().StringVar(&baseline, "baseline", "", "source capsule ID")
	run.Flags().StringVar(&candidate, "candidate", "", "candidate capsule ID")
	runs := &cobra.Command{Use: "runs <capsule-id>", Args: cobra.ExactArgs(1), Short: "List persisted evaluator evidence", RunE: func(_ *cobra.Command, args []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		result, err := client.EvaluatorRuns(context.Background(), args[0])
		if err != nil {
			return err
		}
		return writeOutput(os.Stdout, result, options.json)
	}}
	var exportVersion string
	export := &cobra.Command{Use: "export <evaluator-id> <file>", Args: cobra.ExactArgs(2), Short: "Export a versioned evaluator for local or CI execution", RunE: func(_ *cobra.Command, args []string) error {
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		definition, err := client.GetEvaluator(context.Background(), args[0], exportVersion)
		if err != nil {
			return err
		}
		data, err := yaml.Marshal(definition)
		if err != nil {
			return err
		}
		if err := atomicWrite(args[1], data, 0o600); err != nil {
			return err
		}
		return writeOutput(os.Stdout, map[string]any{"id": definition.ID, "version": definition.Version, "file": args[1]}, options.json)
	}}
	export.Flags().StringVar(&exportVersion, "version", "", "specific evaluator version (latest when omitted)")
	command.AddCommand(validate, run, runs, export)
	return command
}

func newBisect(options *rootOptions) *cobra.Command {
	var good, bad, evaluatorFile string
	var capsuleIDs []string
	var repetitions int
	var passThreshold float64
	var assumeMonotonic, substituteLLM, allowSandbox bool
	command := &cobra.Command{Use: "bisect", Short: "Search Git revisions using controlled replay", RunE: func(_ *cobra.Command, _ []string) error {
		if good == "" || bad == "" || evaluatorFile == "" || len(capsuleIDs) == 0 {
			return types.NewError(types.ErrValidation, "bisect inputs are incomplete", "set --good, --bad, --capsule, and --eval", nil)
		}
		if repetitions <= 0 || passThreshold < 0 || passThreshold > 1 {
			return types.NewError(types.ErrValidation, "bisect trial settings are invalid", "use positive repetitions and a pass threshold from 0 to 1", nil)
		}
		definition, err := readEvaluator(evaluatorFile)
		if err != nil {
			return err
		}
		flightFile, err := config.LoadFlightFile("flight.yaml")
		if err != nil {
			return err
		}
		revisions, err := gitAncestry(context.Background(), good, bad)
		if err != nil {
			return err
		}
		client, err := apiClient(options)
		if err != nil {
			return err
		}
		if err := client.ValidateEvaluator(context.Background(), definition); err != nil {
			return err
		}
		builder := source.NewBuilder("git", config.OCIEnvironment())
		result, err := service.NewBisectService().Search(context.Background(), revisions, assumeMonotonic, func(ctx context.Context, revision string) (types.RevisionEvaluation, error) {
			built, buildErr := builder.Build(ctx, revision, flightFile.Entrypoint)
			if buildErr != nil {
				return types.RevisionEvaluation{Revision: revision, Outcome: types.RevisionInconclusive, Evidence: []string{safeErrorCode(buildErr)}}, nil
			}
			passes, trials, evidence := 0, 0, []string{}
			for _, capsuleID := range capsuleIDs {
				for range repetitions {
					job, replayErr := client.CreateReplay(ctx, types.CreateReplayRequest{SourceCapsuleID: capsuleID, Revision: revision, CandidateImage: built.ImageDigest, CandidateCommit: built.CommitSHA, CandidateDirtyDigest: built.DirtyDigest, CandidateSourceDigest: built.SourceDigest, SubstituteLLM: substituteLLM, AllowSandboxedSideEffects: allowSandbox, Repetitions: 1, PassThreshold: 1})
					if replayErr != nil {
						return types.RevisionEvaluation{Revision: revision, Outcome: types.RevisionInconclusive, Evidence: append(evidence, safeErrorCode(replayErr))}, nil
					}
					job, replayErr = waitForJob(ctx, client, job)
					if replayErr != nil || job.Error != nil {
						if replayErr == nil {
							replayErr = job.Error
						}
						return types.RevisionEvaluation{Revision: revision, Outcome: types.RevisionInconclusive, Evidence: append(evidence, safeErrorCode(replayErr))}, nil
					}
					var replay types.ReplayJobResult
					if json.Unmarshal(job.Result, &replay) != nil || replay.CandidateCapsuleID == "" {
						return types.RevisionEvaluation{Revision: revision, Outcome: types.RevisionInconclusive, Evidence: append(evidence, "invalid_replay_result")}, nil
					}
					run, evaluatorErr := client.RunEvaluator(ctx, types.EvaluatorRunRequest{Definition: definition, BaselineID: capsuleID, CandidateID: replay.CandidateCapsuleID})
					if evaluatorErr != nil {
						return types.RevisionEvaluation{Revision: revision, Outcome: types.RevisionInconclusive, Evidence: append(evidence, safeErrorCode(evaluatorErr))}, nil
					}
					trials++
					if run.Output.Passed {
						passes++
					}
					evidence = append(evidence, run.ID)
				}
			}
			return service.ClassifyRevision(revision, passes, trials, passThreshold, evidence), nil
		})
		if err != nil {
			return err
		}
		if err := writeOutput(os.Stdout, result, options.json); err != nil {
			return err
		}
		if result.Outcome == "inconclusive" || result.Outcome == "non_monotonic" {
			return &reportedError{FlightError: types.NewError(types.ErrInconclusive, result.Message, "inspect per-revision evaluator evidence and rerun after resolving flaky or unsafe revisions", nil)}
		}
		return nil
	}}
	command.Flags().StringVar(&good, "good", "", "known-good Git ref")
	command.Flags().StringVar(&bad, "bad", "", "known-bad Git ref")
	command.Flags().StringSliceVar(&capsuleIDs, "capsule", nil, "replay-ready source capsule (repeatable)")
	command.Flags().StringVar(&evaluatorFile, "eval", "", "versioned evaluator definition")
	command.Flags().IntVar(&repetitions, "repetitions", 3, "trials per capsule and revision")
	command.Flags().Float64Var(&passThreshold, "pass-threshold", 1, "fraction of evaluator passes required for a good revision")
	command.Flags().BoolVar(&assumeMonotonic, "assume-monotonic", false, "skip exhaustive validation and label the result conditional")
	command.Flags().BoolVar(&substituteLLM, "substitute-llm", false, "substitute recorded model outputs")
	command.Flags().BoolVar(&allowSandbox, "allow-sandboxed-side-effects", false, "allow explicitly declared sandbox destinations")
	return command
}

func gitAncestry(ctx context.Context, good, bad string) ([]string, error) {
	ancestor := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", good, bad)
	if err := ancestor.Run(); err != nil {
		return nil, types.NewError(types.ErrValidation, "good ref is not an ancestor of bad ref", "select refs on one ancestry path", err)
	}
	resolve := exec.CommandContext(ctx, "git", "rev-parse", "--verify", good+"^{commit}")
	goodOutput, err := resolve.Output()
	if err != nil {
		return nil, types.NewError(types.ErrValidation, "cannot resolve good ref", "fetch or correct --good", err)
	}
	list := exec.CommandContext(ctx, "git", "rev-list", "--ancestry-path", "--reverse", good+".."+bad)
	output, err := list.Output()
	if err != nil {
		return nil, types.NewError(types.ErrValidation, "cannot enumerate bisect ancestry", "fetch the complete commit history", err)
	}
	revisions := []string{strings.TrimSpace(string(goodOutput))}
	revisions = append(revisions, strings.Fields(string(output))...)
	if len(revisions) < 2 {
		return nil, types.NewError(types.ErrValidation, "bisect refs resolve to the same revision", "select distinct good and bad refs", nil)
	}
	return revisions, nil
}

func waitForJob(ctx context.Context, client *flightclient.Client, job types.Job) (types.Job, error) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for job.State == types.JobQueued || job.State == types.JobRunning {
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-ticker.C:
		}
		updated, err := client.GetJob(ctx, job.ID)
		if err != nil {
			return job, err
		}
		job = updated
	}
	return job, nil
}

func safeErrorCode(err error) string {
	if typed, ok := err.(*types.FlightError); ok {
		return string(typed.Code)
	}
	return "internal"
}

func readEvaluator(filename string) (types.EvaluatorDefinition, error) {
	file, err := os.Open(filepath.Clean(filename))
	if err != nil {
		return types.EvaluatorDefinition{}, types.NewError(types.ErrEvaluator, "cannot read evaluator definition", "check the file path and permissions", err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.KnownFields(true)
	var definition types.EvaluatorDefinition
	if err := decoder.Decode(&definition); err != nil {
		return definition, types.NewError(types.ErrEvaluator, "malformed evaluator definition", "fix the YAML/JSON fields reported by the parser", err)
	}
	return definition, nil
}

func atomicWrite(filename string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(filepath.Clean(filename)), ".flight-write-*")
	if err != nil {
		return types.NewError(types.ErrStorage, "cannot create output file", "check the destination directory permissions", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, filepath.Clean(filename))
}

func apiClient(options *rootOptions) (*flightclient.Client, error) {
	return flightclient.New(options.url, options.token)
}

func writeOutput(writer io.Writer, value any, machine bool) error {
	if machine {
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	switch typed := value.(type) {
	case []types.CapsuleSummary:
		if len(typed) == 0 {
			_, _ = fmt.Fprintln(writer, "No capsules recorded.")
			return nil
		}
		for _, item := range typed {
			_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", item.CapsuleID, item.ProjectID, item.ServiceName, item.TerminalStatus)
		}
		return nil
	default:
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
}

func writeCLIError(writer io.Writer, err error, machine bool) {
	var typed *types.FlightError
	if !errors.As(err, &typed) {
		typed = types.NewError(types.ErrStorage, "command failed", "rerun with --json and inspect service logs", err)
	}
	if machine {
		_ = json.NewEncoder(writer).Encode(map[string]any{"error": typed})
		return
	}
	_, _ = fmt.Fprintf(writer, "flight: %s: %s\n", typed.Code, typed.Message)
	if typed.Remediation != "" {
		_, _ = fmt.Fprintf(writer, "remediation: %s\n", typed.Remediation)
	}
}

func exitCode(err error) int {
	var typed *types.FlightError
	if !errors.As(err, &typed) {
		return 1
	}
	switch typed.Code {
	case types.ErrValidation:
		return 2
	case types.ErrAuthentication:
		return 3
	case types.ErrUnsafeReplay:
		return 4
	case types.ErrIncompleteCapsule, types.ErrUnmatchedBoundary:
		return 5
	case types.ErrInconclusive:
		return 6
	default:
		return 1
	}
}

type reportedError struct{ *types.FlightError }

func (e *reportedError) Unwrap() error { return e.FlightError }
