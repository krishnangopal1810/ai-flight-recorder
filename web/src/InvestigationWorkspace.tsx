import {useMemo, useState} from "react";
import {useQuery} from "@tanstack/react-query";
import {api, type Boundary, type CapsuleSummary, type Operation} from "./api";
import {CompareWorkspace} from "./CompareWorkspace";
import {EvidenceVault} from "./EvidenceVault";
import {ReplayWorkspace} from "./ReplayWorkspace";
import {type ComparisonFocus} from "./JobsWorkspace";
import {InlineError, formatDuration} from "./RunsWorkspace";
import {TimelineWorkspace} from "./TimelineWorkspace";
import {type Route, type Stage, runPath} from "./routing";
import {explainReplayIssue} from "./replayReadiness";

type RunRoute = Extract<Route, {kind: "run"}>;
type Navigate = (path: string, replace?: boolean) => void;

export function InvestigationWorkspace({route, runs, navigate}: {route: RunRoute; runs: CapsuleSummary[]; navigate: Navigate}) {
  const playback = useQuery({queryKey: ["playback", route.runID], queryFn: () => api.playback(route.runID, false)});
  const jobs = useQuery({queryKey: ["jobs", route.runID], queryFn: () => api.jobs(route.runID), refetchInterval: query => query.state.data?.some(job => job.state === "queued" || job.state === "running") ? 1000 : false});
  const [copied, setCopied] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [exported, setExported] = useState(false);
  const [actionError, setActionError] = useState<Error | null>(null);
  const query = window.location.search;
  const queryParams = new URLSearchParams(query);
  const summary = runs.find(item => item.capsule_id === route.runID);
  const candidateIDs = new Set([
    ...(jobs.data?.map(job => job.candidate_capsule_id).filter((value): value is string => Boolean(value)) ?? []),
    ...runs.filter(item => item.source_capsule_id === route.runID).map(item => item.capsule_id),
    ...(route.candidateID ? [route.candidateID] : []),
  ]);
  const candidates = [...candidateIDs].map(candidateID => runs.find(item => item.capsule_id === candidateID) ?? {capsule_id: candidateID});

  function stagePath(stage: Stage, candidateID?: string) {
    const params = new URLSearchParams(window.location.search);
    if (stage === "understand") {
      params.delete("boundary");
      params.delete("edit");
      params.delete("job");
    } else if (stage === "compare") {
      params.delete("job");
    }
    const suffix = params.toString();
    return `${runPath(route.runID, stage, candidateID)}${suffix ? `?${suffix}` : ""}`;
  }
  function experimentPath(operation: Operation, boundary: Boundary, target: "request" | "response") {
    const params = new URLSearchParams();
    params.set("operation", operation.span_id);
    params.set("boundary", boundary.id);
    params.set("edit", target === "request" ? "input" : "output");
    return `${runPath(route.runID, "reproduce")}?${params}`;
  }
  function comparisonPath(candidateID: string, focus?: ComparisonFocus) {
    const params = new URLSearchParams();
    if (focus) {
      params.set("boundary", focus.boundaryID);
      params.set("edit", focus.target === "request" ? "input" : "output");
    }
    return `${runPath(route.runID, "compare", candidateID)}${params.size ? `?${params}` : ""}`;
  }
  async function copyID() {
    await navigator.clipboard.writeText(route.runID);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }

  if (playback.isLoading) return <section className="investigation-loading" role="status"><span className="loading-orbit" /><div><strong>Opening recorded evidence</strong><p>Verifying the manifest and preparing the failure path…</p></div></section>;
  if (playback.error) return <section className="investigation-workspace"><button className="mobile-back" onClick={() => navigate(`/runs${query}`)}>← Runs</button><InlineError title="This recorded run could not be opened" error={playback.error} retry={() => playback.refetch()} /></section>;
  if (!playback.data) return null;
  const {capsule} = playback.data;
  const failure = firstFailure(capsule.operations);
  const failed = capsule.terminal_status !== "ok" && capsule.terminal_status !== "unset";
  const replay = playback.data.completeness.replay;
  const selectedCandidate = candidates.find(candidate => candidate.capsule_id === route.candidateID);
  const selectedCandidateStatus = selectedCandidate && "terminal_status" in selectedCandidate ? selectedCandidate.terminal_status : undefined;
  const primary = primaryAction(route.stage, replay.state, candidates.length, jobs.data ?? [], selectedCandidateStatus);

  async function downloadRecording() {
    setActionError(null);
    setExporting(true);
    try {
      const date = capsule.start_time.slice(0, 10);
      const service = capsule.service_name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "agent";
      await api.exportCapsule(route.runID, `${service}-${date}.tar.zst`);
      setExported(true);
      window.setTimeout(() => setExported(false), 2000);
    } catch (error) {
      setActionError(error instanceof Error ? error : new Error("Recording download failed."));
    } finally {
      setExporting(false);
    }
  }

  function performPrimaryAction() {
    if (route.stage === "compare" && primary.stage === "compare") {
      void downloadRecording();
      return;
    }
    if (route.stage === "reproduce") {
      document.querySelector(".safety-review, .job-progress-panel, .replay-blocked")?.scrollIntoView({behavior: "smooth", block: "start"});
      return;
    }
    navigate(stagePath(primary.stage, primary.candidateID));
  }

  return <section className="investigation-workspace" aria-label={`Investigation of ${capsule.service_name}`}>
    <button className="mobile-back" onClick={() => navigate(`/runs${query}`)}>← Recorded runs</button>
    <header className="investigation-header">
      <div className="breadcrumb"><button onClick={() => navigate(`/runs${query}`)}>Runs</button><span>/</span><span>{capsule.project_id}</span><span>/</span><strong>{capsule.service_name}</strong></div>
      <div className="run-title-row"><div><span className={`outcome-badge ${failed ? "failed" : "success"}`}>{failed ? "Failed" : "Successful"}</span><h1>{capsule.service_name}</h1></div><div className="run-title-actions">{!(route.stage === "compare" && primary.stage === "compare") && <button className="secondary-button" disabled={exporting} onClick={() => void downloadRecording()}>{exporting ? "Preparing download…" : exported ? "Downloaded" : "Download recording"}</button>}<button className="primary-button" onClick={performPrimaryAction}>{primary.label}</button></div></div>
      <div className="run-metadata"><span>{new Date(capsule.start_time).toLocaleString()}</span><span>{formatDuration(Math.max(0, Date.parse(capsule.end_time) - Date.parse(capsule.start_time)))}</span><span>{capsule.model_configuration?.requested_model || summary?.requested_model || "Model not reported"}</span><button className="copy-id" onClick={copyID}>{copied ? "Copied" : `Run ${route.runID.slice(0, 10)}…`}</button></div>
    </header>
    {actionError && <InlineError title="The recording could not be downloaded" error={actionError} retry={() => void downloadRecording()} />}
    <nav className="stage-navigation five-step" aria-label="Investigation workflow">
      <StageButton index="1" label="Add run" active={false} complete onClick={() => navigate("/record")} />
      <StageButton index="2" label="Inspect trace" active={route.stage === "understand"} complete={route.stage !== "understand"} onClick={() => navigate(stagePath("understand"))} />
      <StageButton index="3" label="Change value" active={route.stage === "reproduce" && !queryParams.get("job")} complete={Boolean(jobs.data?.length)} onClick={() => navigate(stagePath("reproduce"))} />
      <StageButton index="4" label="Replay pipeline" active={route.stage === "reproduce" && Boolean(queryParams.get("job"))} complete={candidateIDs.size > 0} disabled={!jobs.data?.length} onClick={() => {const job = jobs.data?.[0]; if (job) navigate(`${runPath(route.runID, "reproduce")}?job=${encodeURIComponent(job.id)}`);}} />
      <StageButton index="5" label="Compare runs" active={route.stage === "compare"} complete={false} disabled={!candidates.length} onClick={() => navigate(stagePath("compare", route.candidateID ?? candidates[0]?.capsule_id))} />
    </nav>
    {route.stage === "understand" && <Understand playback={playback.data} failure={failure} initialOperationID={queryParams.get("operation") ?? failure?.span_id} candidateID={candidates[0]?.capsule_id} onNext={() => navigate(candidates[0] ? stagePath("compare", candidates[0].capsule_id) : stagePath(replay.state === "ready" ? "reproduce" : "understand"))} onExperiment={(operation, boundary, target) => navigate(experimentPath(operation, boundary, target))} />}
    {route.stage === "reproduce" && <ReplayWorkspace playback={playback.data} jobs={jobs.data ?? []} onJobCreated={jobID => {void jobs.refetch(); const params = new URLSearchParams(window.location.search); params.set("job", jobID); navigate(`${runPath(route.runID, "reproduce")}?${params}`, true);}} onCompare={(candidate, focus) => navigate(comparisonPath(candidate, focus))} onInspectBlocker={operationID => {const params = new URLSearchParams(); if (operationID) params.set("operation", operationID); navigate(`${runPath(route.runID, "understand")}${params.size ? `?${params}` : ""}`);}} onRecordAgain={() => navigate("/record")} />}
    {route.stage === "compare" && <CompareWorkspace baseline={route.runID} candidateID={route.candidateID} candidates={candidates} operations={capsule.operations} boundaries={capsule.boundaries ?? []} focusBoundaryID={queryParams.get("boundary") ?? undefined} focusTarget={queryParams.get("edit") === "input" ? "request" : queryParams.get("edit") === "output" ? "response" : undefined} onCandidate={candidate => navigate(stagePath("compare", candidate), true)} />}
  </section>;
}

