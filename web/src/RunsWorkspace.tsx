import {FormEvent, useEffect, useMemo, useState} from "react";
import {useInfiniteQuery} from "@tanstack/react-query";
import {api, type CapsuleSummary, type RunFilters} from "./api";
import {InvestigationWorkspace} from "./InvestigationWorkspace";
import {type Route, runPath} from "./routing";

type RunsRoute = Extract<Route, {kind: "runs" | "run"}>;
type Navigate = (path: string, replace?: boolean) => void;

export function RunsExperience({route, navigate}: {route: RunsRoute; navigate: Navigate}) {
  const searchParams = new URLSearchParams(window.location.search);
  const [search, setSearch] = useState(searchParams.get("search") ?? "");
  const range = searchParams.get("range") ?? "all";
  const filters = useMemo<RunFilters>(() => ({
    search: searchParams.get("search") ?? undefined,
    project: searchParams.get("project") ?? undefined,
    service: searchParams.get("service") ?? undefined,
    outcome: searchParams.get("outcome") ?? undefined,
    readiness: searchParams.get("readiness") ?? undefined,
    after: range === "all" ? undefined : new Date(Date.now() - Number(range) * 86_400_000).toISOString(),
    order: (searchParams.get("order") as "attention" | "newest" | null) ?? "attention",
    limit: 100,
  }), [window.location.search]);
  const view = searchParams.get("view") ?? "needs-investigation";
  const runs = useInfiniteQuery({queryKey: ["runs", filters], queryFn: ({pageParam}) => api.capsules({...filters, cursor: pageParam}), initialPageParam: undefined as string | undefined, getNextPageParam: page => page.next_cursor});
  const items = useMemo(() => runs.data?.pages.flatMap(page => page.items) ?? [], [runs.data]);
  const visible = useMemo(() => applyView(items, view), [items, view]);
  const selectedID = route.kind === "run" ? route.runID : null;
  const activeFilterCount = [filters.project, filters.service, filters.outcome, filters.readiness, filters.after, filters.order === "newest" ? filters.order : undefined].filter(Boolean).length;

  useEffect(() => {
    if (route.kind !== "runs" || visible.length === 0 || !window.matchMedia("(min-width: 900px)").matches) return;
    navigate(`${runPath(visible[0].capsule_id, "understand")}${window.location.search}`, true);
  }, [route.kind, visible[0]?.capsule_id]);

  function update(values: Record<string, string | undefined>) {
    const next = new URLSearchParams(window.location.search);
    Object.entries(values).forEach(([key, value]) => value ? next.set(key, value) : next.delete(key));
    const base = route.kind === "run" ? runPath(route.runID, route.stage, route.candidateID) : "/runs";
    navigate(`${base}${next.size ? `?${next}` : ""}`, true);
  }
  function submitSearch(event: FormEvent) {
    event.preventDefault();
    update({search: search.trim() || undefined});
  }
  function openRun(id: string) {
    navigate(`${runPath(id, "understand")}${window.location.search}`);
  }

  return <div className={`runs-layout ${route.kind === "run" ? "with-workspace" : "inbox-only"}`}>
    <aside className={`runs-inbox ${route.kind === "run" ? "desktop-sidebar" : ""}`} aria-label="Recorded runs inbox">
      <div className="inbox-heading"><div><p className="overline">Investigation queue</p><h1>Recorded runs</h1><p>Start with a failed run, inspect its execution, then test one change.</p></div><div className="inbox-actions"><button className="secondary-button" onClick={() => navigate("/record")}>Add run</button><button className="icon-button" onClick={() => runs.refetch()} aria-label="Refresh recorded runs">Refresh</button></div></div>
      <form className="run-search" onSubmit={submitSearch}><label htmlFor="run-search">Find a run</label><div><input id="run-search" value={search} onChange={event => setSearch(event.target.value)} placeholder="Search service, project, trace, or run ID" /><button type="submit" aria-label="Search">Search</button></div></form>
      <div className="saved-views" aria-label="Saved views">
        <button className={view === "needs-investigation" ? "active" : ""} onClick={() => update({view: "needs-investigation", outcome: undefined, readiness: undefined})}>Needs investigation</button>
        <button className={view === "replay-blocked" ? "active" : ""} onClick={() => update({view: "replay-blocked", outcome: undefined, readiness: "blocked"})}>Replay blocked</button>
        <button className={view === "ready-compare" ? "active" : ""} onClick={() => update({view: "ready-compare", outcome: undefined, readiness: undefined})}>Ready to compare</button>
        <button className={view === "recently-fixed" ? "active" : ""} onClick={() => update({view: "recently-fixed", outcome: "ok", readiness: undefined})}>Recently fixed</button>
        <button className={view === "all" ? "active" : ""} onClick={() => update({view: "all", outcome: undefined, readiness: undefined})}>All runs</button>
      </div>
      <details className="filter-disclosure">
        <summary><span>Filters</span>{activeFilterCount > 0 && <strong>{activeFilterCount} active</strong>}</summary>
        <div className="run-filters">
          <label><span>Project</span><input key={filters.project ?? "all-projects"} aria-label="Filter by project" defaultValue={filters.project ?? ""} onBlur={event => update({project: event.target.value.trim() || undefined})} placeholder="Any project" /></label>
          <label><span>Service</span><input key={filters.service ?? "all-services"} aria-label="Filter by service" defaultValue={filters.service ?? ""} onBlur={event => update({service: event.target.value.trim() || undefined})} placeholder="Any service" /></label>
          <label><span>Outcome</span><select aria-label="Filter by outcome" value={filters.outcome ?? ""} onChange={event => update({outcome: event.target.value || undefined, view: "all"})}><option value="">Any outcome</option><option value="error">Failed</option><option value="ok">Successful</option></select></label>
          <label><span>Readiness</span><select aria-label="Filter by replay readiness" value={filters.readiness ?? ""} onChange={event => update({readiness: event.target.value || undefined, view: "all"})}><option value="">Any readiness</option><option value="ready">Replay ready</option><option value="blocked">Replay blocked</option><option value="incomplete">Incomplete</option></select></label>
          <label><span>Time</span><select aria-label="Filter by time" value={range} onChange={event => update({range: event.target.value === "all" ? undefined : event.target.value})}><option value="all">Any time</option><option value="1">Past 24 hours</option><option value="7">Past 7 days</option><option value="30">Past 30 days</option></select></label>
          <label><span>Order</span><select aria-label="Sort runs" value={filters.order} onChange={event => update({order: event.target.value})}><option value="attention">Needs attention</option><option value="newest">Newest first</option></select></label>
        </div>
      </details>
      <div className="inbox-count" aria-live="polite">{runs.isLoading ? "Loading recorded runs…" : `${visible.length} ${visible.length === 1 ? "run" : "runs"}`}</div>
      {runs.error && <InlineError title="Runs could not be loaded" error={runs.error} retry={() => runs.refetch()} />}
      {!runs.isLoading && !runs.error && visible.length === 0 && <RunsEmpty filtered={Boolean(filters.search || filters.outcome || filters.readiness || filters.project || filters.service || filters.after)} clear={() => {setSearch(""); navigate("/runs?view=all", true);}} addRun={() => navigate("/record")} />}
      <div className="run-list">{visible.map(run => <RunRow key={run.capsule_id} run={run} active={selectedID === run.capsule_id} onClick={() => openRun(run.capsule_id)} />)}</div>
      {runs.hasNextPage && <button className="load-more" disabled={runs.isFetchingNextPage} onClick={() => runs.fetchNextPage()}>{runs.isFetchingNextPage ? "Loading more runs…" : "Load more runs"}</button>}
    </aside>
    {route.kind === "run" && <InvestigationWorkspace route={route} runs={items} navigate={navigate} />}
  </div>;
}

