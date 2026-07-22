import {useMemo, useState} from "react";
import {useQuery} from "@tanstack/react-query";
import {api, type Boundary, type Operation, type Playback, type TraceLog} from "./api";
import {formatDuration} from "./RunsWorkspace";

type Filter = "all" | "failures" | "external";

export function TimelineWorkspace({playback, initialOperationID, onExperiment}: {playback: Playback; initialOperationID?: string; onExperiment?: (operation: Operation, boundary: Boundary, target: "request" | "response") => void}) {
  const linkedOperation = new URLSearchParams(window.location.search).get("operation");
  const initial = linkedOperation ?? initialOperationID ?? playback.capsule.operations[0]?.span_id;
  const [selectedID, setSelectedID] = useState<string | undefined>(initial);
  const [panelOpen, setPanelOpen] = useState(() => Boolean(linkedOperation) || window.matchMedia("(min-width: 701px)").matches);
  const [expanded, setExpanded] = useState(() => initialExpanded(playback.capsule.operations, initial));
  const [filter, setFilter] = useState<Filter>("all");
  const [search, setSearch] = useState("");
  const tree = useMemo(() => buildTree(playback.capsule.operations), [playback.capsule.operations]);
  const selected = playback.capsule.operations.find(operation => operation.span_id === selectedID) ?? playback.capsule.operations[0];
  const boundaries = (playback.capsule.boundaries ?? []).filter(boundary => boundary.operation_span_id === selected?.span_id);
  const selectedLogs = logsForOperation(playback, selected?.span_id);
  const operationIDs = new Set(playback.capsule.operations.map(operation => operation.span_id));
  const runLogs = (playback.capsule.logs ?? []).filter(record => !record.span_id || !operationIDs.has(record.span_id));

  function select(id: string) {
    setSelectedID(id);
    setPanelOpen(true);
    const params = new URLSearchParams(window.location.search);
    params.set("operation", id);
    history.replaceState({}, "", `${window.location.pathname}?${params}`);
  }
  function toggle(id: string) {
    setExpanded(current => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  }

  return <section className="timeline-section" aria-labelledby="timeline-title">
    <div className="timeline-heading"><div><p className="overline">Supporting evidence</p><h2 id="timeline-title">Execution timeline</h2><p>The failure path is open. Unrelated successful work stays collapsed.</p></div><div className="timeline-jumps"><button onClick={() => initialOperationID && select(initialOperationID)}>Jump to failure</button></div></div>
    <div className="timeline-toolbar"><label><span className="sr-only">Search operations</span><input value={search} onChange={event => setSearch(event.target.value)} placeholder="Search operations" /></label><div role="group" aria-label="Timeline filters"><button className={filter === "all" ? "active" : ""} onClick={() => setFilter("all")}>All</button><button className={filter === "failures" ? "active" : ""} onClick={() => setFilter("failures")}>Failures</button><button className={filter === "external" ? "active" : ""} onClick={() => setFilter("external")}>External calls</button></div></div>
    {runLogs.length > 0 && <LogSection title="Run logs" records={runLogs} playback={playback} origin={playback.capsule.start_time} />}
    <div className="timeline-layout">
      <div className="operation-tree" role="tree" aria-label="Recorded execution timeline">
        {tree.map(node => <OperationRow key={node.operation.span_id} node={node} depth={0} playback={playback} expanded={expanded} selectedID={selectedID} filter={filter} search={search} select={select} toggle={toggle} />)}
      </div>
      {selected && panelOpen && <OperationPanel key={selected.span_id} operation={selected} boundaries={boundaries} logs={selectedLogs} playback={playback} close={() => setPanelOpen(false)} onExperiment={onExperiment} />}
    </div>
  </section>;
}

interface TreeNode {operation: Operation; children: TreeNode[]}

function OperationRow({node, depth, playback, expanded, selectedID, filter, search, select, toggle}: {node: TreeNode; depth: number; playback: Playback; expanded: Set<string>; selectedID?: string; filter: Filter; search: string; select: (id: string) => void; toggle: (id: string) => void}) {
  const {operation} = node;
  const isExpanded = expanded.has(operation.span_id);
  const hasChildren = node.children.length > 0;
  const boundaries = (playback.capsule.boundaries ?? []).filter(boundary => boundary.operation_span_id === operation.span_id);
  const logs = logsForOperation(playback, operation.span_id);
  const failed = operation.status !== "ok" && operation.status !== "unset";
  const logMetadata = logs.map(record => `${record.severity_text ?? ""} ${record.event_name ?? ""} ${record.scope_name ?? ""}`).join(" ");
  const matchesSearch = !search || `${operation.name} ${operation.kind} ${operation.logical_key ?? ""} ${logMetadata}`.toLowerCase().includes(search.toLowerCase());
  const visible = matchesSearch && (filter === "all" || (filter === "failures" && failed) || (filter === "external" && boundaries.length > 0));
  const visibleDescendant = node.children.some(child => branchMatches(child, filter, search, playback));
  if (!visible && !visibleDescendant) return null;
  return <div role="none" className="tree-branch">
    {visible && <div className={`operation-row ${selectedID === operation.span_id ? "selected" : ""} ${failed ? "failed" : ""}`} role="treeitem" aria-level={depth + 1} aria-expanded={hasChildren ? isExpanded : undefined} style={{"--depth": depth} as React.CSSProperties}>
      <button className="tree-toggle" onClick={() => hasChildren && toggle(operation.span_id)} aria-label={hasChildren ? `${isExpanded ? "Collapse" : "Expand"} ${operation.name}` : undefined} tabIndex={hasChildren ? 0 : -1}>{hasChildren ? (isExpanded ? "−" : "+") : "·"}</button>
      <button className="operation-select" onClick={() => select(operation.span_id)}><span className={`operation-icon ${operation.kind}`} aria-hidden="true">{kindIcon(operation.kind)}</span><span className="operation-main"><strong>{operation.name}</strong><small>{operation.kind}{operation.logical_key ? ` · ${operation.logical_key}` : ""}{logs.length ? ` · ${logs.length} ${logs.length === 1 ? "log" : "logs"}` : ""}</small></span><span className="operation-result">{failed ? operation.status || "error" : boundaries.length ? `${boundaries.length} recorded ${boundaries.length === 1 ? "call" : "calls"}` : "Completed"}</span><span className="operation-duration">{formatDuration(Math.max(0, Date.parse(operation.end_time) - Date.parse(operation.start_time)))}</span><span className={`operation-outcome ${failed ? "failed" : "success"}`}>{failed ? "Failed" : "Passed"}</span></button>
    </div>}
    {(isExpanded || !visible) && node.children.map(child => <OperationRow key={child.operation.span_id} node={child} depth={depth + 1} playback={playback} expanded={expanded} selectedID={selectedID} filter={filter} search={search} select={select} toggle={toggle} />)}
  </div>;
}

function OperationPanel({operation, boundaries, logs, playback, close, onExperiment}: {operation: Operation; boundaries: Boundary[]; logs: TraceLog[]; playback: Playback; close: () => void; onExperiment?: (operation: Operation, boundary: Boundary, target: "request" | "response") => void}) {
  const usage = Object.entries(operation.attributes ?? {}).filter(([key]) => /(token|cost|usage)/i.test(key));
  const failed = operation.status !== "ok" && operation.status !== "unset";
  return <aside className="operation-panel" aria-label={`${operation.name} details`}>
    <header><div><p className="overline">Selected operation</p><h3>{operation.name}</h3><span className={`operation-outcome ${failed ? "failed" : "success"}`}>{failed ? "Failed" : "Passed"}</span></div><button className="panel-close" onClick={close} aria-label="Close operation details">×</button></header>
    <section className="operation-summary"><div><span>Type</span><strong>{operation.kind}</strong></div><div><span>Duration</span><strong>{formatDuration(Math.max(0, Date.parse(operation.end_time) - Date.parse(operation.start_time)))}</strong></div><div><span>Started</span><strong>{new Date(operation.start_time).toLocaleTimeString()}</strong></div></section>
    {logs.length > 0 && <LogSection title="Logs" records={logs} playback={playback} origin={operation.start_time} />}
    <PayloadDisclosure label="Input" reference={operation.input} playback={playback} />
    <PayloadDisclosure label="Output" reference={operation.output} playback={playback} />
    {boundaries.map(boundary => <section className="boundary-card" key={boundary.id}><div><span className="semantic-label evidence">Evidence</span><h4>{boundary.logical_key}</h4><p>{boundary.class} dependency · {boundary.side_effect.replaceAll("_", " ")}</p></div>{onExperiment && <div className="experiment-actions"><p>Test a controlled change</p>{boundary.class === "llm" && operation.input && <button className="secondary-button" onClick={() => onExperiment(operation, boundary, "request")}>Change model input</button>}{boundary.response && <button className="secondary-button" onClick={() => onExperiment(operation, boundary, "response")}>Change {boundary.class === "llm" ? "model" : "tool"} output</button>}</div>}<dl><div><dt>Replay policy</dt><dd>{boundary.replay_policy}</dd></div><div><dt>Safety</dt><dd>{boundary.replay_policy === "substitute" ? "Recorded response" : boundary.sandbox ? "Approved sandbox" : "Declared live read"}</dd></div></dl><PayloadDisclosure label="Recorded response" reference={boundary.response} playback={playback} /></section>)}
    {usage.length > 0 && <section className="usage-card"><h4>Cost and usage</h4><dl>{usage.map(([key, value]) => <div key={key}><dt>{key.replace(/^.*\./, "").replaceAll("_", " ")}</dt><dd>{value}</dd></div>)}</dl></section>}
    <details className="technical-details"><summary>Technical details</summary><dl><div><dt>Span ID</dt><dd><code>{operation.span_id}</code></dd></div>{operation.logical_key && <div><dt>Logical key</dt><dd><code>{operation.logical_key}</code></dd></div>}<div><dt>Sequence</dt><dd>{operation.sequence}</dd></div></dl></details>
  </aside>;
}

function LogSection({title, records, playback, origin}: {title: string; records: TraceLog[]; playback: Playback; origin: string}) {
  return <section className="trace-logs" aria-label={title}>
    <header><div><span className="semantic-label evidence">Evidence</span><h4>{title}</h4></div><span>{records.length}</span></header>
    <ol>{records.map(record => <li key={record.id} className={`trace-log severity-${severityClass(record)}`}><div className="trace-log-heading"><strong>{record.severity_text || severityLabel(record.severity_number)}</strong><time dateTime={record.timestamp} title={new Date(record.timestamp).toLocaleString()}>{relativeTime(record.timestamp, origin)}</time></div><p>{record.event_name || record.scope_name || "Application log"}</p>{record.body && <PayloadDisclosure label="Message" reference={record.body} playback={playback} />}{record.attributes && <PayloadDisclosure label="Attributes" reference={record.attributes} playback={playback} />}</li>)}</ol>
  </section>;
}

function severityClass(record: TraceLog) { return record.severity_number >= 17 ? "error" : record.severity_number >= 13 ? "warn" : "info"; }
function severityLabel(value: number) { return value >= 17 ? "Error" : value >= 13 ? "Warning" : value >= 5 ? "Info" : "Debug"; }
function relativeTime(timestamp: string, origin: string) {
  const milliseconds = Math.max(0, Date.parse(timestamp) - Date.parse(origin));
  if (milliseconds < 1000) return `+${milliseconds} ms`;
  if (milliseconds < 60_000) return `+${(milliseconds / 1000).toFixed(milliseconds < 10_000 ? 2 : 1)} s`;
  return `+${(milliseconds / 60_000).toFixed(1)} min`;
}
function logsForOperation(playback: Playback, spanID?: string): TraceLog[] { return spanID ? (playback.capsule.logs ?? []).filter(record => record.span_id === spanID) : []; }

function PayloadDisclosure({label, reference, playback}: {label: string; reference?: NonNullable<Operation["input"]>; playback: Playback}) {
  const [open, setOpen] = useState(false);
  const payload = playback.payloads.find(item => item.digest === reference?.digest);
  const content = useQuery({queryKey: ["payload", playback.capsule.capsule_id, reference?.digest], queryFn: () => api.payload(playback.capsule.capsule_id, reference!.digest!), enabled: open && Boolean(reference?.digest) && payload?.availability === "available", retry: false});
  if (!reference) return <section className="payload-disclosure unavailable"><div><strong>{label}</strong><span>Not recorded</span></div></section>;
  if (reference.state === "omitted") return <section className="payload-disclosure warning"><div><strong>{label}</strong><span>Omitted</span></div><p>{reference.reason?.replaceAll("_", " ") || "Producer omission"}</p></section>;
  const available = payload?.availability === "available";
  return <section className={`payload-disclosure ${available ? "available" : "unavailable"}`}><div><strong>{label}</strong><span>{available ? "Recorded" : payload?.availability?.replaceAll("_", " ") || "Unavailable"}</span></div><p>{reference.media_type || payload?.media_type || "Recorded value"} · {formatBytes(reference.size)}</p>{available && <button className="text-button" onClick={() => setOpen(value => !value)}>{open ? "Hide recorded value" : "View recorded value"}</button>}{!available && <small>Unaffected evidence remains usable.</small>}{content.isLoading && <p role="status">Loading verified value…</p>}{content.error && <p role="alert">This recorded value could not be opened.</p>}{open && content.data && <pre>{prettyPayload(content.data, reference.media_type)}</pre>}<details><summary>Digest</summary><code>{reference.digest}</code></details></section>;
}

function buildTree(operations: Operation[]): TreeNode[] {
  const nodes = new Map(operations.map(operation => [operation.span_id, {operation, children: [] as TreeNode[]}]))
  const roots: TreeNode[] = [];
  nodes.forEach(node => {
    const parent = node.operation.parent_span_id ? nodes.get(node.operation.parent_span_id) : undefined;
    if (parent && parent !== node) parent.children.push(node); else roots.push(node);
  });
  const sort = (items: TreeNode[]) => items.forEach(item => {item.children.sort((a, b) => a.operation.sequence - b.operation.sequence); sort(item.children);});
  sort(roots);
  return roots.sort((a, b) => a.operation.sequence - b.operation.sequence);
}

function initialExpanded(operations: Operation[], selected?: string): Set<string> {
  const parents = new Map(operations.map(operation => [operation.span_id, operation.parent_span_id]));
  const result = new Set<string>();
  let current = selected;
  while (current) {
    result.add(current);
    current = parents.get(current);
  }
  return result;
}

function branchMatches(node: TreeNode, filter: Filter, search: string, playback: Playback): boolean {
  const failed = node.operation.status !== "ok" && node.operation.status !== "unset";
  const external = (playback.capsule.boundaries ?? []).some(boundary => boundary.operation_span_id === node.operation.span_id);
  const logMetadata = logsForOperation(playback, node.operation.span_id).map(record => `${record.severity_text ?? ""} ${record.event_name ?? ""} ${record.scope_name ?? ""}`).join(" ");
  const text = `${node.operation.name} ${node.operation.kind} ${node.operation.logical_key ?? ""} ${logMetadata}`.toLowerCase();
  const matches = (!search || text.includes(search.toLowerCase())) && (filter === "all" || (filter === "failures" && failed) || (filter === "external" && external));
  return matches || node.children.some(child => branchMatches(child, filter, search, playback));
}

function kindIcon(kind: string) {
  if (kind === "llm") return "AI";
  if (kind === "tool") return "T";
  if (kind === "retriever") return "R";
  if (kind === "agent") return "A";
  return "·";
}

function formatBytes(value: number) { return value < 1024 ? `${value} B` : `${(value / 1024).toFixed(1)} KiB`; }
function prettyPayload(value: string, mediaType?: string) {
  if (!mediaType?.includes("json")) return value;
  try { return JSON.stringify(JSON.parse(value), null, 2); } catch { return value; }
}