function Understand({playback, failure, initialOperationID, candidateID, onNext, onExperiment}: {playback: Awaited<ReturnType<typeof api.playback>>; failure?: Awaited<ReturnType<typeof api.playback>>["capsule"]["operations"][number]; initialOperationID?: string; candidateID?: string; onNext: () => void; onExperiment: (operation: Operation, boundary: Boundary, target: "request" | "response") => void}) {
  const replay = playback.completeness.replay;
  const issueCount = (replay.missing?.length ?? 0) + (replay.ambiguity_details?.length ?? 0) + (replay.ambiguities?.length ?? 0);
  const failed = playback.capsule.terminal_status !== "ok" && playback.capsule.terminal_status !== "unset";
  const action = candidateID ? "Review first important change" : replay.state === "ready" ? "Choose a value to change" : issueCount ? "Review missing evidence" : "Inspect the failure path";
  return <div className="stage-content understand-stage">
    <section className="incident-brief" aria-labelledby="incident-brief-title"><div><p className="overline">Incident brief · Recorded fact</p><h2 id="incident-brief-title">{failed ? `Run failed during ${failure?.name ?? "the recorded trajectory"}` : "Run completed without a recorded error"}</h2><p>{failed ? `${operationKind(failure?.kind)} reported an error after ${failure ? formatDuration(Math.max(0, Date.parse(failure.end_time) - Date.parse(failure.start_time))) : "the recorded execution"}.` : "The recorded operation tree completed successfully."} Historical playback is available. {replay.state === "ready" ? "A controlled replay can safely test one declared change." : `Controlled replay is ${replay.state} because ${issueCount || "one or more"} prerequisites need attention.`}</p></div><div className={`readiness-summary ${replay.state}`}><span>Replay readiness</span><strong>{replay.state === "ready" ? "Ready to reproduce" : `Replay ${replay.state}`}</strong><small>{replay.state === "ready" ? "Recorded dependencies can be frozen." : `${issueCount} ${issueCount === 1 ? "issue" : "issues"} to resolve.`}</small></div></section>
    <section className="next-action"><div className="next-action-index">Next</div><div><p className="overline">Trusted next action</p><h2>{action}</h2><p>{candidateID ? "A controlled candidate is ready. Start with the first aligned change, then inspect its supporting evidence." : replay.state === "ready" ? "Review what changes and what stays frozen before starting an isolated candidate." : "The exact missing prerequisite is shown with the capability it blocks and how to recover."}</p></div><button className="primary-button" onClick={onNext}>{action}</button></section>
    <div className="passive-message" role="status"><span aria-hidden="true">✓</span><strong>Safe to inspect</strong><p>No application code runs and no external requests are made during playback.</p><details><summary>Technical proof</summary><span>{playback.application_processes_started} application processes · {playback.outbound_requests} outbound requests · manifest {playback.manifest_verified ? "verified" : "unverified"}</span></details></div>
    {replay.state !== "ready" && <ReadinessIssues playback={playback} />}
    <TimelineWorkspace playback={playback} initialOperationID={initialOperationID} onExperiment={onExperiment} />
    <EvidenceVault playback={playback} />
  </div>;
}

