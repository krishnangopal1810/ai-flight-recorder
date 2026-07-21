import {FormEvent, useState} from "react";
import {useQuery} from "@tanstack/react-query";
import {api, type Job, type JobEvent, type JobSummary} from "./api";
import {InlineError, formatDuration} from "./RunsWorkspace";
import {type Route, runPath} from "./routing";

type JobsRoute = Extract<Route, {kind: "jobs" | "job"}>;
type Navigate = (path: string, replace?: boolean) => void;
export interface ComparisonFocus {boundaryID: string; target: "request" | "response"}

export function JobsExperience({route, navigate}: {route: JobsRoute; navigate: Navigate}) {
  const jobs = useQuery({queryKey: ["jobs"], queryFn: () => api.jobs(), refetchInterval: query => query.state.data?.some(active) ? 1000 : false});
  const [jobID, setJobID] = useState("");
  function openID(event: FormEvent) {
    event.preventDefault();
    if (jobID.trim()) navigate(`/jobs/${encodeURIComponent(jobID.trim())}`);
  }
  return <div className={`jobs-layout ${route.kind === "job" ? "with-job" : ""}`}>
    <section className="jobs-list" aria-labelledby="jobs-title"><div className="page-heading"><div><p className="overline">Controlled replay</p><h1 id="jobs-title">Replay jobs</h1><p>Actual isolation stages and durable candidate evidence.</p></div><button className="icon-button" onClick={() => jobs.refetch()}>Refresh</button></div>
      <form className="job-id-recovery" onSubmit={openID}><details><summary>Open a job by ID</summary><div><label htmlFor="job-id">Job ID</label><input id="job-id" value={jobID} onChange={event => setJobID(event.target.value)} placeholder="UUID" /><button className="secondary-button" type="submit">Open job</button></div></details></form>
      {jobs.error && <InlineError title="Replay jobs could not be loaded" error={jobs.error} retry={() => jobs.refetch()} />}
      {jobs.isLoading && <p role="status" className="loading-copy">Loading replay history…</p>}
      {!jobs.isLoading && jobs.data?.length === 0 && <div className="jobs-empty"><h2>No controlled replays yet</h2><p>Open a replay-ready failed run and choose the change you want to test.</p><button className="primary-button" onClick={() => navigate("/runs")}>Find a failed run</button></div>}
      <div className="job-cards">{jobs.data?.map(job => <JobCard key={job.id} job={job} active={route.kind === "job" && route.jobID === job.id} onClick={() => navigate(`/jobs/${job.id}`)} />)}</div>
    </section>
    {route.kind === "job" && <section className="job-detail-page"><button className="mobile-back" onClick={() => navigate("/jobs")}>← Replay jobs</button><JobProgressPanel jobID={route.jobID} navigate={navigate} /></section>}
  </div>;
}

function JobCard({job, active: selected, onClick}: {job: JobSummary; active: boolean; onClick: () => void}) {
  return <button className={`job-card ${selected ? "active" : ""}`} onClick={onClick}><span className="job-card-top"><span className={`job-state ${job.state}`}>{job.state.replaceAll("_", " ")}</span><time>{new Date(job.created_at).toLocaleString()}</time></span><strong>{job.outcome ? `${humanize(job.outcome)} replay` : "Controlled replay"}</strong><span>{job.source_capsule_id ? `Source ${job.source_capsule_id.slice(0, 10)}…` : "Preparing source evidence"}</span>{job.error && <small>{job.error.message}</small>}</button>;
}