function RunRow({run, active, onClick}: {run: CapsuleSummary; active: boolean; onClick: () => void}) {
  const failed = run.terminal_status !== "ok" && run.terminal_status !== "unset";
  const readiness = run.replay_readiness === "ready" ? "Replay ready" : run.replay_readiness === "blocked" ? "Replay blocked" : "Replay incomplete";
  return <button className={`run-row ${active ? "active" : ""}`} onClick={onClick} aria-current={active ? "page" : undefined}>
    <span className="run-row-top"><span className={`outcome-badge ${failed ? "failed" : "success"}`}>{failed ? "Failed" : "Successful"}</span><time dateTime={run.start_time}>{relativeTime(run.start_time)}</time></span>
    <strong>{run.service_name || "Unnamed agent"}</strong><span className="run-project">{run.project_id || "default project"}</span>
    <span className="run-failure">{failed ? `Failed at ${run.failure_operation || "recorded operation"}` : "Completed without a recorded error"}</span>
    <span className="run-row-bottom"><span className={`readiness ${run.replay_readiness}`}>{readiness}</span><span>{formatDuration(run.duration_ms)}</span>{run.evidence_warning_count > 0 && <span className="warning-count">{run.evidence_warning_count} evidence {run.evidence_warning_count === 1 ? "warning" : "warnings"}</span>}{run.candidate_replay_count > 0 && <span>{run.candidate_replay_count} {run.candidate_replay_count === 1 ? "candidate" : "candidates"}</span>}</span>
  </button>;
}

