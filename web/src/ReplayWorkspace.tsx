import {FormEvent, useEffect, useMemo, useState} from "react";
import {useMutation, useQuery} from "@tanstack/react-query";
import {api, type Boundary, type CreateReplayRequest, type Job, type JobSummary, type Operation, type Playback} from "./api";
import {type ComparisonFocus, JobProgressPanel} from "./JobsWorkspace";
import {InlineError} from "./RunsWorkspace";
import {explainReplayIssue} from "./replayReadiness";

type AdvancedChange = "model" | "image";
type OverrideTarget = "request" | "response";

interface EditableBoundary {
  boundary: Boundary;
  operation: Operation;
}

interface ReplayWorkspaceProps {
  playback: Playback;
  jobs: JobSummary[];
  onJobCreated: (jobID: string) => void;
  onCompare: (candidate: string, focus?: ComparisonFocus) => void;
  onInspectBlocker: (operationID?: string) => void;
  onRecordAgain: () => void;
}

export function ReplayWorkspace({playback, jobs, onJobCreated, onCompare, onInspectBlocker, onRecordAgain}: ReplayWorkspaceProps) {
  const params = new URLSearchParams(window.location.search);
  const editable = useMemo(() => editableBoundaries(playback), [playback]);
  const requestedBoundary = params.get("boundary");
  const initial = editable.find(item => item.boundary.id === requestedBoundary) ?? editable.find(item => item.operation.status !== "ok") ?? editable[0];
  const supportsModelOverride = Boolean(playback.capsule.provenance.entrypoint?.model_override);
  const [mode, setMode] = useState<"value" | "advanced">("value");
  const [advancedType, setAdvancedType] = useState<AdvancedChange>(supportsModelOverride ? "model" : "image");
  const [candidate, setCandidate] = useState("");
  const [selectedBoundaryID, setSelectedBoundaryID] = useState(initial?.boundary.id ?? "");
  const selected = editable.find(item => item.boundary.id === selectedBoundaryID) ?? editable[0];
  const requestedTarget = params.get("edit") === "input" ? "request" : "response";
  const [target, setTarget] = useState<OverrideTarget>(requestedTarget === "request" && selected?.boundary.class === "llm" ? "request" : "response");
  const [draft, setDraft] = useState("");
  const [draftSource, setDraftSource] = useState("");
  const [createdJob, setCreatedJob] = useState<string | null>(params.get("job") ?? jobs.find(job => job.state === "queued" || job.state === "running")?.id ?? null);
  const replay = playback.completeness.replay;
  const staticMissing = [...(replay.missing ?? []), ...(replay.ambiguity_details ?? [])];
  const blockerExplanations = staticMissing.map(item => explainReplayIssue(playback, item));
  const reference = selected ? target === "request" ? selected.operation.input : selected.boundary.response ?? selected.operation.output : undefined;
  const payload = playback.payloads.find(item => item.digest === reference?.digest);
  const recorded = useQuery({
    queryKey: ["experiment-value", playback.capsule.capsule_id, reference?.digest],
    queryFn: () => api.payload(playback.capsule.capsule_id, reference!.digest!),
    enabled: mode === "value" && Boolean(reference?.digest) && payload?.availability === "available",
    retry: false,
  });
  const latestCompleted = useMemo(() => jobs.find(job => job.candidate_capsule_id), [jobs]);
  const jsonError = mode === "value" ? validateJSON(draft) : null;

  useEffect(() => {
    const key = `${selectedBoundaryID}:${target}:${recorded.data ?? ""}`;
    if (recorded.data !== undefined && key !== draftSource) {
      setDraft(prettyJSON(recorded.data));
      setDraftSource(key);
    }
  }, [draftSource, recorded.data, selectedBoundaryID, target]);

  const replayRequest = useMemo<CreateReplayRequest | null>(() => {
    const common = {source_capsule_id: playback.capsule.capsule_id, allow_sandboxed_side_effects: false};
    if (mode === "value") {
      if (!selected || !draft || jsonError || payload?.availability !== "available") return null;
      return {
        ...common,
        substitute_llm: target !== "request",
        overrides: [{boundary_id: selected.boundary.id, target, value: JSON.parse(draft)}],
      };
    }
    if (!candidate.trim()) return null;
    if (advancedType === "model") return {...common, model: candidate.trim(), substitute_llm: false};
    return {...common, candidate_image: candidate.trim(), substitute_llm: true};
  }, [advancedType, candidate, draft, jsonError, mode, payload?.availability, playback.capsule.capsule_id, selected, target]);

  const preflight = useQuery({
    queryKey: ["replay-preflight", replayRequest ? JSON.stringify(replayRequest) : "incomplete"],
    queryFn: () => api.preflightReplay(replayRequest!),
    enabled: Boolean(replayRequest),
    retry: false,
  });

  const create = useMutation({
    mutationFn: () => {
      if (!replayRequest || preflight.data?.state !== "ready") throw new Error("Replay preflight has not passed.");
      return api.createReplay(replayRequest);
    },
    onSuccess: (job: Job) => {
      setCreatedJob(job.id);
      onJobCreated(job.id);
    },
  });

  function chooseBoundary(boundaryID: string) {
    const choice = editable.find(item => item.boundary.id === boundaryID);
    setSelectedBoundaryID(boundaryID);
    const nextTarget = target === "request" && choice?.boundary.class !== "llm" ? "response" : target;
    setTarget(nextTarget);
    setDraft("");
    setDraftSource("");
    const next = new URLSearchParams(window.location.search);
    next.set("boundary", boundaryID);
    if (choice) next.set("operation", choice.operation.span_id);
    next.set("edit", nextTarget === "request" ? "input" : "output");
    next.delete("job");
    history.replaceState({}, "", `${window.location.pathname}?${next}`);
  }

  function chooseTarget(nextTarget: OverrideTarget) {
    setTarget(nextTarget);
    setDraft("");
    setDraftSource("");
    const next = new URLSearchParams(window.location.search);
    next.set("edit", nextTarget === "request" ? "input" : "output");
    next.delete("job");
    history.replaceState({}, "", `${window.location.pathname}?${next}`);
  }

  if (createdJob) return <div className="stage-content reproduce-stage"><JobProgressPanel jobID={createdJob} onCompare={onCompare} /></div>;

  const dynamicIssues = preflight.data?.issues ?? [];
  const imageBlocked = dynamicIssues.some(issue => issue.code === "entrypoint_image_missing" || issue.code === "entrypoint_image_mutable" || issue.code === "candidate_image_mutable");
  const ready = preflight.data?.state === "ready";
  const recordedBoundaries = preflight.data?.effective.substitutions ?? [];
  const liveBoundaries = preflight.data?.effective.live_boundaries ?? [];

  return <div className="stage-content reproduce-stage">
    {replay.state !== "ready" && <section className="replay-blocked" aria-labelledby="replay-blocked-title">
      <span className="large-status" aria-hidden="true">!</span>
      <div><p className="overline">Replay unavailable for this recording</p><h2 id="replay-blocked-title">This run can’t be replayed yet</h2><p>{blockerExplanations[0]?.summary ?? "The recording is safe to inspect, but required replay evidence is missing."}</p></div>
    </section>}
    {blockerExplanations.length > 0 && <section className="blocker-list" aria-label="What prevents replay">
      {blockerExplanations.map((explanation, index) => <article className="blocker-card" key={`${explanation.technicalCode}-${index}`}>
        <header><span>{blockerExplanations.length > 1 ? `Issue ${index + 1}` : "What’s missing"}</span><h3>{explanation.title}</h3></header>
        <div className="blocker-guidance"><section><strong>Why replay needs it</strong><p>{explanation.why}</p></section><section><strong>How to fix it</strong><p>{explanation.fix}</p></section></div>
        <details><summary>Technical details</summary><code>{explanation.technicalCode}</code><p>{explanation.technicalMessage}</p></details>
      </article>)}
      <footer className="blocker-actions"><button className="secondary-button" onClick={() => onInspectBlocker(blockerExplanations[0]?.operationID)}>Review the recorded run</button><button className="secondary-button" onClick={onRecordAgain}>Record another run</button></footer>
    </section>}

    <section className="replay-intro"><div><p className="overline">Step 3 of 5 · Controlled experiment</p><h2>Change one value, then replay the pipeline</h2><p>Every selectable plan is checked against the recorded image, boundary policy, model destinations, credentials, and OCI runtime before Replay is enabled.</p></div>{latestCompleted?.candidate_capsule_id && <button className="secondary-button" onClick={() => onCompare(latestCompleted.candidate_capsule_id!)}>Open latest comparison</button>}</section>
    <div className="experiment-mode"><button className={mode === "value" ? "active" : ""} onClick={() => setMode("value")}><strong>Edit a recorded value</strong><span>Tool output, model output, or model input</span></button><button className={mode === "advanced" ? "active" : ""} onClick={() => setMode("advanced")}><strong>Change code or configuration</strong><span>{supportsModelOverride ? "Declared model or immutable image" : "Immutable candidate image"}</span></button></div>

    <form onSubmit={(event: FormEvent) => {event.preventDefault(); create.mutate();}} className="replay-builder">
      {mode === "value" ? <>
        <section className="builder-step"><div className="step-number">1</div><div className="step-body"><p className="overline">Choose exactly what changes</p><h3>Select an operation and value</h3>{editable.length ? <><label className="candidate-field"><span>Tool or model operation</span><select aria-label="Tool or model operation" value={selected?.boundary.id ?? ""} onChange={event => chooseBoundary(event.target.value)}>{editable.map(item => <option key={item.boundary.id} value={item.boundary.id}>{item.operation.name} · {humanize(item.boundary.class)}</option>)}</select></label><div className="value-targets" role="radiogroup" aria-label="Value to change">{selected?.boundary.class === "llm" && <label className={target === "request" ? "selected" : ""}><input type="radio" name="override-target" checked={target === "request"} onChange={() => chooseTarget("request")} /><strong>Model input</strong><small>Runs this declared model call live with the edited request.</small></label>}<label className={target === "response" ? "selected" : ""}><input type="radio" name="override-target" checked={target === "response"} onChange={() => chooseTarget("response")} /><strong>{selected?.boundary.class === "llm" ? "Model output" : "Tool output"}</strong><small>Substitutes your value and keeps model calls recorded.</small></label></div></> : <p className="scope-warning">This trace has no replay boundaries. Add a Flight boundary around every model, tool, retriever, API, or MCP call, then record it again.</p>}</div></section>
        <section className="builder-step"><div className="step-number">2</div><div className="step-body"><p className="overline">Edit the recorded JSON</p><h3>{selected ? `${selected.operation.name} · ${target === "request" ? "input" : "output"}` : "No editable operation"}</h3>{recorded.isLoading && <p role="status">Loading the verified recorded value…</p>}{recorded.error && <InlineError title="The recorded value could not be loaded" error={recorded.error} />}{reference && payload?.availability !== "available" && <p className="scope-warning">This value is {payload?.availability ?? "unavailable"} and cannot be edited safely.</p>}<label className="json-editor"><span>Candidate JSON value</span><textarea aria-label="Candidate JSON value" value={draft} onChange={event => setDraft(event.target.value)} spellCheck={false} rows={12} /></label>{jsonError && draft && <p className="field-error" role="alert">{jsonError}</p>}<small>The original recording remains immutable. This value is stored only in the replay plan and candidate evidence.</small></div></section>
      </> : <section className="builder-step"><div className="step-number">1</div><div className="step-body"><p className="overline">Advanced candidate</p><h3>Choose a declared model or immutable image</h3><div className="change-options" role="radiogroup" aria-label="Advanced change type">{(supportsModelOverride ? ["model", "image"] as const : ["image"] as const).map(type => <label key={type} className={advancedType === type ? "selected" : ""}><input type="radio" name="change-type" value={type} checked={advancedType === type} onChange={() => {setAdvancedType(type); setCandidate("");}} /><strong>{type === "model" ? "Declared model override" : "Container image"}</strong><small>{type === "model" ? "Uses FLIGHT_REPLAY_MODEL and verifies the captured requested model." : "Uses an immutable sha256 image ID; this is a different-build experiment."}</small></label>)}</div><label className="candidate-field"><span>{advancedType === "model" ? "Candidate model" : "Candidate image digest"}</span><input value={candidate} onChange={event => setCandidate(event.target.value)} required placeholder={advancedType === "model" ? "e.g. gpt-5-mini" : "sha256:…"} /></label>{advancedType === "image" && <p className="capture-helper">For exact-source replay, record again with <code>flight record --replay-ready</code>. Supplying a digest here intentionally tests a different build.</p>}</div></section>}

      <section className="builder-step"><div className="step-number">3</div><div className="step-body"><p className="overline">What runs and what stays recorded</p><h3>Effective replay plan</h3>{preflight.isPending && replayRequest && <p role="status">Checking image, boundaries, model access, credentials, and runtime…</p>}<div className="dependency-groups"><div><strong>{recordedBoundaries.length} substituted {recordedBoundaries.length === 1 ? "boundary" : "boundaries"}</strong><ul>{recordedBoundaries.length ? recordedBoundaries.map(name => <li key={name}><span className="dependency-status recorded">Recorded</span>{name}</li>) : <li>No substitutions resolved yet.</li>}</ul></div><div><strong>{liveBoundaries.length} live {liveBoundaries.length === 1 ? "boundary" : "boundaries"}</strong><ul>{liveBoundaries.length ? liveBoundaries.map(name => <li key={name}><span className="dependency-status live">Live read</span>{name}</li>) : <li>No live destinations.</li>}</ul></div></div>{preflight.data?.effective.allowed_hosts.length ? <p className="capture-helper"><strong>Allowed model hosts:</strong> {preflight.data.effective.allowed_hosts.join(", ")}</p> : null}{preflight.data?.effective.environment_names.length ? <p className="capture-helper"><strong>Worker credential names:</strong> {preflight.data.effective.environment_names.join(", ")}. Values remain on the worker.</p> : null}</div></section>

      <section className="builder-step safety-review"><div className="step-number">4</div><div className="step-body"><p className="overline">Replay preflight</p><h3>{ready ? "Ready for isolated replay" : replayRequest ? "Resolve these setup requirements" : "Complete one candidate change"}</h3>{dynamicIssues.length > 0 ? <ul>{dynamicIssues.map(issue => <li key={`${issue.code}-${issue.boundary_id ?? ""}`}><span>!</span><div><strong>{humanize(issue.code)}</strong><p>{issue.message} {issue.remediation}</p></div></li>)}</ul> : <ul><li><span>{ready ? "✓" : "·"}</span>The immutable image and OCI runtime are available.</li><li><span>{ready ? "✓" : "·"}</span>Every live destination and credential name is declared.</li><li><span>{ready ? "✓" : "·"}</span>Mutating and unknown boundaries remain substituted or sandboxed.</li></ul>}{imageBlocked && <div className="image-recovery"><p><strong>Exact-source replay:</strong> re-record this agent with <code>flight record --replay-ready</code> so the recording carries its built image.</p><p><strong>Different-build experiment:</strong> supply another immutable image and Flight Recorder will label the candidate as a different build.</p><button type="button" className="secondary-button" onClick={() => {setMode("advanced"); setAdvancedType("image");}}>Supply a candidate image</button></div>}{preflight.error && <InlineError title="Replay preflight could not run" error={preflight.error} />}</div></section>
      {create.error && <InlineError title="Replay could not be started" error={create.error} />}
      <footer className="builder-actions"><p>Replay is enabled only after the same preflight used by job creation returns ready.</p><button className="primary-button" type="submit" disabled={!ready || create.isPending}>{create.isPending ? "Starting isolated replay…" : "Replay from this change"}</button></footer>
    </form>
  </div>;
}

function editableBoundaries(playback: Playback): EditableBoundary[] {
  return (playback.capsule.boundaries ?? []).flatMap(boundary => {
    const operation = playback.capsule.operations.find(item => item.span_id === boundary.operation_span_id);
    return operation ? [{boundary, operation}] : [];
  });
}

function validateJSON(value: string): string | null {
  if (!value.trim()) return "Enter one complete JSON value.";
  try { JSON.parse(value); return null; } catch (error) { return error instanceof SyntaxError ? `Invalid JSON: ${error.message}` : "Invalid JSON."; }
}
function prettyJSON(value: string): string { try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; } }
function humanize(value: string) { return value.replaceAll("_", " ").replace(/^./, match => match.toUpperCase()); }
