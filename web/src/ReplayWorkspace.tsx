import {FormEvent, useEffect, useMemo, useState} from "react";
import {useMutation, useQuery} from "@tanstack/react-query";
import {api, type Boundary, type Job, type JobSummary, type Operation, type Playback} from "./api";
import {type ComparisonFocus, JobProgressPanel} from "./JobsWorkspace";
import {InlineError} from "./RunsWorkspace";
import {explainReplayIssue} from "./replayReadiness";

type AdvancedChange = "model" | "revision" | "image";
type OverrideTarget = "request" | "response";

interface EditableBoundary {
  boundary: Boundary;
  operation: Operation;
}

export function ReplayWorkspace({playback, jobs, onJobCreated, onCompare, onInspectBlocker, onRecordAgain}: {playback: Playback; jobs: JobSummary[]; onJobCreated: (jobID: string) => void; onCompare: (candidate: string, focus?: ComparisonFocus) => void; onInspectBlocker: (operationID?: string) => void; onRecordAgain: () => void}) {
  const params = new URLSearchParams(window.location.search);
  const editable = useMemo(() => editableBoundaries(playback), [playback]);
  const requestedBoundary = params.get("boundary");
  const initial = editable.find(item => item.boundary.id === requestedBoundary) ?? editable.find(item => item.operation.status !== "ok") ?? editable[0];
  const [mode, setMode] = useState<"value" | "advanced">("value");
  const [advancedType, setAdvancedType] = useState<AdvancedChange>("model");
  const [candidate, setCandidate] = useState("");
  const [selectedBoundaryID, setSelectedBoundaryID] = useState(initial?.boundary.id ?? "");
  const selected = editable.find(item => item.boundary.id === selectedBoundaryID) ?? editable[0];
  const requestedTarget = params.get("edit") === "input" ? "request" : "response";
  const [target, setTarget] = useState<OverrideTarget>(requestedTarget === "request" && selected?.boundary.class === "llm" ? "request" : "response");
  const [draft, setDraft] = useState("");
  const [draftSource, setDraftSource] = useState("");
  const [createdJob, setCreatedJob] = useState<string | null>(params.get("job") ?? jobs.find(job => job.state === "queued" || job.state === "running")?.id ?? null);
  const replay = playback.completeness.replay;
  const missing = [...(replay.missing ?? []), ...(replay.ambiguity_details ?? [])];
  const blockerExplanations = missing.map(item => explainReplayIssue(playback, item));
  const reference = selected ? target === "request" ? selected.operation.input : selected.boundary.response ?? selected.operation.output : undefined;
  const payload = playback.payloads.find(item => item.digest === reference?.digest);
  const recorded = useQuery({queryKey: ["experiment-value", playback.capsule.capsule_id, reference?.digest], queryFn: () => api.payload(playback.capsule.capsule_id, reference!.digest!), enabled: mode === "value" && Boolean(reference?.digest) && payload?.availability === "available", retry: false});
  const latestCompleted = useMemo(() => jobs.find(job => job.candidate_capsule_id), [jobs]);
  const jsonError = mode === "value" ? validateJSON(draft) : null;
  const selectedResponseOverride = mode === "value" && target === "response" ? selected?.boundary.id : undefined;
  const recordedBoundaries = (playback.capsule.boundaries ?? []).filter(boundary => boundary.id !== selectedResponseOverride && boundary.replay_policy !== "live");
  const liveBoundaries = (playback.capsule.boundaries ?? []).filter(boundary => boundary.id !== selectedResponseOverride && boundary.replay_policy === "live");
  const unsafeLive = liveBoundaries.filter(boundary => boundary.side_effect === "mutating" || boundary.side_effect === "unknown");

  useEffect(() => {
    const key = `${selectedBoundaryID}:${target}:${recorded.data ?? ""}`;
    if (recorded.data !== undefined && key !== draftSource) {
      setDraft(prettyJSON(recorded.data));
      setDraftSource(key);
    }
  }, [draftSource, recorded.data, selectedBoundaryID, target]);

  const create = useMutation({mutationFn: () => {
    if (mode === "value") {
      if (!selected || jsonError) throw new Error(jsonError ?? "Choose a recorded tool or model value.");
      return api.createReplay({
        source_capsule_id: playback.capsule.capsule_id,
        substitute_llm: false,
        allow_sandboxed_side_effects: false,
        overrides: [{boundary_id: selected.boundary.id, target, value: JSON.parse(draft)}],
      });
    }
    return api.createReplay({
      source_capsule_id: playback.capsule.capsule_id,
      ...(advancedType === "model" ? {model: candidate} : advancedType === "revision" ? {revision: candidate} : {candidate_image: candidate}),
      substitute_llm: advancedType !== "model",
      allow_sandboxed_side_effects: false,
    });
  }, onSuccess: (job: Job) => {setCreatedJob(job.id); onJobCreated(job.id);}});

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

  if (replay.state !== "ready") {
    const first = blockerExplanations[0];
    return <div className="stage-content reproduce-stage blocked-replay-page">
      <section className="replay-blocked" aria-labelledby="replay-blocked-title">
        <span className="large-status" aria-hidden="true">!</span>
        <div><p className="overline">Replay unavailable for this recording</p><h2 id="replay-blocked-title">This run can’t be replayed yet</h2><p>{first ? first.summary : "The recording is safe to inspect, but it is missing information required for replay."}</p></div>
      </section>
      <section className="blocker-list" aria-label="What prevents replay">
        {blockerExplanations.map((explanation, index) => <article className="blocker-card" key={`${explanation.technicalCode}-${index}`}>
          <header><span>{blockerExplanations.length > 1 ? `Issue ${index + 1}` : "What’s missing"}</span><h3>{explanation.title}</h3></header>
          <div className="blocker-guidance"><section><strong>Why replay needs it</strong><p>{explanation.why}</p></section><section><strong>How to fix it</strong><p>{explanation.fix}</p></section></div>
          <details><summary>Technical details</summary><code>{explanation.technicalCode}</code><p>{explanation.technicalMessage}</p></details>
        </article>)}
      </section>
      <section className="blocked-still-available"><div><span aria-hidden="true">✓</span><div><strong>You can still use this recording</strong><p>Inspect its timeline, view recorded inputs and outputs, and download it for later.</p></div></div></section>
      <footer className="blocker-actions"><button className="primary-button" onClick={() => onInspectBlocker(first?.operationID)}>{first?.operationID ? "Inspect the affected call" : "Review the recorded run"}</button><button className="secondary-button" onClick={onRecordAgain}>Record another run</button></footer>
    </div>;
  }

  if (createdJob) return <div className="stage-content reproduce-stage"><JobProgressPanel jobID={createdJob} onCompare={onCompare} /></div>;

  return <div className="stage-content reproduce-stage">
    <section className="replay-intro"><div><p className="overline">Step 3 of 5 · Controlled experiment</p><h2>Change one value, then replay the pipeline</h2><p>Select a recorded tool or model call. Every other recorded tool response remains frozen.</p></div>{latestCompleted?.candidate_capsule_id && <button className="secondary-button" onClick={() => onCompare(latestCompleted.candidate_capsule_id!)}>Open latest comparison</button>}</section>
    <div className="experiment-mode"><button className={mode === "value" ? "active" : ""} onClick={() => setMode("value")}><strong>Edit a recorded value</strong><span>Tool output, model output, or model input</span></button><button className={mode === "advanced" ? "active" : ""} onClick={() => setMode("advanced")}><strong>Change code or configuration</strong><span>Model variant, Git revision, or image</span></button></div>
    <form onSubmit={(event: FormEvent) => {event.preventDefault(); create.mutate();}} className="replay-builder">
      {mode === "value" ? <>
        <section className="builder-step"><div className="step-number">1</div><div className="step-body"><p className="overline">Choose exactly what changes</p><h3>Select an operation and value</h3>{editable.length ? <><label className="candidate-field"><span>Tool or model operation</span><select aria-label="Tool or model operation" value={selected?.boundary.id ?? ""} onChange={event => chooseBoundary(event.target.value)}>{editable.map(item => <option key={item.boundary.id} value={item.boundary.id}>{item.operation.name} · {humanize(item.boundary.class)}</option>)}</select></label><div className="value-targets" role="radiogroup" aria-label="Value to change">{selected?.boundary.class === "llm" && <label className={target === "request" ? "selected" : ""}><input type="radio" name="override-target" checked={target === "request"} onChange={() => chooseTarget("request")} /><strong>Model input</strong><small>Edit the request sent to this model call.</small></label>}<label className={target === "response" ? "selected" : ""}><input type="radio" name="override-target" checked={target === "response"} onChange={() => chooseTarget("response")} /><strong>{selected?.boundary.class === "llm" ? "Model output" : "Tool output"}</strong><small>Return your value instead of the recorded response.</small></label></div></> : <p className="scope-warning">This trace has no replay boundaries. Add a Flight boundary around the model or tool call, then record it again.</p>}</div></section>
        <section className="builder-step"><div className="step-number">2</div><div className="step-body"><p className="overline">Edit the recorded JSON</p><h3>{selected ? `${selected.operation.name} · ${target === "request" ? "input" : "output"}` : "No editable operation"}</h3>{recorded.isLoading && <p role="status">Loading the verified recorded value…</p>}{recorded.error && <InlineError title="The recorded value could not be loaded" error={recorded.error} />}{reference && payload?.availability !== "available" && <p className="scope-warning">This value is {payload?.availability ?? "unavailable"} and cannot be edited safely.</p>}<label className="json-editor"><span>Candidate JSON value</span><textarea aria-label="Candidate JSON value" value={draft} onChange={event => setDraft(event.target.value)} spellCheck={false} rows={12} /></label>{jsonError && draft && <p className="field-error" role="alert">{jsonError}</p>}<small>The original recording remains immutable. This value is stored only in the replay plan and candidate evidence.</small></div></section>
      </> : <section className="builder-step"><div className="step-number">1</div><div className="step-body"><p className="overline">Advanced candidate</p><h3>Change code or configuration</h3><div className="change-options" role="radiogroup" aria-label="Advanced change type">{(["model", "revision", "image"] as const).map(type => <label key={type} className={advancedType === type ? "selected" : ""}><input type="radio" name="change-type" value={type} checked={advancedType === type} onChange={() => {setAdvancedType(type); setCandidate("");}} /><strong>{type === "model" ? "Model or prompt variant" : type === "revision" ? "Git revision" : "Container image"}</strong><small>{type === "model" ? "Use a declared model configuration." : type === "revision" ? "Use a recorded source revision." : "Use an immutable sha256 image ID."}</small></label>)}</div><label className="candidate-field"><span>{advancedType === "model" ? "Candidate model or prompt variant" : advancedType === "revision" ? "Candidate revision" : "Candidate image digest"}</span><input value={candidate} onChange={event => setCandidate(event.target.value)} required placeholder={advancedType === "model" ? "e.g. prompt-policy-v2" : advancedType === "revision" ? "Git commit SHA" : "sha256:…"} /></label></div></section>}

      <section className="builder-step"><div className="step-number">3</div><div className="step-body"><p className="overline">What runs and what stays recorded</p><h3>Replay plan</h3><div className="dependency-groups"><div><strong>{mode === "value" && selected ? "1 value changed by you" : "1 candidate change"}</strong><ul><li><span className="dependency-status changed">Changed</span>{mode === "value" && selected ? `${selected.operation.name} · ${target === "request" ? "model input" : `${selected.boundary.class} output`}` : `${humanize(advancedType)} · ${candidate || "not entered"}`}</li></ul></div><div><strong>{recordedBoundaries.length} recorded tool {recordedBoundaries.length === 1 ? "response" : "responses"}</strong><ul>{recordedBoundaries.length ? recordedBoundaries.map(boundary => <li key={boundary.id}><span className="dependency-status recorded">Recorded</span>{boundary.logical_key}</li>) : <li><span className="dependency-status recorded">Recorded</span>No other tool responses</li>}</ul></div><div><strong>{mode === "advanced" && advancedType !== "model" ? 0 : liveBoundaries.length} declared live {liveBoundaries.length === 1 ? "call" : "calls"}</strong><ul>{mode === "advanced" && advancedType !== "model" ? <li><span className="dependency-status recorded">Recorded</span>Model responses are frozen</li> : liveBoundaries.map(boundary => <li key={boundary.id}><span className="dependency-status live">Live read</span>{boundary.logical_key}</li>)}</ul></div></div></div></section>
      <section className="builder-step safety-review"><div className="step-number">4</div><div className="step-body"><p className="overline">Replay pipeline</p><h3>{unsafeLive.length ? "This plan is not safe to start" : "Ready for isolated replay"}</h3><ul><li><span>✓</span>The application runs as a non-root user in an isolated container.</li><li><span>✓</span>Recorded tool calls cannot reach their original destinations.</li><li><span>✓</span>No production credentials or Docker socket are provided.</li><li><span>{unsafeLive.length ? "!" : "✓"}</span>No mutating boundary will run live.</li></ul></div></section>
      {create.error && <InlineError title="Replay could not be started" error={create.error} />}
      <footer className="builder-actions"><p>The candidate run and visual comparison are created automatically.</p><button className="primary-button" type="submit" disabled={(mode === "value" ? !selected || !draft || Boolean(jsonError) || payload?.availability !== "available" : !candidate.trim()) || unsafeLive.length > 0 || create.isPending}>{create.isPending ? "Starting isolated replay…" : "Replay from this change"}</button></footer>
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