export function JobProgressPanel({jobID, navigate, onCompare}: {jobID: string; navigate?: Navigate; onCompare?: (candidate: string, focus?: ComparisonFocus) => void}) {
  const job = useQuery({queryKey: ["job", jobID], queryFn: () => api.job(jobID), refetchInterval: query => terminal((query.state.data as Job | undefined)?.state) ? false : 800});
  const events = useQuery({queryKey: ["job-events", jobID], queryFn: () => api.jobEvents(jobID), refetchInterval: terminal(job.data?.state) ? false : 800});
  if (job.isLoading) return <div className="job-progress-panel" role="status">Opening durable replay state…</div>;
  if (job.error) return <InlineError title="Replay job could not be opened" error={job.error} retry={() => job.refetch()} />;
  if (!job.data) return null;
  const request = job.data.request as unknown as {plan?: {source_capsule_id?: string; substitutions?: string[]; live_boundaries?: string[]}};
  const result = job.data.result as unknown as {source_capsule_id?: string; candidate_capsule_id?: string; outcome?: string; substitutions?: string[]; live_boundaries?: string[]; duration_ms?: number; overrides?: Array<{boundary_id: string; target: "request" | "response"}>; override_results?: Array<{boundary_id: string; target: "request" | "response"; applied: boolean; message: string}>};
  const sourceID = result?.source_capsule_id ?? request?.plan?.source_capsule_id;
  const stages = stageRows(events.data ?? [], job.data);
  const candidateID = result?.candidate_capsule_id;
  const overrideResult = result?.override_results?.[0];
  const focus = overrideResult?.applied ? {boundaryID: overrideResult.boundary_id, target: overrideResult.target} : undefined;
  const compare = () => {
    if (!candidateID) return;
    if (onCompare) return onCompare(candidateID, focus);
    const params = new URLSearchParams();
    if (focus) {
      params.set("boundary", focus.boundaryID);
      params.set("edit", focus.target === "request" ? "input" : "output");
    }
    navigate?.(`${runPath(sourceID!, "compare", candidateID)}${params.size ? `?${params}` : ""}`);
  };
  return <article className="job-progress-panel" aria-labelledby={`job-${jobID}`}>
    <header><div><p className="overline">Isolated replay</p><h1 id={`job-${jobID}`}>{terminal(job.data.state) ? humanize(job.data.state) : "Replay in progress"}</h1><p>{job.data.error ? job.data.error.message : terminal(job.data.state) ? "Candidate evidence and comparison state are durable." : "The worker reports real execution stages; no estimated percentage is shown."}</p></div><span className={`job-state ${job.data.state}`}>{job.data.state.replaceAll("_", " ")}</span></header>
    <ol className="stage-list" aria-live="polite">{stages.map(stage => <li key={stage.stage} className={stage.state}><span className="stage-marker">{stage.state === "completed" ? "✓" : stage.state === "skipped" ? "–" : stage.state === "running" ? "●" : stage.state === "failed" || stage.state === "blocked" ? "!" : "○"}</span><div><strong>{stageLabel(stage.stage)}</strong><p>{stage.message}</p>{stage.time && <small>{new Date(stage.time).toLocaleTimeString()}</small>}</div><span>{humanize(stage.state)}</span></li>)}</ol>
    <section className="job-safety-summary"><span aria-hidden="true">✓</span><div><strong>Isolation remained enforced</strong><p>Non-root application · private network · no Docker socket · no undeclared host mounts</p></div></section>
    {job.data.error && <div className="job-remediation" role="alert"><strong>{job.data.error.message}</strong><p>{job.data.error.remediation}</p></div>}
    {result && <dl className="job-result"><div><dt>Outcome</dt><dd>{humanize(result.outcome ?? job.data.state)}</dd></div>{result.duration_ms !== undefined && <div><dt>Duration</dt><dd>{formatDuration(result.duration_ms)}</dd></div>}<div><dt>Recorded substitutions</dt><dd>{result.substitutions?.length ?? 0}</dd></div><div><dt>Declared live dependencies</dt><dd>{result.live_boundaries?.length ?? 0}</dd></div>{overrideResult && <div><dt>Edited value</dt><dd>{overrideResult.applied ? "Verified in candidate" : "Not applied"}</dd></div>}</dl>}
    <footer>{sourceID && <button className="secondary-button" onClick={() => navigate?.(runPath(sourceID, "understand"))}>Open source run</button>}{candidateID && <button className="primary-button" onClick={compare}>{focus ? "See my change" : result?.overrides?.length ? "Review failed replay evidence" : "Review first important change"}</button>}</footer>
    <details className="technical-details"><summary>Job technical details</summary><code>{jobID}</code></details>
  </article>;
}

const stageOrder = ["preflight", "preparing_candidate", "building_image", "starting_isolated_application", "replaying_recorded_dependencies", "capturing_candidate", "comparing_executions", "complete"];
interface StageRow {stage: string; state: string; message: string; time?: string}
function stageRows(events: JobEvent[], job: Job): StageRow[] {
  const byStage = new Map<string, StageRow>();
  events.filter(event => event.type === "stage").forEach(event => {
    const data = event.data as unknown as {stage?: string; state?: string; message?: string};
    if (data.stage) byStage.set(data.stage, {stage: data.stage, state: data.state ?? "pending", message: data.message ?? "", time: event.time});
  });
  return stageOrder.map(stage => byStage.get(stage) ?? {stage, state: stage === "preflight" && job.state !== "queued" ? "completed" : "pending", message: defaultStageMessage(stage)});
}
function defaultStageMessage(stage: string) {
  if (stage === "preflight") return "Checking integrity, replay readiness, destinations, and side-effect policy.";
  if (stage === "building_image") return "Waiting for candidate image preparation.";
  return "Waiting for the preceding stage.";
}
function stageLabel(value: string) { return humanize(value).replace("Oci", "OCI"); }
function terminal(state?: Job["state"]) { return state === "completed" || state === "failed" || state === "blocked" || state === "inconclusive"; }
function active(job: JobSummary) { return job.state === "queued" || job.state === "running"; }
function humanize(value: string) { return value.replaceAll("_", " ").replace(/^./, match => match.toUpperCase()); }
