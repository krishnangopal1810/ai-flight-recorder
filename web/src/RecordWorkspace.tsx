import {type ReactNode, useEffect, useState} from "react";
import {useMutation, useQuery, useQueryClient} from "@tanstack/react-query";
import {api, connection} from "./api";
import {InlineError} from "./RunsWorkspace";

type Method = "local" | "running" | "import";
type TracingAnswer = "ask" | "ready" | "help";
type Navigate = (path: string, replace?: boolean) => void;
type Language = "python" | "typescript";
type ModelMode = "substitute" | "live";

export function RecordWorkspace({scopes, releaseVersion, navigate}: {scopes: string[]; releaseVersion?: string; navigate: Navigate}) {
  const [method, setMethod] = useState<Method>("local");
  const [tracing, setTracing] = useState<TracingAnswer>("ask");
  const [autoInstrument, setAutoInstrument] = useState(false);
  const [agentCommand, setAgentCommand] = useState("python agent.py");
  const [projectName, setProjectName] = useState("my-agent");
  const [language, setLanguage] = useState<Language>("python");
  const [replayReady, setReplayReady] = useState(false);
  const [modelMode, setModelMode] = useState<ModelMode>("substitute");
  const [modelHost, setModelHost] = useState("api.openai.com");
  const [credentialName, setCredentialName] = useState("OPENAI_API_KEY");
  const [modelOverride, setModelOverride] = useState(false);
  const [deployedImage, setDeployedImage] = useState("");
  const [deployedEntrypoint, setDeployedEntrypoint] = useState("");
  const [copied, setCopied] = useState<string | null>(null);
  const [waiting, setWaiting] = useState(false);
  const [baselineRunID, setBaselineRunID] = useState<string | null | undefined>(undefined);
  const client = useQueryClient();
  const canIngest = scopes.includes("ingest") || scopes.includes("admin");
  const origin = connection.serviceURL;
  const development = !releaseVersion || releaseVersion === "development";
  const flight = development ? "./bin/flight" : "flight";
  const executable = autoInstrument ? `opentelemetry-instrument ${agentCommand.trim() || "python agent.py"}` : (agentCommand.trim() || "python agent.py");
  const recordCommand = `${flight} --url ${origin} --token $FLIGHT_TOKEN record --project ${projectName.trim() || "my-agent"}${replayReady ? " --replay-ready" : ""} -- ${executable}`;
  const deployedArgv = parseArgv(deployedEntrypoint);
  const effectiveEntrypoint = method === "running" ? deployedArgv : executable.split(/\s+/);
  const entrypoint = {
	command: effectiveEntrypoint ?? [], image: deployedImage,
    input_mode: "stdin_json", output_mode: "stdout_json", environment: modelMode === "live" ? [credentialName] : [],
    model_hosts: modelMode === "live" ? [modelHost] : [], model_override: modelOverride,
  };
  const otlpCommand = [
    `OTEL_EXPORTER_OTLP_ENDPOINT=${origin}`,
    `OTEL_EXPORTER_OTLP_HEADERS="authorization=Bearer $FLIGHT_TOKEN"`,
    `OTEL_RESOURCE_ATTRIBUTES="flight.capture=true,flight.project.id=${projectName.trim() || "my-agent"}"`,
    `FLIGHT_PROJECT_ID=${projectName.trim() || "my-agent"}`,
	...(replayReady && /^sha256:[a-f0-9]{64}$/.test(deployedImage) && effectiveEntrypoint?.length ? [`FLIGHT_ENTRYPOINT_JSON='${JSON.stringify({schema_version: "1", entrypoint})}'`] : []),
  ].join("\n");
  const replaySetup = <ReplaySetup
    method={method === "running" ? "running" : "local"}
	language={language} releaseVersion={releaseVersion} flight={flight} projectName={projectName} agentCommand={method === "local" ? executable : deployedEntrypoint}
	replayReady={replayReady} modelMode={modelMode} modelHost={modelHost} credentialName={credentialName}
	modelOverride={modelOverride} deployedImage={deployedImage} deployedEntrypoint={deployedEntrypoint}
    onLanguage={setLanguage} onReplayReady={setReplayReady} onModelMode={setModelMode} onModelHost={setModelHost}
	onCredentialName={setCredentialName} onModelOverride={setModelOverride} onDeployedImage={setDeployedImage} onDeployedEntrypoint={setDeployedEntrypoint}
  />;
  const latestRun = useQuery({
    queryKey: ["record-latest-run"],
    queryFn: () => api.capsules({order: "newest", limit: 1}),
    refetchInterval: waiting ? 1500 : false,
  });
  const importRun = useMutation({
    mutationFn: (file: File) => api.importCapsule(file),
    onSuccess: async capsule => {
      await client.invalidateQueries({queryKey: ["runs"]});
      navigate(`/runs/${encodeURIComponent(capsule.capsule_id)}/understand`);
    },
  });

  useEffect(() => {
    if (baselineRunID !== undefined || !latestRun.data) return;
    setBaselineRunID(latestRun.data.items[0]?.capsule_id ?? null);
  }, [latestRun.data, baselineRunID]);

  useEffect(() => {
    if (!waiting || baselineRunID === undefined) return;
    const captured = latestRun.data?.items[0];
    if (!captured || captured.capsule_id === baselineRunID) return;
    setWaiting(false);
    void client.invalidateQueries({queryKey: ["runs"]});
    navigate(`/runs/${encodeURIComponent(captured.capsule_id)}/understand`);
  }, [waiting, baselineRunID, latestRun.data, client, navigate]);

  async function copy(name: string, value: string, listen = false) {
    await navigator.clipboard.writeText(value);
    setCopied(name);
    if (listen) {
      setWaiting(true);
      void latestRun.refetch();
    }
    window.setTimeout(() => setCopied(null), 1500);
  }

  function chooseMethod(next: Method) {
    setMethod(next);
    setWaiting(false);
  }

  function startListening() {
    setWaiting(true);
    void latestRun.refetch();
  }

  return <section className="record-workspace" aria-labelledby="record-title">
    <header className="record-header record-intro">
      <div>
        <p className="overline">Add a run</p>
        <h1 id="record-title">See what your agent did</h1>
        <p>Run your agent once. Flight Recorder will open a visual timeline of its model calls, tool calls, inputs, and outputs.</p>
      </div>
      <button className="secondary-button" onClick={() => navigate("/runs")}>View recorded runs</button>
    </header>

    <ol className="record-outcome" aria-label="What happens next">
      <li><span>1</span><div><strong>Connect</strong><small>Choose where the agent runs</small></div></li>
      <li><span>2</span><div><strong>Run once</strong><small>Make one real agent request</small></div></li>
      <li><span>3</span><div><strong>Inspect</strong><small>We open the timeline automatically</small></div></li>
    </ol>

    <section className="record-choice" aria-labelledby="record-choice-title">
      <div><div><p className="overline">Choose one</p><h2 id="record-choice-title">Where is the agent you want to inspect?</h2></div></div>
      <nav className="record-methods" aria-label="Ways to add a run">
        <button className={method === "local" ? "active" : ""} onClick={() => chooseMethod("local")} aria-pressed={method === "local"}>
          <span className="method-icon" aria-hidden="true">›_</span><strong>On this computer</strong><span>I can start it from a terminal.</span>
        </button>
        <button className={method === "running" ? "active" : ""} onClick={() => chooseMethod("running")} aria-pressed={method === "running"}>
          <span className="method-icon" aria-hidden="true">●</span><strong>Already running</strong><span>It is deployed or runs in another process.</span>
        </button>
        <button className={method === "import" ? "active" : ""} onClick={() => chooseMethod("import")} aria-pressed={method === "import"}>
          <span className="method-icon" aria-hidden="true">↑</span><strong>A recording file</strong><span>I have a .tar.zst export.</span>
        </button>
      </nav>
    </section>

    {method === "local" && <LocalAgentCapture
      tracing={tracing}
      command={agentCommand}
	  projectName={projectName}
      generatedCommand={recordCommand}
      copied={copied === "record"}
      waiting={waiting}
      ready={!latestRun.isLoading && baselineRunID !== undefined}
      error={latestRun.error}
      onTracing={value => {
        setTracing(value);
        if (value === "ready" && tracing === "help") setAutoInstrument(true);
      }}
      onCommand={value => {
        setAgentCommand(value);
        if (!/(^|\s)(python|python3)(\s|$)|^uv\s+run\b/.test(value.trim())) setAutoInstrument(false);
      }}
	  onProjectName={setProjectName}
      onCopy={() => copy("record", recordCommand, true)}
      onListen={startListening}
      onStop={() => setWaiting(false)}
      onRuns={() => navigate("/runs")}
	  replaySetup={replaySetup}
    />}

    {method === "running" && <RunningAgentCapture
      projectName={projectName}
      command={otlpCommand}
      copied={copied === "otlp"}
      waiting={waiting}
      ready={!latestRun.isLoading && baselineRunID !== undefined}
      error={latestRun.error}
      onProjectName={setProjectName}
      onCopy={() => copy("otlp", otlpCommand, true)}
      onListen={startListening}
      onStop={() => setWaiting(false)}
      onRuns={() => navigate("/runs")}
      onLocal={() => chooseMethod("local")}
	  replaySetup={replaySetup}
    />}

    {method === "import" && <section className="capture-panel import-panel" aria-labelledby="import-title">
      <header className="capture-panel-heading"><div><p className="overline">Recording file</p><h2 id="import-title">Import a recorded run</h2><p>Open a <code>.tar.zst</code> file downloaded from this or another Flight Recorder.</p></div></header>
      <div className="import-layout">
        <div className="import-action">
          <div className="instruction-heading"><span>1</span><div><h3>Choose the recording file</h3><p>Before adding it, Flight Recorder checks that the recording is complete and has not been changed or damaged.</p></div></div>
          {canIngest ? <label className={`bundle-upload ${importRun.isPending ? "pending" : ""}`}><span aria-hidden="true">↑</span><strong>{importRun.isPending ? "Checking the recording…" : "Choose a .tar.zst file"}</strong><small>{importRun.isPending ? "The run will open automatically when the check finishes." : "The imported run opens automatically."}</small><input type="file" accept=".zst,application/zstd" disabled={importRun.isPending} onChange={event => {const file = event.target.files?.[0]; if (file) importRun.mutate(file);}} /></label> : <p className="scope-warning">Your access token does not allow recording imports. Ask for ingest access, then reconnect.</p>}
          {importRun.error && <InlineError title="This recording could not be opened" error={importRun.error} />}
        </div>
        <aside className="export-help" aria-labelledby="export-help-title">
          <p className="overline">Don’t have a file yet?</p>
          <h3 id="export-help-title">Download one from any recorded run</h3>
          <ol><li><span>1</span><p>Open <strong>Runs</strong>.</p></li><li><span>2</span><p>Select the run you want to keep or share.</p></li><li><span>3</span><p>Choose <strong>Download recording</strong>.</p></li></ol>
          <button className="secondary-button" onClick={() => navigate("/runs")}>Open recorded runs</button>
          <details><summary>Export from the command line</summary><p>Use the run ID shown in the run’s details:</p><code>flight capsule export &lt;run-id&gt; recording.tar.zst</code></details>
        </aside>
      </div>
    </section>}
  </section>;
}