function ReadinessIssues({playback}: {playback: Awaited<ReturnType<typeof api.playback>>}) {
  const replay = playback.completeness.replay;
  const missing = [...(replay.missing ?? []), ...(replay.ambiguity_details ?? [])];
  const explanations = missing.map(item => explainReplayIssue(playback, item));
  return <section className="readiness-issues" aria-labelledby="readiness-title"><div><p className="overline">Replay unavailable</p><h2 id="readiness-title">This run can’t be replayed yet</h2><p>You can still inspect its timeline and all unaffected recorded evidence.</p></div><ul>{explanations.map((explanation, index) => <li key={`${explanation.technicalCode}-${index}`}><span>{index + 1}</span><div><strong>{explanation.title}</strong><p>{explanation.summary}</p><small>{explanation.fix}</small><details><summary>Technical details</summary><code>{explanation.technicalCode}</code><p>{explanation.technicalMessage}</p></details></div></li>)}</ul><details><summary>View complete capability matrix</summary><dl>{(["playback", "replay", "diff", "bisect"] as const).map(mode => <div key={mode}><dt>{humanize(mode)}</dt><dd>{playback.completeness[mode].state}</dd></div>)}</dl></details></section>;
}

function StageButton({index, label, active, complete, disabled, onClick}: {index: string; label: string; active: boolean; complete: boolean; disabled?: boolean; onClick: () => void}) {
  return <button className={active ? "active" : ""} disabled={disabled} onClick={onClick} aria-current={active ? "step" : undefined}><span>{complete ? "✓" : index}</span><strong>{label}</strong></button>;
}

