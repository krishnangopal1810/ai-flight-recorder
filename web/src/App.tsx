import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type Capsule, type CapsuleSummary, type Completeness, type Job, type Operation } from "./api";

export function App() {
  const capsules = useQuery({queryKey: ["capsules"], queryFn: api.capsules});
  const initialCapsule = window.location.pathname.match(/^\/capsules\/([^/]+)$/)?.[1];
  const initialJob = window.location.pathname.match(/^\/jobs\/([^/]+)$/)?.[1];
  const [selectedID, setSelectedID] = useState<string | null>(initialCapsule ? decodeURIComponent(initialCapsule) : null);
  const [jobInput, setJobInput] = useState(initialJob ? decodeURIComponent(initialJob) : "");
  const [trackedJob, setTrackedJob] = useState(initialJob ? decodeURIComponent(initialJob) : "");
  const selected = useQuery({queryKey: ["capsule", selectedID], queryFn: () => api.capsule(selectedID!), enabled: Boolean(selectedID)});
  const ready = capsules.data ?? [];

  return <div className="shell">
    <header className="masthead">
      <div className="brand-mark" aria-hidden="true"><span /><span /><span /></div>
      <div><p className="eyebrow">CAUSAL REGRESSION DEBUGGER</p><h1>AI Flight Recorder</h1></div>
      <div className="system-state"><span className="pulse" /> Local evidence store</div>
    </header>
    <main>
      <section className="hero">
        <div><p className="kicker">INCIDENT WORKSPACE</p><h2>Find where the trajectory first changed.</h2><p>Inspect immutable evidence, replay readiness, and logical execution paths without invoking application code.</p></div>
        <div className="hero-stat"><strong>{ready.length}</strong><span>recorded capsules</span></div>
      </section>
	  <form className="job-locator" aria-label="Track replay job" onSubmit={event => {event.preventDefault(); const id = jobInput.trim(); setTrackedJob(id); if (id) history.pushState({}, "", `/jobs/${encodeURIComponent(id)}`);}}>
		<label htmlFor="job-id">Replay job</label><input id="job-id" value={jobInput} onChange={event => setJobInput(event.target.value)} placeholder="Paste a job UUID to inspect progress" /><button type="submit">Track progress</button>
	  </form>
	  {trackedJob && <JobProgress id={trackedJob} />}
      <section className="workspace">
        <aside className="capsule-list" aria-label="Run Capsules">
          <div className="section-heading"><h3>Run Capsules</h3><button onClick={() => capsules.refetch()}>Refresh</button></div>
          {capsules.isLoading && <p className="muted">Loading evidence…</p>}
          {capsules.error && <ErrorPanel message={capsules.error.message} />}
          {!capsules.isLoading && ready.length === 0 && <EmptyState />}
		  {ready.map(item => <CapsuleCard key={item.capsule_id} capsule={item} active={item.capsule_id === selectedID} onClick={() => {setSelectedID(item.capsule_id); history.pushState({}, "", `/capsules/${encodeURIComponent(item.capsule_id)}`);}} />)}
        </aside>
        <section className="evidence" aria-live="polite">
          {!selectedID && <div className="evidence-empty"><div className="radar" /><h3>Select a capsule</h3><p>Playback executes nothing and makes no outbound requests.</p></div>}
          {selected.isLoading && <p className="muted">Verifying capsule integrity…</p>}
          {selected.error && <ErrorPanel message={selected.error.message} />}
		  {selected.data && <CapsuleDetail capsule={selected.data} candidates={ready} />}
        </section>
      </section>
    </main>
  </div>;
}

function CapsuleCard({capsule, active, onClick}: {capsule: CapsuleSummary; active: boolean; onClick: () => void}) {
  const blocked = Object.values(capsule.completeness).filter(item => item.state === "blocked").length;
  return <button className={`capsule-card ${active ? "active" : ""}`} onClick={onClick} aria-pressed={active}>
    <span className="card-top"><span className={`status ${capsule.terminal_status}`}>{capsule.terminal_status}</span><time>{new Date(capsule.start_time).toLocaleString()}</time></span>
    <strong>{capsule.service_name || "unnamed service"}</strong><code>{capsule.capsule_id.slice(0, 20)}…</code>
    <span className="card-bottom"><span>{capsule.project_id || "default"}</span><span>{blocked ? `${blocked} blocked modes` : "replay evidence ready"}</span></span>
  </button>;
}

function CapsuleDetail({capsule, candidates}: {capsule: Capsule; candidates: CapsuleSummary[]}) {
  const roots = useMemo(() => capsule.operations.filter(operation => !operation.parent_span_id), [capsule]);
  const completeness = useQuery({queryKey: ["completeness", capsule.capsule_id], queryFn: () => api.completeness(capsule.capsule_id)});
  return <>
    <div className="detail-header"><div><p className="eyebrow">VERIFIED EVIDENCE</p><h3>{capsule.service_name}</h3><code>{capsule.capsule_id}</code></div><span className="integrity">SHA-256 verified</span></div>
    {completeness.isLoading && <p className="muted">Computing mode-specific prerequisites…</p>}
    {completeness.error && <ErrorPanel message={completeness.error.message} />}
    {completeness.data && <div className="completeness-grid">{(["playback", "replay", "diff", "bisect"] as const).map(mode => <CompletenessCard key={mode} mode={mode} value={completeness.data[mode]} />)}</div>}
    <div className="evidence-labels"><span className="label evidence-label">Evidence</span><p>Recorded values below are immutable capsule facts.</p></div>
	<div className="evidence-stats"><span><strong>{capsule.boundaries?.length ?? 0}</strong> boundaries</span><span><strong>{capsule.artifacts?.length ?? 0}</strong> artifacts</span><span><strong>{capsule.integrity.payload_digests.length}</strong> payloads</span><span><strong>{Object.values(capsule.privacy.redactions ?? {}).reduce((sum, count) => sum + count, 0)}</strong> redactions</span></div>
    <div className="tree" role="tree" aria-label="Logical execution tree">{roots.map(root => <OperationNode key={root.span_id} operation={root} operations={capsule.operations} depth={0} />)}</div>
	<DiffPanel baseline={capsule.capsule_id} candidates={candidates.filter(item => item.capsule_id !== capsule.capsule_id)} />
  </>;
}