function LocalAgentCapture({tracing, command, projectName, generatedCommand, copied, waiting, ready, error, onTracing, onCommand, onProjectName, onCopy, onListen, onStop, onRuns, replaySetup}: {
	tracing: TracingAnswer; command: string; projectName: string; generatedCommand: string; copied: boolean; waiting: boolean; ready: boolean; error: Error | null; replaySetup: ReactNode;
	onTracing: (value: TracingAnswer) => void; onCommand: (value: string) => void; onProjectName: (value: string) => void; onCopy: () => void; onListen: () => void; onStop: () => void; onRuns: () => void;
}) {
  return <section className="capture-panel" aria-labelledby="local-capture-title">
    <header className="capture-panel-heading"><div><p className="overline">On this computer</p><h2 id="local-capture-title">Run your agent through Flight Recorder</h2><p>We do not change your agent. We point its existing trace output at this recorder for one run.</p></div></header>

    {tracing === "ask" && <section className="tracing-question" aria-labelledby="tracing-question-title">
      <div><span className="setup-check" aria-hidden="true">?</span><div><h3 id="tracing-question-title">Does this agent already create traces?</h3><p>If you already use OpenTelemetry, LangSmith, Datadog, Honeycomb, or another tracing tool, choose yes.</p></div></div>
      <div className="tracing-actions"><button className="primary-button" onClick={() => onTracing("ready")}>Yes, tracing is set up</button><button className="secondary-button" onClick={() => onTracing("help")}>No / I’m not sure</button></div>
    </section>}

    {tracing === "help" && <TracingHelp onReady={() => onTracing("ready")} />}

	{tracing === "ready" && <>
	  <div className="capture-command-builder">
		<div className="instruction-heading"><span>1</span><div><h3>Name the project and enter the command you normally run</h3><p>The project ID is added without replacing existing OpenTelemetry resource attributes.</p></div></div>
		<label><span>Project ID</span><input aria-label="Project ID" value={projectName} onChange={event => onProjectName(event.target.value)} placeholder="support-agent" spellCheck={false} /></label>
		<label><span>Your agent command</span><input aria-label="Your agent command" value={command} onChange={event => onCommand(event.target.value)} placeholder="python agent.py" spellCheck={false} /></label>
        <div className="instruction-heading command-step"><span>2</span><div><h3>Copy this command and run it in your terminal</h3><p>The page starts listening as soon as you copy.</p></div></div>
        <div className="generated-command"><div><span>Ready to run</span><code>{generatedCommand}</code></div><button className="primary-button" onClick={onCopy}>{copied ? "Copied — listening" : "Copy and listen"}</button></div>
        <p className="capture-helper"><strong>Before running:</strong> set <code>FLIGHT_TOKEN</code> in that terminal to the access token you used to connect. The token is never displayed here.</p>
      </div>
      <CaptureRunFinder waiting={waiting} ready={ready} error={error} onListen={onListen} onStop={onStop} onRuns={onRuns} />
      <details className="capture-troubleshooting"><summary>My run did not appear</summary><div><ol><li>Confirm the agent finished and created at least one trace.</li><li>Confirm <code>FLIGHT_TOKEN</code> is set in the same terminal.</li><li>Look for a trace-export error in the agent output, then run the copied command again.</li></ol></div></details>
	  {replaySetup}
    </>}
  </section>;
}

