import {useMemo, useState} from "react";
import {useQuery} from "@tanstack/react-query";
import {api, type Boundary, type Capsule, type CapsuleSummary, type Completeness, type Operation, type Playback, type PlaybackPayload} from "./api";

type PayloadRef = NonNullable<Operation["input"]>;
type Evaluation = NonNullable<Capsule["evaluations"]>[number];
type Artifact = NonNullable<Capsule["artifacts"]>[number];

export function CapsuleDetail({playback, candidates}: {playback: Playback; candidates: CapsuleSummary[]}) {
  const {capsule} = playback;
  const roots = useMemo(() => logicalRoots(capsule.operations), [capsule.operations]);
  const available = playback.payloads.filter(payload => payload.availability === "available").length;
  return <>
    <div className="detail-header"><div><p className="eyebrow">VERIFIED EVIDENCE</p><h3>{capsule.service_name}</h3><code>{capsule.capsule_id}</code></div><span className="integrity">Manifest verified · {available}/{playback.payloads.length} payloads available</span></div>
    <div className="passive-proof" role="status" aria-label="Playback isolation proof"><strong>Passive playback</strong><span>{playback.application_processes_started} application processes</span><span>{playback.outbound_requests} outbound requests</span></div>
    {playback.warnings.length > 0 && <section className="playback-warnings" role="alert" aria-label="Playback evidence warnings"><strong>Some recorded values are unavailable</strong><ul>{playback.warnings.map(warning => <li key={`${warning.code}-${warning.digest}`}><span>{warning.code}</span><code>{warning.digest}</code><p>{warning.message}</p><small>{warning.references.join(" · ") || "unattached payload"}</small></li>)}</ul></section>}
    <div className="completeness-grid">{(["playback", "replay", "diff", "bisect"] as const).map(mode => <CompletenessCard key={mode} mode={mode} value={playback.completeness[mode]} />)}</div>
    <div className="evidence-labels"><span className="label evidence-label">Evidence</span><p>Recorded values below are immutable capsule facts. Unverified bytes are never rendered.</p></div>
    <div className="evidence-stats"><span><strong>{capsule.boundaries?.length ?? 0}</strong> boundaries</span><span><strong>{capsule.artifacts?.length ?? 0}</strong> artifacts</span><span><strong>{playback.payloads.length}</strong> payloads</span><span><strong>{playback.warnings.length}</strong> unavailable values</span></div>
    <section className="evidence-section" aria-label="Logical execution evidence"><div className="section-heading"><div><p className="eyebrow">ORIGINAL TRAJECTORY</p><h3>Logical execution tree</h3></div></div><div className="tree" role="tree" aria-label="Logical execution tree">{roots.map(root => <OperationNode key={root.span_id} operation={root} operations={capsule.operations} boundaries={capsule.boundaries ?? []} playback={playback} depth={0} ancestry={new Set()} />)}</div></section>
    <ModelEvidence capsule={capsule} playback={playback} />
    <ArtifactEvidence artifacts={capsule.artifacts ?? []} playback={playback} />
    <EvaluationEvidence evaluations={capsule.evaluations ?? []} playback={playback} />
    <DiffPanel baseline={capsule.capsule_id} candidates={candidates.filter(item => item.capsule_id !== capsule.capsule_id)} />
  </>;
}