function firstFailure(operations: Awaited<ReturnType<typeof api.playback>>["capsule"]["operations"]) {
  return [...operations].reverse().find(operation => operation.status !== "ok" && operation.status !== "unset") ?? operations[0];
}

function primaryAction(stage: Stage, readiness: string, candidates: number, jobs: Array<{candidate_capsule_id?: string}>, candidateStatus?: string) {
  const candidateID = jobs.find(job => job.candidate_capsule_id)?.candidate_capsule_id;
  if (stage === "compare" && candidateStatus === "ok") return {label: "Save as regression", stage: "compare" as Stage, candidateID};
  if (stage === "compare") return {label: "Try another change", stage: "reproduce" as Stage};
  if (candidates > 0 && candidateID) return {label: "Review first important change", stage: "compare" as Stage, candidateID};
  if (stage === "reproduce") return {label: "Review replay plan", stage: "reproduce" as Stage};
  if (readiness === "ready") return {label: "Choose a value to change", stage: "reproduce" as Stage};
  return {label: "Review missing evidence", stage: "understand" as Stage};
}

function humanize(value: string) { return value.replaceAll("_", " ").replace(/^./, match => match.toUpperCase()); }
function operationKind(kind?: string) {
  if (kind === "llm") return "The model call";
  if (kind === "tool") return "The tool call";
  if (kind === "retriever") return "The retrieval step";
  if (kind === "agent") return "The agent";
  return "An operation";
}