function TracingHelp({onReady}: {onReady: () => void}) {
  return <section className="tracing-help" aria-labelledby="tracing-help-title">
    <div className="tracing-help-intro"><span className="setup-check" aria-hidden="true">1</span><div><h3 id="tracing-help-title">Add tracing first</h3><p>Flight Recorder reconstructs a run from its trace. For a Python agent, automatic OpenTelemetry setup is the quickest way to get a first timeline.</p></div></div>
    <div className="quick-setup-command"><span>Run once in your project</span><code>pip install opentelemetry-distro opentelemetry-exporter-otlp</code><code>opentelemetry-bootstrap -a install</code></div>
    <p className="tracing-help-note">This captures supported model, HTTP, and framework calls. You can add explicit model/tool controls later when you want safe replay.</p>
    <div className="tracing-help-actions"><button className="primary-button" onClick={onReady}>Tracing is installed — continue</button><details><summary>Using TypeScript or another language?</summary><p>Configure its standard OpenTelemetry SDK and exporter, then return here. The next step supplies the recorder address and authorization automatically.</p></details></div>
  </section>;
}

function RunningAgentCapture({projectName, command, copied, waiting, ready, error, onProjectName, onCopy, onListen, onStop, onRuns, onLocal, replaySetup}: {
	projectName: string; command: string; copied: boolean; waiting: boolean; ready: boolean; error: Error | null; replaySetup: ReactNode;
  onProjectName: (value: string) => void; onCopy: () => void; onListen: () => void; onStop: () => void; onRuns: () => void; onLocal: () => void;
}) {
  return <section className="capture-panel" aria-labelledby="running-capture-title">
    <header className="capture-panel-heading"><div><p className="overline">Already running</p><h2 id="running-capture-title">Connect an agent that already sends traces</h2><p>Use this for a deployed service or another running process. Add three environment settings, restart it, and make one request.</p></div></header>
    <div className="eligibility-note"><strong>Not sure whether the agent sends traces?</strong><span>Use the local setup instead—we’ll help you check.</span><button className="text-button" onClick={onLocal}>Use local setup</button></div>
    <div className="running-setup">
      <div className="instruction-heading"><span>1</span><div><h3>Name this agent</h3><p>This is how its runs will be grouped in the inbox.</p></div></div>
      <label className="project-field"><span>Agent or project name</span><input aria-label="Agent or project name" value={projectName} onChange={event => onProjectName(event.target.value)} placeholder="support-agent" /></label>
      <div className="instruction-heading"><span>2</span><div><h3>Copy these settings into the agent’s environment</h3><p>They tell its existing trace exporter where to send the next run and which project it belongs to.</p></div></div>
      <div className="generated-command standalone"><div><span>Environment settings</span><code>{command}</code></div><button className="primary-button" onClick={onCopy}>{copied ? "Copied — listening" : "Copy and listen"}</button></div>
      <p className="capture-helper"><strong>If you already use <code>OTEL_RESOURCE_ATTRIBUTES</code>:</strong> append the two <code>flight.*</code> values instead of replacing your existing attributes.</p>
      <div className="instruction-heading final-running-step"><span>3</span><div><h3>Restart the agent and make one request</h3><p>Keep this page open. The new run opens here automatically.</p></div></div>
    </div>
    <CaptureRunFinder waiting={waiting} ready={ready} error={error} onListen={onListen} onStop={onStop} onRuns={onRuns} />
    <details className="capture-troubleshooting"><summary>What do these settings do?</summary><div><dl><div><dt>Trace destination</dt><dd>Points the standard OpenTelemetry exporter at Flight Recorder.</dd></div><div><dt>Authorization</dt><dd>Uses the access token from <code>FLIGHT_TOKEN</code>; its value is never shown here.</dd></div><div><dt>Run marker</dt><dd>Keeps only opted-in traces and groups them under the agent name above.</dd></div></dl></div></details>
	{replaySetup}
  </section>;
}