function applyView(items: CapsuleSummary[], view: string): CapsuleSummary[] {
  if (view === "needs-investigation") return items.filter(item => item.terminal_status !== "ok" || item.evidence_warning_count > 0);
  if (view === "replay-blocked") return items.filter(item => item.replay_readiness === "blocked" || item.replay_readiness === "incomplete");
  if (view === "ready-compare") return items.filter(item => item.candidate_replay_count > 0 || Boolean(item.source_capsule_id));
  if (view === "recently-fixed") return items.filter(item => item.terminal_status === "ok" && Boolean(item.source_capsule_id));
  return items;
}

function RunsEmpty({filtered, clear, addRun}: {filtered: boolean; clear: () => void; addRun: () => void}) {
  return <section className="runs-empty"><span aria-hidden="true">◎</span><h2>{filtered ? "No runs match these filters" : "No recorded runs yet"}</h2><p>{filtered ? "The evidence store is connected. Broaden the inbox filters to see more runs." : "Add one agent run or existing OTLP trace to begin."}</p>{filtered ? <button className="secondary-button" onClick={clear}>Clear filters</button> : <button className="primary-button" onClick={addRun}>Add your first run</button>}</section>;
}

export function InlineError({title, error, retry}: {title: string; error: Error; retry?: () => void}) {
  return <div className="inline-error" role="alert"><strong>{title}</strong><p>{error.message}</p>{retry && <button className="text-button" onClick={retry}>Try again</button>}</div>;
}

export function formatDuration(milliseconds: number): string {
  if (milliseconds < 1000) return `${milliseconds} ms`;
  if (milliseconds < 60_000) return `${(milliseconds / 1000).toFixed(milliseconds < 10_000 ? 1 : 0)} s`;
  return `${Math.round(milliseconds / 60_000)} min`;
}

function relativeTime(value: string): string {
  const seconds = Math.round((Date.now() - Date.parse(value)) / 1000);
  if (Math.abs(seconds) < 60) return "Just now";
  if (Math.abs(seconds) < 3600) return `${Math.abs(Math.round(seconds / 60))} min ago`;
  if (Math.abs(seconds) < 86_400) return `${Math.abs(Math.round(seconds / 3600))} hr ago`;
  return new Date(value).toLocaleDateString();
}