function OperationNode({operation, operations, boundaries, playback, depth, ancestry}: {operation: Operation; operations: Operation[]; boundaries: Boundary[]; playback: Playback; depth: number; ancestry: Set<string>}) {
  const nextAncestry = new Set(ancestry).add(operation.span_id);
  const children = operations.filter(item => item.parent_span_id === operation.span_id && !nextAncestry.has(item.span_id));
  const operationBoundaries = boundaries.filter(boundary => boundary.operation_span_id === operation.span_id);
  const usage = Object.entries(operation.attributes ?? {}).filter(([key]) => /(token|cost|usage)/i.test(key));
  return <div role="treeitem" aria-level={depth + 1} className="operation" style={{"--depth": depth} as React.CSSProperties}>
    <div className="operation-line"><span className="operation-kind">{operation.kind}</span><strong>{operation.name}</strong>{operation.logical_key && <code>{operation.logical_key}</code>}<span className="operation-timing">{formatDuration(operation.start_time, operation.end_time)}</span><span className={`operation-status ${operation.status}`}>{operation.status}</span></div>
    <div className="operation-evidence">
      <div className="timing-evidence"><span>Started {formatTimestamp(operation.start_time)}</span><span>Ended {formatTimestamp(operation.end_time)}</span></div>
      <div className="payload-pair"><PayloadEvidence label="Input" reference={operation.input} playback={playback} /><PayloadEvidence label="Output" reference={operation.output} playback={playback} /></div>
      {usage.length > 0 && <dl className="usage-evidence" aria-label={`${operation.name} cost and usage`}>{usage.map(([key, value]) => <div key={key}><dt>{key}</dt><dd>{value}</dd></div>)}</dl>}
      {operationBoundaries.map(boundary => <BoundaryEvidence key={boundary.id} boundary={boundary} playback={playback} />)}
    </div>
    {children.map(child => <OperationNode key={child.span_id} operation={child} operations={operations} boundaries={boundaries} playback={playback} depth={depth + 1} ancestry={nextAncestry} />)}
  </div>;
}

function BoundaryEvidence({boundary, playback}: {boundary: Boundary; playback: Playback}) {
  return <section className="boundary-evidence" aria-label={`Boundary ${boundary.logical_key}`}>
    <div><span className="label policy-label">Boundary policy</span><strong>{boundary.logical_key}</strong><small>{boundary.class} · occurrence {boundary.occurrence}</small></div>
    <dl><div><dt>Replay</dt><dd>{boundary.replay_policy}</dd></div><div><dt>Side effect</dt><dd>{boundary.side_effect}</dd></div><div><dt>Matching</dt><dd>{boundary.matching_rule.mode}</dd></div>{boundary.sandbox && <div><dt>Sandbox</dt><dd>{boundary.sandbox}</dd></div>}</dl>
    {boundary.request_digest && <code className="request-digest">request {boundary.request_digest}</code>}
    {boundary.matching_rule.ignore_paths && boundary.matching_rule.ignore_paths.length > 0 && <small>Ignored pointers: {boundary.matching_rule.ignore_paths.join(", ")}</small>}
    <PayloadEvidence label="Recorded response" reference={boundary.response} playback={playback} />
  </section>;
}

function ModelEvidence({capsule, playback}: {capsule: Capsule; playback: Playback}) {
  const model = capsule.model_configuration;
  if (!model || (!model.provider && !model.requested_model && !model.provider_reported_model && !model.parameters && !model.response_metadata && !model.tool_definitions?.length)) return null;
  return <section className="evidence-section model-evidence" aria-label="Model configuration evidence">
    <div className="section-heading"><div><p className="eyebrow">RECORDED CONFIGURATION</p><h3>Model and cost evidence</h3></div></div>
    <dl className="metadata-grid">{model.provider && <Metadata name="Provider" value={model.provider} />}{model.requested_model && <Metadata name="Requested model" value={model.requested_model} />}{model.provider_reported_model && <Metadata name="Reported model" value={model.provider_reported_model} />}{model.parameters && <Metadata name="Parameters" value={pretty(model.parameters)} />}{model.response_metadata && <Metadata name="Usage / cost" value={pretty(model.response_metadata)} />}</dl>
    {model.tool_definitions?.map((definition, index) => <PayloadEvidence key={`${definition.digest}-${index}`} label={`Tool definition ${index + 1}`} reference={definition} playback={playback} />)}
  </section>;
}