function CaptureRunFinder({waiting, ready, error, onListen, onStop, onRuns}: {waiting: boolean; ready: boolean; error: Error | null; onListen: () => void; onStop: () => void; onRuns: () => void}) {
  return <section className={`capture-finder ${waiting ? "waiting" : ""}`} aria-live="polite">
    <div className="finder-copy"><span className="finder-status" aria-hidden="true">{waiting ? "" : "✓"}</span><div><h3>{waiting ? "Listening for your next run…" : "Ready to watch for a run"}</h3><p>{waiting ? "Run or call the agent now. The timeline will open automatically when the recording arrives." : "Start listening before you run the agent so this page can open it for you."}</p></div></div>
    <div className="finder-actions">{waiting ? <button className="secondary-button" onClick={onStop}>Stop listening</button> : <button className="primary-button" disabled={!ready} onClick={onListen}>{ready ? "Start listening" : "Connecting…"}</button>}<button className="text-button" onClick={onRuns}>Open existing runs</button></div>
    {error && <InlineError title="Flight Recorder cannot check for a new run" error={error} />}
  </section>;
}

function ReplaySetup({method, language, releaseVersion, flight, projectName, agentCommand, replayReady, modelMode, modelHost, credentialName, modelOverride, deployedImage, deployedEntrypoint, onLanguage, onReplayReady, onModelMode, onModelHost, onCredentialName, onModelOverride, onDeployedImage, onDeployedEntrypoint}: {
  method: "local" | "running"; language: Language; releaseVersion?: string; flight: string; projectName: string; agentCommand: string;
  replayReady: boolean; modelMode: ModelMode; modelHost: string; credentialName: string; modelOverride: boolean; deployedImage: string; deployedEntrypoint: string;
  onLanguage: (value: Language) => void; onReplayReady: (value: boolean) => void; onModelMode: (value: ModelMode) => void;
  onModelHost: (value: string) => void; onCredentialName: (value: string) => void; onModelOverride: (value: boolean) => void; onDeployedImage: (value: string) => void; onDeployedEntrypoint: (value: string) => void;
}) {
  const development = !releaseVersion || releaseVersion === "development";
  const version = releaseVersion ?? "development";
  const tag = version.startsWith("v") ? version : `v${version}`;
  const pythonVersion = pep440Version(version.replace(/^v/, ""));
  const sdkInstall = development
    ? language === "python" ? "pip install -e ./sdk/python" : "npm install ./sdk/typescript"
    : language === "python"
	  ? `SDK=ai_flight_recorder-${pythonVersion}-py3-none-any.whl
curl -fLO "https://github.com/krishnangopal1810/ai-flight-recorder/releases/download/${tag}/\${SDK}"
curl -fLO "https://github.com/krishnangopal1810/ai-flight-recorder/releases/download/${tag}/release-checksums.txt"
grep "  \${SDK}$" release-checksums.txt | shasum -a 256 -c -
pip install "./\${SDK}"`
	  : `SDK=ai-flight-recorder-sdk-${version.replace(/^v/, "")}.tgz
curl -fLO "https://github.com/krishnangopal1810/ai-flight-recorder/releases/download/${tag}/\${SDK}"
curl -fLO "https://github.com/krishnangopal1810/ai-flight-recorder/releases/download/${tag}/release-checksums.txt"
grep "  \${SDK}$" release-checksums.txt | shasum -a 256 -c -
npm install "./\${SDK}"`;
  const cliInstall = development ? `make bootstrap
mkdir -p bin
go build -o ./bin/flight ./cmd/flight
./bin/flight version`
    : `VERSION=${version.replace(/^v/, "")}
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in x86_64) ARCH=amd64 ;; arm64|aarch64) ARCH=arm64 ;; *) exit 1 ;; esac
ARCHIVE="ai-flight-recorder_\${VERSION}_\${OS}_\${ARCH}.tar.gz"
curl -fLO "https://github.com/krishnangopal1810/ai-flight-recorder/releases/download/${tag}/\${ARCHIVE}"
curl -fLO "https://github.com/krishnangopal1810/ai-flight-recorder/releases/download/${tag}/checksums.txt"
grep "  \${ARCHIVE}$" checksums.txt | shasum -a 256 -c -
tar -xzf "\${ARCHIVE}"
sudo install flight /usr/local/bin/flight
flight version`;
  const initFlags = [
    `${flight} init --project ${projectName.trim() || "my-agent"}`,
    ...(modelMode === "live" ? [`--model-host ${modelHost.trim() || "api.openai.com"}`, `--credential ${credentialName.trim() || "OPENAI_API_KEY"}`] : []),
    ...(modelOverride ? ["--accept-model-override"] : []),
    `-- ${agentCommand.trim() || (language === "python" ? "python agent.py" : "npm run agent")}`,
  ].join(" ");
  const sample = language === "python" ? pythonReplaySample(modelMode) : typescriptReplaySample(modelMode);

  return <details className="replay-enablement">
    <summary><span className="optional-pill">Before recording</span><span><strong>Need to change a model or tool value and replay this run?</strong><small>Complete this checklist once. Inspect-only recording remains available without it.</small></span><b>Show replay setup</b></summary>
    <div className="replay-setup-guide">
      <label className="replay-ready-choice"><input type="checkbox" checked={replayReady} onChange={event => onReplayReady(event.target.checked)} /><span><strong>Prepare this recording for controlled replay</strong><small>This validates or builds an immutable image before your agent starts.</small></span></label>
      {replayReady && <>
        <nav className="setup-language" aria-label="SDK language"><button className={language === "python" ? "active" : ""} onClick={() => onLanguage("python")}>Python</button><button className={language === "typescript" ? "active" : ""} onClick={() => onLanguage("typescript")}>TypeScript</button></nav>
        <section><h3>1. Install matching CLI and SDK versions</h3>{development && <p className="scope-warning">Connected to a development build. These commands use this repository instead of release downloads.</p>}<pre>{cliInstall}</pre><pre>{sdkInstall}</pre><details><summary>Upgrade or uninstall</summary><p>To upgrade, repeat the verified install commands for the release reported by the connected server.</p><pre>{language === "python" ? "pip uninstall ai-flight-recorder\nsudo rm /usr/local/bin/flight" : "npm uninstall @ai-flight-recorder/sdk\nsudo rm /usr/local/bin/flight"}</pre></details></section>
        {method === "local" ? <section><h3>2. Generate the replay definition</h3><pre>{initFlags}</pre><p>The generated file uses a Dockerfile and build context from this project. <code>flight record --replay-ready</code> resolves the resulting image to a <code>sha256:</code> ID.</p></section> : <section><h3>2. Identify the deployed image and entrypoint</h3><label><span>Immutable deployed image ID</span><input aria-label="Immutable deployed image ID" value={deployedImage} onChange={event => onDeployedImage(event.target.value)} placeholder="sha256:…" spellCheck={false} /></label>{!/^sha256:[a-f0-9]{64}$/.test(deployedImage) && <p className="scope-warning">A deployed replay-ready recording requires the exact 64-character sha256 image ID. Mutable tags are rejected.</p>}<label><span>Container entrypoint JSON argv</span><input aria-label="Container entrypoint JSON argv" value={deployedEntrypoint} onChange={event => onDeployedEntrypoint(event.target.value)} placeholder={'["python","/app/agent.py"]'} spellCheck={false} /></label>{parseArgv(deployedEntrypoint) === null && <p className="scope-warning">Enter a non-empty JSON string array. Flight Recorder does not guess a language or split shell text.</p>}</section>}
        <section><h3>3. Choose the model policy</h3><div className="value-targets" role="radiogroup" aria-label="Model replay policy"><label className={modelMode === "substitute" ? "selected" : ""}><input type="radio" checked={modelMode === "substitute"} onChange={() => onModelMode("substitute")} /><strong>Substitute recorded model output</strong><small>Default. No model host or credential is exposed.</small></label><label className={modelMode === "live" ? "selected" : ""}><input type="radio" checked={modelMode === "live"} onChange={() => onModelMode("live")} /><strong>Allow a declared live model</strong><small>Only the named host and worker credential are available.</small></label></div>{modelMode === "live" && <div className="model-setup-fields"><label><span>Model hostname</span><input value={modelHost} onChange={event => onModelHost(event.target.value)} placeholder="api.openai.com" /></label><label><span>Credential environment name</span><input value={credentialName} onChange={event => onCredentialName(event.target.value)} placeholder="OPENAI_API_KEY" /></label><p>Set <code>FLIGHT_REPLAY_CREDENTIAL_{credentialName || "OPENAI_API_KEY"}</code> on the replay worker. Its value is never sent to this page or stored in a capsule.</p></div>}<label className="replay-ready-choice"><input type="checkbox" checked={modelOverride} onChange={event => onModelOverride(event.target.checked)} /><span><strong>Agent reads FLIGHT_REPLAY_MODEL</strong><small>Only enable this if the effective model is emitted as <code>gen_ai.request.model</code>.</small></span></label></section>
        <section><h3>4. Register the root and wrap every external boundary</h3><pre>{sample}</pre><p>Use stable logical keys. Tool, API, retriever, and MCP calls substitute by default; request edits require a request-aware live LLM callback.</p></section>
        <section><h3>5. Record and confirm readiness</h3><p>{method === "local" ? "The generated command above now includes --replay-ready. Flight Recorder builds the image before starting the agent." : deployedImage && parseArgv(deployedEntrypoint) ? "The generated environment block above now carries the immutable entrypoint metadata. Restart the deployment and record one request." : "Enter the deployed image ID and exact container entrypoint before copying the environment block."}</p></section>
      </>}
      {!replayReady && <p className="guide-note">This run will be labeled inspect only. It can still be viewed and exported, but controlled replay may require recording it again.</p>}
    </div>
  </details>;
}