function DiffPanel({baseline, candidates}: {baseline: string; candidates: CapsuleSummary[]}) {
	const [candidate, setCandidate] = useState("");
	const comparison = useQuery({queryKey: ["diff", baseline, candidate], queryFn: () => api.diff(baseline, candidate), enabled: Boolean(candidate)});
	return <section className="diff-panel" aria-label="First divergence">
		<div className="section-heading"><div><p className="eyebrow">OBSERVATIONS</p><h3>First divergence</h3></div><select aria-label="Candidate capsule" value={candidate} onChange={event => setCandidate(event.target.value)}><option value="">Choose a candidate…</option>{candidates.map(item => <option key={item.capsule_id} value={item.capsule_id}>{item.service_name} · {item.capsule_id.slice(0, 12)}</option>)}</select></div>
		{comparison.isLoading && <p className="muted">Aligning logical execution paths…</p>}
		{comparison.error && <ErrorPanel message={comparison.error.message} />}
		{comparison.data && !comparison.data.first_divergence_known && <div className="divergence unknown"><span className="label observation-label">Observation</span><strong>First divergence is ambiguous</strong><p>Review the earliest low-confidence alternatives before forming a hypothesis.</p></div>}
		{comparison.data?.first_divergence && <div className="divergence"><span className="label observation-label">Observation</span><strong>{comparison.data.first_divergence.path}</strong><p>{String(comparison.data.first_divergence.before ?? "∅")} → {String(comparison.data.first_divergence.after ?? "∅")}</p><small>{Math.round(comparison.data.first_divergence.confidence * 100)}% alignment confidence · computed, not causal</small></div>}
		{comparison.data && comparison.data.observations.length === 0 && <p className="empty-inline">No replay-relevant execution changes were observed.</p>}
	</section>;
}

function JobProgress({id}: {id: string}) {
	const job = useQuery({queryKey: ["job", id], queryFn: () => api.job(id), refetchInterval: query => terminal((query.state.data as Job | undefined)?.state) ? false : 1000});
	if (job.error) return <ErrorPanel message={job.error.message} />;
	if (!job.data) return <section className="job-progress"><p className="muted">Loading durable job state…</p></section>;
	const progress = job.data.state === "queued" ? 15 : job.data.state === "running" ? 58 : 100;
	return <section className={`job-progress ${job.data.state}`} aria-label="Replay job progress"><div><p className="eyebrow">JOB {id.slice(0, 12)}</p><strong>{job.data.state.replace("_", " ")}</strong>{job.data.error && <small>{job.data.error.code}: {job.data.error.message}</small>}</div><div className="progress-track" aria-label={`${progress}%`}><span style={{width: `${progress}%`}} /></div></section>;
}

function terminal(state?: Job["state"]) { return state === "completed" || state === "failed" || state === "blocked" || state === "inconclusive"; }

function CompletenessCard({mode, value}: {mode: string; value: Completeness}) {
  return <div className={`completeness ${value.state}`} role="group" aria-label={`${mode} completeness`}>
    <span>{mode}</span><strong>{value.state.replace("_", " ")}</strong>
    {value.missing && value.missing.length > 0 && <ul className="missing-prerequisites" aria-label={`${mode} missing prerequisites`}>{value.missing.map((item, index) => <li key={`${item.code}-${item.boundary_id ?? "capsule"}-${index}`}><code>{item.boundary_id ?? "capsule"}</code><span>{item.code}</span><small>{item.message}</small></li>)}</ul>}
    {value.ambiguity_details && value.ambiguity_details.length > 0 && <ul className="ambiguous-prerequisites" aria-label={`${mode} ambiguous prerequisites`}>{value.ambiguity_details.map((item, index) => <li key={`${item.code}-${item.boundary_id ?? "capsule"}-${index}`}><code>{item.boundary_id ?? "capsule"}</code><span>{item.code}</span><small>{item.message}</small></li>)}</ul>}
    {value.ambiguities && value.ambiguities.length > 0 && <ul className="completeness-ambiguities" aria-label={`${mode} ambiguities`}>{value.ambiguities.map(item => <li key={item}>{item}</li>)}</ul>}
  </div>;
}

function OperationNode({operation, operations, depth}: {operation: Operation; operations: Operation[]; depth: number}) {
  const children = operations.filter(item => item.parent_span_id === operation.span_id);
  return <div role="treeitem" aria-level={depth + 1} className="operation" style={{"--depth": depth} as React.CSSProperties}>
    <div className="operation-line"><span className="operation-kind">{operation.kind}</span><strong>{operation.name}</strong>{operation.logical_key && <code>{operation.logical_key}</code>}<span className={`operation-status ${operation.status}`}>{operation.status}</span></div>
    {children.map(child => <OperationNode key={child.span_id} operation={child} operations={operations} depth={depth + 1} />)}
  </div>;
}

function EmptyState() { return <div className="empty"><span>00</span><p>No capsules recorded yet.</p><code>flight record -- &lt;command&gt;</code></div>; }
function ErrorPanel({message}: {message: string}) { return <div role="alert" className="error-panel"><strong>Evidence unavailable</strong><p>{message}</p></div>; }