function ArtifactEvidence({artifacts, playback}: {artifacts: Artifact[]; playback: Playback}) {
  if (artifacts.length === 0) return null;
  return <section className="evidence-section" aria-label="Artifact evidence"><div className="section-heading"><div><p className="eyebrow">ATTACHED EVIDENCE</p><h3>Artifacts</h3></div></div><div className="artifact-grid">{artifacts.map(artifact => <article key={artifact.id} className="artifact-card"><div><span className={`payload-state ${artifact.state}`}>{artifact.state}</span><strong>{artifact.logical_name}</strong><small>{artifact.type}{artifact.version ? ` · ${artifact.version}` : ""}</small></div><PayloadEvidence label={artifact.media_type || "Artifact payload"} reference={artifact.payload} playback={playback} /></article>)}</div></section>;
}

function EvaluationEvidence({evaluations, playback}: {evaluations: Evaluation[]; playback: Playback}) {
  if (evaluations.length === 0) return null;
  return <section className="evidence-section" aria-label="Evaluation evidence"><div className="section-heading"><div><p className="eyebrow">RECORDED ASSESSMENT</p><h3>Evaluations</h3></div></div><div className="evaluation-grid">{evaluations.map((evaluation, index) => <article key={`${evaluation.id}-${evaluation.version}-${index}`} className="evaluation-card"><div><strong>{evaluation.id}</strong><code>v{evaluation.version}</code></div><dl>{evaluation.score !== undefined && <Metadata name="Score" value={String(evaluation.score)} />}{evaluation.threshold !== undefined && <Metadata name="Threshold" value={String(evaluation.threshold)} />}{evaluation.result !== undefined && <Metadata name="Result" value={pretty(evaluation.result)} />}</dl>{evaluation.inputs?.map((input, inputIndex) => <PayloadEvidence key={`${input.digest}-${inputIndex}`} label={`Evaluator input ${inputIndex + 1}`} reference={input} playback={playback} />)}<PayloadEvidence label="Evaluator evidence" reference={evaluation.evidence} playback={playback} /></article>)}</div></section>;
}

function PayloadEvidence({label, reference, playback}: {label: string; reference?: PayloadRef; playback: Playback}) {
  if (!reference) return <section className="payload-evidence empty-payload"><div><strong>{label}</strong><span>not recorded</span></div></section>;
  if (reference.state === "omitted") return <section className="payload-evidence omitted"><div><strong>{label}</strong><span>omitted</span></div><p>{reference.reason || "producer omission"} · {formatBytes(reference.size)}</p></section>;
  const payload = playback.payloads.find(candidate => candidate.digest === reference.digest);
  const availability = payload?.availability ?? "unavailable";
  const visibleState = reference.reason === "payload_truncated" && availability === "available" ? "truncated" : availability;
  return <section className={`payload-evidence ${visibleState}`} aria-label={`${label} payload`}>
    <div><strong>{label}</strong><span>{visibleState}</span></div>
    <code>{reference.digest || "digest unavailable"}</code>
    <small>{reference.media_type || payload?.media_type || "application/octet-stream"} · {formatBytes(reference.size)}{reference.original_size ? ` of ${formatBytes(reference.original_size)}` : ""}</small>
    {payload?.preview && <pre>{formatPreview(payload)}</pre>}
    {payload?.preview_truncated && <p>Preview limited to the first 64 KiB; verified full bytes remain available through the payload endpoint.</p>}
    {availability !== "available" && <p>Unavailable recorded value. The rest of the manifest-verified capsule is still inspectable.</p>}
  </section>;
}

function CompletenessCard({mode, value}: {mode: string; value: Completeness}) {
  return <div className={`completeness ${value.state}`} role="group" aria-label={`${mode} completeness`}><span>{mode}</span><strong>{value.state.replace("_", " ")}</strong>{value.missing && value.missing.length > 0 && <ul className="missing-prerequisites" aria-label={`${mode} missing prerequisites`}>{value.missing.map((item, index) => <li key={`${item.code}-${item.boundary_id ?? "capsule"}-${index}`}><code>{item.boundary_id ?? "capsule"}</code><span>{item.code}</span><small>{item.message}</small></li>)}</ul>}{value.ambiguity_details && value.ambiguity_details.length > 0 && <ul className="ambiguous-prerequisites" aria-label={`${mode} ambiguous prerequisites`}>{value.ambiguity_details.map((item, index) => <li key={`${item.code}-${item.boundary_id ?? "capsule"}-${index}`}><code>{item.boundary_id ?? "capsule"}</code><span>{item.code}</span><small>{item.message}</small></li>)}</ul>}{value.ambiguities && value.ambiguities.length > 0 && <ul className="completeness-ambiguities" aria-label={`${mode} ambiguities`}>{value.ambiguities.map(item => <li key={item}>{item}</li>)}</ul>}</div>;
}