function pep440Version(version: string): string {
  return version.replace(/-alpha\.(\d+)$/, "a$1").replace(/-beta\.(\d+)$/, "b$1").replace(/-rc\.(\d+)$/, "rc$1");
}

function parseArgv(value: string): string[] | null {
  try {
    const parsed: unknown = JSON.parse(value);
    return Array.isArray(parsed) && parsed.length > 0 && parsed.every(item => typeof item === "string" && item.length > 0) ? parsed : null;
  } catch {
    return null;
  }
}

function pythonReplaySample(modelMode: ModelMode): string {
  return `from flight_recorder import boundary, openai_boundary, register_entrypoint, set_run_provenance

set_run_provenance()
register_entrypoint()

documents = boundary(
    "retriever.policy", request,
    lambda effective: retrieve(effective),
    boundary_class="retriever", side_effect="read_only", replay_policy="substitute",
)

answer = openai_boundary(
    "llm.answer", model_request,
    lambda effective: call_model(effective),
)  # ${modelMode === "live" ? "declared live; preflight checks host + credential" : "recorded output is substituted by default"}

result = boundary(
    "tool.lookup", tool_request,
    lambda effective: lookup(effective),
    boundary_class="tool", side_effect="read_only", replay_policy="substitute",
)`;
}

function typescriptReplaySample(modelMode: ModelMode): string {
  return `import {boundary, openAIBoundary, registerEntrypoint, setRunProvenance} from "@ai-flight-recorder/sdk";

setRunProvenance();
registerEntrypoint();

const documents = await boundary(request, effective => retrieve(effective), {
  logicalKey: "retriever.policy", boundaryClass: "retriever", sideEffect: "read_only", replayPolicy: "substitute",
});

const answer = await openAIBoundary(
  "llm.answer", modelRequest,
  effective => callModel(effective),
); // ${modelMode === "live" ? "declared live; preflight checks host + credential" : "recorded output is substituted by default"}

const result = await boundary(toolRequest, effective => lookup(effective), {
  logicalKey: "tool.lookup", boundaryClass: "tool", sideEffect: "read_only", replayPolicy: "substitute",
});`;
}
