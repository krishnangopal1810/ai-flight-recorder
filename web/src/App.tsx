import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type CapsuleSummary, type Job } from "./api";
import { CapsuleDetail } from "./PlaybackInspector";

export function App() {
  const capsules = useQuery({queryKey: ["capsules"], queryFn: api.capsules});
  const initialCapsule = window.location.pathname.match(/^\/capsules\/([^/]+)$/)?.[1];
  const initialJob = window.location.pathname.match(/^\/jobs\/([^/]+)$/)?.[1];
  const [selectedID, setSelectedID] = useState<string | null>(initialCapsule ? decodeURIComponent(initialCapsule) : null);
  const [jobInput, setJobInput] = useState(initialJob ? decodeURIComponent(initialJob) : "");
  const [trackedJob, setTrackedJob] = useState(initialJob ? decodeURIComponent(initialJob) : "");
  const selected = useQuery({queryKey: ["playback", selectedID], queryFn: () => api.playback(selectedID!), enabled: Boolean(selectedID)});
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
	          {selected.isLoading && <p className="muted">Verifying the manifest and inspecting recorded payloads…</p>}
	          {selected.error && <ErrorPanel message={selected.error.message} />}
			  {selected.data && <CapsuleDetail playback={selected.data} candidates={ready} />}
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

function JobProgress({id}: {id: string}) {
	const job = useQuery({queryKey: ["job", id], queryFn: () => api.job(id), refetchInterval: query => terminal((query.state.data as Job | undefined)?.state) ? false : 1000});
	if (job.error) return <ErrorPanel message={job.error.message} />;
	if (!job.data) return <section className="job-progress"><p className="muted">Loading durable job state…</p></section>;
	const progress = job.data.state === "queued" ? 15 : job.data.state === "running" ? 58 : 100;
	return <section className={`job-progress ${job.data.state}`} aria-label="Replay job progress"><div><p className="eyebrow">JOB {id.slice(0, 12)}</p><strong>{job.data.state.replace("_", " ")}</strong>{job.data.error && <small>{job.data.error.code}: {job.data.error.message}</small>}</div><div className="progress-track" aria-label={`${progress}%`}><span style={{width: `${progress}%`}} /></div></section>;
}

function terminal(state?: Job["state"]) { return state === "completed" || state === "failed" || state === "blocked" || state === "inconclusive"; }

function EmptyState() { return <div className="empty"><span>00</span><p>No capsules recorded yet.</p><code>flight record -- &lt;command&gt;</code></div>; }
function ErrorPanel({message}: {message: string}) { return <div role="alert" className="error-panel"><strong>Evidence unavailable</strong><p>{message}</p></div>; }