function DiffPanel({baseline, candidates}: {baseline: string; candidates: CapsuleSummary[]}) {
  const [candidate, setCandidate] = useState("");
  const comparison = useQuery({queryKey: ["diff", baseline, candidate], queryFn: () => api.diff(baseline, candidate), enabled: Boolean(candidate)});
  return <section className="diff-panel" aria-label="First divergence"><div className="section-heading"><div><p className="eyebrow">OBSERVATIONS</p><h3>First divergence</h3></div><select aria-label="Candidate capsule" value={candidate} onChange={event => setCandidate(event.target.value)}><option value="">Choose a candidate…</option>{candidates.map(item => <option key={item.capsule_id} value={item.capsule_id}>{item.service_name} · {item.capsule_id.slice(0, 12)}</option>)}</select></div>{comparison.isLoading && <p className="muted">Aligning logical execution paths…</p>}{comparison.error && <ErrorPanel message={comparison.error.message} />}{comparison.data && !comparison.data.first_divergence_known && <div className="divergence unknown"><span className="label observation-label">Observation</span><strong>First divergence is ambiguous</strong><p>Review the earliest low-confidence alternatives before forming a hypothesis.</p></div>}{comparison.data?.first_divergence && <div className="divergence"><span className="label observation-label">Observation</span><strong>{comparison.data.first_divergence.path}</strong><p>{String(comparison.data.first_divergence.before ?? "∅")} → {String(comparison.data.first_divergence.after ?? "∅")}</p><small>{Math.round(comparison.data.first_divergence.confidence * 100)}% alignment confidence · computed, not causal</small></div>}{comparison.data && comparison.data.observations.length === 0 && <p className="empty-inline">No replay-relevant execution changes were observed.</p>}</section>;
}

function Metadata({name, value}: {name: string; value: string}) { return <div><dt>{name}</dt><dd>{value}</dd></div>; }
function ErrorPanel({message}: {message: string}) { return <div role="alert" className="error-panel"><strong>Evidence unavailable</strong><p>{message}</p></div>; }

function logicalRoots(operations: Operation[]): Operation[] {
  const ids = new Set(operations.map(operation => operation.span_id));
  const roots = operations.filter(operation => !operation.parent_span_id || !ids.has(operation.parent_span_id));
  return roots.length > 0 ? roots : operations.slice(0, 1);
}

function formatDuration(start: string, end: string): string {
  const milliseconds = Math.max(0, Date.parse(end) - Date.parse(start));
  return milliseconds < 1000 ? `${milliseconds} ms` : `${(milliseconds / 1000).toFixed(3)} s`;
}

function formatTimestamp(value: string): string { return Number.isNaN(Date.parse(value)) ? value : new Date(value).toISOString(); }
function formatBytes(value: number): string { return value < 1024 ? `${value} B` : value < 1024 * 1024 ? `${(value / 1024).toFixed(1)} KiB` : `${(value / (1024 * 1024)).toFixed(1)} MiB`; }

function formatPreview(payload: PlaybackPayload): string {
  if (payload.preview_encoding !== "json") return payload.preview ?? "";
  try { return JSON.stringify(JSON.parse(payload.preview ?? ""), null, 2); } catch { return payload.preview ?? ""; }
}

function pretty(value: unknown): string {
  if (typeof value === "string") return value;
  try { return JSON.stringify(value, null, 2); } catch { return String(value); }
}
