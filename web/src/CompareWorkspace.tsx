import {useState} from "react";
import {useQuery} from "@tanstack/react-query";
import {api, type Boundary, type Observation, type Operation} from "./api";
import {InlineError} from "./RunsWorkspace";

interface CandidateOption {capsule_id: string; service_name?: string; terminal_status?: string}

export function CompareWorkspace({baseline, candidateID, candidates, operations, boundaries, focusBoundaryID, focusTarget, onCandidate}: {baseline: string; candidateID?: string; candidates: CandidateOption[]; operations: Operation[]; boundaries: Boundary[]; focusBoundaryID?: string; focusTarget?: "request" | "response"; onCandidate: (candidate: string) => void}) {
  const [copied, setCopied] = useState(false);
  const candidate = candidates.find(item => item.capsule_id === candidateID);
  const candidateDetails = useQuery({queryKey: ["candidate", candidateID], queryFn: () => api.capsule(candidateID!), enabled: Boolean(candidateID) && !candidate?.terminal_status});
  const candidateStatus = candidate?.terminal_status ?? candidateDetails.data?.terminal_status;
  const comparison = useQuery({queryKey: ["diff", baseline, candidateID], queryFn: () => api.diff(baseline, candidateID!), enabled: Boolean(candidateID)});
  async function copyRegression() {
    await navigator.clipboard.writeText(`flight evaluator run --baseline ${baseline} --candidate ${candidateID}`);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  }

  return <div className="stage-content compare-stage">
    <section className="comparison-heading"><div><p className="overline">Step 5 of 5 · Visual comparison</p><h2>What changed after the replay?</h2><p>{focusBoundaryID ? "Your edited value appears first. Any earlier replay difference remains visible as separate evidence." : "Baseline and candidate are aligned by operation. The earliest changed value appears first."}</p></div><label><span>Candidate run</span><select aria-label="Candidate run" value={candidateID ?? ""} onChange={event => onCandidate(event.target.value)}><option value="">Choose a candidate</option>{candidates.map(item => <option key={item.capsule_id} value={item.capsule_id}>{item.service_name || "Candidate replay"} · {item.terminal_status ? item.terminal_status === "ok" ? "successful" : "failed" : "recorded"} · {item.capsule_id.slice(0, 10)}</option>)}</select></label></section>
    {!candidateID && <section className="comparison-empty"><span>↔</span><h3>Choose a candidate run</h3><p>Select a controlled replay or another recorded run to align with this baseline.</p></section>}
    {comparison.isLoading && <section className="comparison-loading" role="status">Aligning the two logical execution paths…</section>}
    {comparison.error && <InlineError title="These runs could not be compared" error={comparison.error} retry={() => comparison.refetch()} />}
    {comparison.data && <ComparisonResult result={comparison.data} candidateStatus={candidateStatus} operations={operations} boundaries={boundaries} focusBoundaryID={focusBoundaryID} focusTarget={focusTarget} />}
    {comparison.data && candidateStatus === "ok" && <section className="resolution-card"><span className="resolution-mark">✓</span><div><p className="overline">Resolution</p><h2>Candidate no longer reproduces the incident</h2><p>The baseline failed and this candidate completed successfully. Preserve the evidence as a regression before closing the investigation.</p><div><button className="primary-button" onClick={() => api.exportCapsule(baseline)}>Save as regression</button><button className="secondary-button" onClick={copyRegression}>{copied ? "CI command copied" : "Copy evaluator / CI command"}</button></div></div></section>}
  </div>;
}

function ComparisonResult({result, candidateStatus, operations, boundaries, focusBoundaryID, focusTarget}: {result: Awaited<ReturnType<typeof api.diff>>; candidateStatus?: string; operations: Operation[]; boundaries: Boundary[]; focusBoundaryID?: string; focusTarget?: "request" | "response"}) {
  const first = result.first_divergence;
  const focusBoundary = boundaries.find(item => item.id === focusBoundaryID);
  const focusField = focusTarget === "request" ? "input" : focusTarget === "response" ? "output" : undefined;
  const focused = focusBoundary && focusField ? result.observations.find(item => observationMatches(item, focusBoundary.operation_span_id, focusField)) : undefined;
  const recordedFirst = first ? result.observations.find(item => sameObservation(item, first)) ?? first : undefined;
  const primary = focused ?? recordedFirst;
  const isFocused = Boolean(focused);
  const downstream = primary ? result.observations.filter(item => !sameObservation(item, primary) && (!first || !sameObservation(item, first))) : result.observations;
  const validatedExplanations = result.hypotheses?.filter(hypothesis => hypothesis.validated) ?? [];
  if (!result.first_divergence_known || !first) return <section className="first-change ambiguous"><span className="semantic-label observation">Observation</span><p className="overline">First important change</p><h2>{result.observations.length ? "The comparison could not identify one reliable first change" : "No replay-relevant change was observed"}</h2><p>{result.observations.length ? "The operations could be matched in more than one way. Review the alternatives in technical details before drawing a conclusion." : "The logical executions are equivalent apart from allowed provenance changes."}</p></section>;
  if (!primary) return null;
  const primaryBefore = primary.before_value ?? primary.before;
  const primaryAfter = primary.after_value ?? primary.after;
  return <>
    <DivergenceGraph primary={primary} first={recordedFirst ?? first} downstream={downstream} operations={operations} alignments={result.alignments} candidateStatus={candidateStatus} focused={isFocused} />
    <section className="primary-value-change" aria-label="Changed fields"><header><div><p className="overline">Changed fields</p><h3>What was different</h3></div><p>Only fields with different recorded values are shown.</p></header><ValueChangeSummary before={primaryBefore} after={primaryAfter} limit={4} technical={`Alignment confidence: ${Math.round(primary.confidence * 100)}% · rule ${primary.rule.replaceAll("_", " ")}`} /></section>
    {isFocused && !sameObservation(first, primary) && <section className="earlier-divergence"><span className="semantic-label observation">Observation</span><div><p className="overline">An earlier difference also occurred</p><h3>{observationLabel(first, operations)}</h3><p>This changed before your edit was applied. Review it separately because the replay did not hold this value constant.</p></div></section>}
    <ComparisonMethod observation={primary} operations={operations} focused={isFocused} baselineID={result.baseline_capsule_id} candidateID={result.candidate_capsule_id} />
    {downstream.length > 0 && <DownstreamChanges observations={downstream} operations={operations} />}
    {validatedExplanations.length > 0 && <section className="hypotheses"><p className="overline">Validated explanation</p>{validatedExplanations.map((hypothesis, index) => <article key={index}><span className="semantic-label hypothesis">Validated</span><strong>{hypothesis.summary}</strong><p>Supported by controlled evidence.</p>{hypothesis.contradicting && hypothesis.contradicting.length > 0 && <small>{hypothesis.contradicting.length} contradicting evidence references remain.</small>}</article>)}</section>}
  </>;
}

function DivergenceGraph({primary, first, downstream, operations, alignments, candidateStatus, focused}: {primary: Observation; first: Observation; downstream: Observation[]; operations: Operation[]; alignments: Awaited<ReturnType<typeof api.diff>>["alignments"]; candidateStatus?: string; focused: boolean}) {
  const observations: Observation[] = [];
  const addObservation = (observation: Observation) => {
    if (!observations.some(item => sameObservation(item, observation))) observations.push(observation);
  };
  if (focused && !sameObservation(first, primary)) addObservation(first);
  addObservation(primary);
  downstream.forEach(addObservation);

  const firstSpanID = observationSpanID(observations[0]);
  const splitOperation = operations.find(item => item.span_id === firstSpanID);
  const changedSpans = new Set(observations.map(observationSpanID));
  const alignedSpans = new Set(alignments.filter(item => item.confidence >= .85 && item.baseline_span).map(item => item.baseline_span));
  const sharedOperations = operations
    .filter(item => item.kind !== "agent" && item.span_id !== firstSpanID && item.sequence < (splitOperation?.sequence ?? Number.MAX_SAFE_INTEGER) && alignedSpans.has(item.span_id) && !changedSpans.has(item.span_id))
    .sort((left, right) => left.sequence - right.sequence)
    .slice(-3);
  const agentOperation = operations.find(item => item.kind === "agent" && !item.parent_span_id) ?? operations.find(item => !item.parent_span_id) ?? operations[0];
  const baselineOutcome = runOutcome(agentOperation?.status);
  const replayOutcome = runOutcome(candidateStatus);

  return <figure className="divergence-graph" aria-labelledby="divergence-graph-title">
    <header><div><p className="overline">Agent execution</p><h3 id="divergence-graph-title">Where the replay split</h3><p>Follow the recorded run and candidate replay from the first changed value.</p></div><div className={`graph-confidence ${confidenceClass(primary.confidence)}`}><strong>{alignmentSummary(primary.confidence)}</strong><span>The comparison uses the corresponding operation in both runs.</span></div></header>
    <div className="agent-context"><span className="agent-context-mark" aria-hidden="true">A</span><div><small>Agent under investigation</small><strong>{agentOperation?.name || "Recorded agent"}</strong></div></div>
    {sharedOperations.length > 0 && <section className="shared-execution" aria-label="Matched steps before the split"><p><span aria-hidden="true">✓</span> Matched before the split</p><ol>{sharedOperations.map(operation => <li key={operation.span_id}><small>{operationKind(operation.kind)}</small><strong>{operation.name}</strong></li>)}</ol></section>}
    <div className="split-caption"><span>{focused ? "The runs differ before and at your edit" : "The runs split here"}</span></div>
    <div className="graph-lane-headings" aria-hidden="true"><span>Recorded run</span><span>Candidate replay</span></div>
    <ol className="graph-changes" aria-label="Changed execution path">{observations.slice(0, 4).map((observation, index) => {
      const before = observation.before_value ?? observation.before;
      const after = observation.after_value ?? observation.after;
      const changes = summarizeValueChanges(before, after);
      const change = changes[0] ?? {path: observation.field || "", before, after};
      const marker = sameObservation(observation, primary) && focused ? "Your edit" : sameObservation(observation, first) ? "First change" : "Later change";
      return <li key={`${observation.path}-${index}`} className={sameObservation(observation, primary) ? "primary" : undefined}>
        <span className="graph-change-marker"><i aria-hidden="true">{index + 1}</i><small>{marker}</small></span>
        <article className="graph-node baseline"><span className="graph-node-lane">Recorded run</span><small>{operationName(observation, operations)} · {changeTypeLabel(observation, operations)}</small><strong>{friendlyField(change.path)}</strong><p>{formatGraphValue(change.before)}</p>{changes.length > 1 && <em>+{changes.length - 1} more changed {changes.length - 1 === 1 ? "field" : "fields"}</em>}</article>
        <article className="graph-node candidate"><span className="graph-node-lane">Candidate replay</span><small>{operationName(observation, operations)} · {changeTypeLabel(observation, operations)}</small><strong>{friendlyField(change.path)}</strong><p>{formatGraphValue(change.after)}</p>{changes.length > 1 && <em>+{changes.length - 1} more changed {changes.length - 1 === 1 ? "field" : "fields"}</em>}</article>
      </li>;
    })}</ol>
    {observations.length > 4 && <p className="graph-overflow">+{observations.length - 4} additional changed {observations.length - 4 === 1 ? "step" : "steps"} in the detailed comparison below.</p>}
    <footer className="graph-outcomes"><div className={baselineOutcome.className}><small>Recorded outcome</small><strong>{baselineOutcome.label}</strong></div><span aria-hidden="true">End</span><div className={replayOutcome.className}><small>Replay outcome</small><strong>{replayOutcome.label}</strong></div></footer>
  </figure>;
}

function DownstreamChanges({observations, operations}: {observations: Observation[]; operations: Operation[]}) {
  return <details className="downstream-changes">
    <summary><span><strong>{observations.length} later {observations.length === 1 ? "effect" : "effects"}</strong><small>Values that changed after the first important change</small></span><span className="downstream-disclosure"><span className="when-closed">Review</span><span className="when-open">Hide</span></span></summary>
    <div className="downstream-intro"><span className="semantic-label observation">Observed</span><p>These values changed later in the replay.</p></div>
    <ol>{observations.map((observation, index) => <DownstreamChange key={`${observation.path}-${index}`} observation={observation} operations={operations} index={index} />)}</ol>
  </details>;
}

function DownstreamChange({observation, operations, index}: {observation: Observation; operations: Operation[]; index: number}) {
  const before = observation.before_value ?? observation.before;
  const after = observation.after_value ?? observation.after;
  return <li className="downstream-change">
    <span className="change-index" aria-hidden="true">{index + 1}</span>
    <article>
      <header><div><p className="change-operation">{operationName(observation, operations)}</p><h4>{changeTypeLabel(observation, operations)}</h4></div><span className={`alignment-summary ${confidenceClass(observation.confidence)}`}>{alignmentSummary(observation.confidence)}</span></header>
      <ValueChangeSummary before={before} after={after} limit={3} technical={`Technical alignment confidence: ${Math.round(observation.confidence * 100)}% · rule ${observation.rule.replaceAll("_", " ")}`} />
    </article>
  </li>;
}

function ValueChangeSummary({before, after, limit, technical}: {before: unknown; after: unknown; limit: number; technical?: string}) {
  const changes = summarizeValueChanges(before, after);
  const visible = changes.slice(0, limit);
  const unchangedCount = comparableFields(before, after).filter(field => !field.changed).length;
  const detailsLabel = unchangedCount ? `Show ${unchangedCount} unchanged ${unchangedCount === 1 ? "field" : "fields"} and technical data` : "Show technical value details";
  return <>
    <dl className="change-facts">{visible.map((change, changeIndex) => <div key={`${change.path}-${changeIndex}`}><dt>{friendlyField(change.path)}</dt><dd><span><small>Baseline</small>{formatSummaryValue(change.before)}</span><i aria-hidden="true">→</i><span><small>Candidate</small>{formatSummaryValue(change.after)}</span></dd></div>)}</dl>
    {changes.length > visible.length && <p className="additional-change-count">And {changes.length - visible.length} more changed {changes.length - visible.length === 1 ? "field" : "fields"} in the complete values.</p>}
    <details className="raw-change"><summary>{detailsLabel}</summary><CompleteValueComparison before={before} after={after} visibleChangeCount={limit} technical={technical} /></details>
  </>;
}

function CompleteValueComparison({before, after, visibleChangeCount, technical}: {before: unknown; after: unknown; visibleChangeCount: number; technical?: string}) {
  const fields = comparableFields(before, after);
  const changed = fields.filter(field => field.changed);
  const additionalChanged = changed.slice(visibleChangeCount);
  const unchanged = fields.filter(field => !field.changed);
  return <div className="complete-value-comparison">
    <header><div><strong>{changed.length} changed {changed.length === 1 ? "field" : "fields"}</strong><span>{unchanged.length} unchanged {unchanged.length === 1 ? "field" : "fields"}</span></div><p>{additionalChanged.length ? "Additional changed fields appear below." : "All changed fields are summarized above."}</p></header>
    {additionalChanged.length > 0 && <div className="complete-field-list" aria-label="Additional changed fields">{additionalChanged.map(field => <ComparableFieldRow key={field.path} field={field} />)}</div>}
    {unchanged.length > 0 && <details className="unchanged-fields"><summary>Show {unchanged.length} unchanged {unchanged.length === 1 ? "field" : "fields"}</summary><div className="complete-field-list">{unchanged.map(field => <ComparableFieldRow key={field.path} field={field} />)}</div></details>}
    <details className="raw-json"><summary>View raw JSON</summary><div><section><span>Recorded run</span><pre>{pretty(before)}</pre></section><section><span>Candidate replay</span><pre>{pretty(after)}</pre></section></div></details>
    {technical && <small className="comparison-technical">{technical}</small>}
  </div>;
}

function ComparisonMethod({observation, operations, focused, baselineID, candidateID}: {observation: Observation; operations: Operation[]; focused: boolean; baselineID: string; candidateID: string}) {
  const operation = operationName(observation, operations);
  const comparedValue = changeTypeLabel(observation, operations).replace(/ changed$/, "");
  return <details className="comparison-method">
    <summary>How were these values matched?</summary>
    <div>
      <h3>{operation} appears in both runs</h3>
      <p>{focused ? "Flight Recorder matched the value you edited to the same operation in the recorded run and replay." : "After matching the two execution paths by operation, this was the earliest recorded value that differed."}</p>
      <dl className="match-summary"><div><dt>Operation</dt><dd>{operation}</dd></div><div><dt>Compared value</dt><dd>{comparedValue}</dd></div><div><dt>Match result</dt><dd>{alignmentSummary(observation.confidence)}</dd></div></dl>
      <details className="match-technical"><summary>Technical matching details</summary><dl><div><dt>Algorithm</dt><dd>{friendlyRule(observation.rule)}</dd></div><div><dt>Confidence score</dt><dd>{Math.round(observation.confidence * 100)}%</dd></div></dl>{observation.evidence.length > 0 && <ul>{observation.evidence.map((evidence, index) => <li key={`${evidence.capsule_id}-${index}`}><span>{evidence.capsule_id === baselineID ? "Recorded run" : evidence.capsule_id === candidateID ? "Candidate replay" : `Reference ${index + 1}`}</span><strong>{evidenceFieldLabel(evidence.field)}</strong><code>{shortID(evidence.capsule_id)}</code></li>)}</ul>}</details>
    </div>
  </details>;
}

function ComparableFieldRow({field}: {field: ComparableField}) {
  return <article className={field.changed ? "changed" : "unchanged"}>
    <header><div><strong>{friendlyField(field.path)}</strong><span>{readablePath(field.path)}</span></div><small>{field.changed ? "Changed" : "Same in both runs"}</small></header>
    {field.changed ? <div className="field-values"><section><span>Recorded run</span><p>{formatDetailedValue(field.before)}</p></section><i aria-hidden="true">→</i><section><span>Candidate replay</span><p>{formatDetailedValue(field.after)}</p></section></div> : <p className="unchanged-value">{formatDetailedValue(field.before)}</p>}
  </article>;
}

function observationMatches(observation: Observation, operationSpanID: string, field: "input" | "output") {
  const normalizedField = observation.field?.replace(/_digest$/, "");
  return observation.path.includes(operationSpanID) && (normalizedField === field || observation.path.endsWith(`.${field}_digest`) || observation.path.endsWith(`.${field}`));
}
function sameObservation(left: Observation, right: Observation) {
  return left.path === right.path && left.field === right.field && left.kind === right.kind;
}
function observationSpanID(observation: Observation) { return observation.path.split(".")[1] || observation.evidence[0]?.span_id || ""; }
function operationKind(kind: string) {
  return ({agent: "Agent", llm: "Model", tool: "Tool", retriever: "Retrieval"} as Record<string, string>)[kind] ?? kind.replaceAll("_", " ");
}
function runOutcome(status?: string) {
  if (["ok", "success", "completed"].includes(status ?? "")) return {label: "Completed", className: "success"};
  if (["error", "failed", "failure"].includes(status ?? "")) return {label: "Failed", className: "failed"};
  return {label: status ? status.replaceAll("_", " ") : "Recorded", className: "neutral"};
}
function friendlyRule(rule: string) {
  const label = rule.replaceAll("_", " ");
  return `${label.replace(/^./, letter => letter.toUpperCase())} comparison`;
}
function evidenceFieldLabel(field?: string) {
  const normalized = field?.replace(/_digest$/, "");
  if (normalized === "input") return "Recorded input";
  if (normalized === "output") return "Recorded output";
  return normalized ? normalized.replaceAll("_", " ").replace(/^./, letter => letter.toUpperCase()) : "Recorded value";
}
function shortID(value: string) { return value.length > 14 ? `${value.slice(0, 14)}…` : value; }
function observationLabel(observation: Observation, operations: Operation[]) {
  const spanID = observation.path.split(".")[1];
  const operation = observation.operation_name || operations.find(item => item.span_id === spanID)?.name;
  const field = (observation.field || observation.path.split(".").at(-1))?.replace(/_digest$/, "");
  return [operation, field?.replaceAll("_", " ")].filter(Boolean).join(" › ") || observation.path.replaceAll(".", " › ").replaceAll("_", " ");
}
function pretty(value: unknown) { try { return JSON.stringify(value, null, 2) ?? "Not recorded"; } catch { return String(value ?? "Not recorded"); } }
function operationName(observation: Observation, operations: Operation[]) {
  const spanID = observation.path.split(".")[1];
  return observation.operation_name || operations.find(item => item.span_id === spanID)?.name || "Recorded operation";
}
function changeTypeLabel(observation: Observation, operations: Operation[]) {
  const spanID = observation.path.split(".")[1];
  const kind = operations.find(item => item.span_id === spanID)?.kind;
  const field = (observation.field || observation.path.split(".").at(-1) || "").replace(/_digest$/, "");
  if (field === "input") {
    if (kind === "llm") return "Model request changed";
    if (kind === "retriever") return "Retrieval request changed";
    if (kind === "tool") return "Tool input changed";
    return "Operation input changed";
  }
  if (kind === "llm") return "Model response changed";
  if (kind === "retriever") return "Retrieved result changed";
  if (kind === "tool") return "Tool response changed";
  if (kind === "agent") return "Final agent result changed";
  return "Operation result changed";
}

const NO_VALUE = Symbol("no recorded value");
type SummaryValue = unknown | typeof NO_VALUE;
interface ValueChange {path: string; before: SummaryValue; after: SummaryValue}
interface ComparableField extends ValueChange {changed: boolean}

function summarizeValueChanges(before: unknown, after: unknown): ValueChange[] {
  return collectValueChanges(before, after).sort((left, right) => fieldPriority(left.path) - fieldPriority(right.path));
}
function collectValueChanges(before: SummaryValue, after: SummaryValue, path = ""): ValueChange[] {
  if (summaryValuesEqual(before, after)) return [];
  if (isRecord(before) && isMissingSummary(after)) return flattenValue(before, path).map(item => ({...item, after: NO_VALUE}));
  if (isRecord(after) && isMissingSummary(before)) return flattenValue(after, path).map(item => ({path: item.path, before: NO_VALUE, after: item.before}));
  if (isRecord(before) && isRecord(after)) {
    const keys = new Set([...Object.keys(before), ...Object.keys(after)]);
    return [...keys].flatMap(key => collectValueChanges(Object.hasOwn(before, key) ? before[key] : NO_VALUE, Object.hasOwn(after, key) ? after[key] : NO_VALUE, path ? `${path}.${key}` : key));
  }
  if (Array.isArray(before) && Array.isArray(after)) {
    return Array.from({length: Math.max(before.length, after.length)}, (_, index) => collectValueChanges(index in before ? before[index] : NO_VALUE, index in after ? after[index] : NO_VALUE, `${path}[${index}]`)).flat();
  }
  if (Array.isArray(before) && isMissingSummary(after)) return flattenValue(before, path).map(item => ({...item, after: NO_VALUE}));
  if (Array.isArray(after) && isMissingSummary(before)) return flattenValue(after, path).map(item => ({path: item.path, before: NO_VALUE, after: item.before}));
  return [{path, before: normalizeMissing(before), after: normalizeMissing(after)}];
}
function flattenValue(value: unknown, path = ""): ValueChange[] {
  if (Array.isArray(value)) return value.flatMap((item, index) => flattenValue(item, `${path}[${index}]`));
  if (isRecord(value)) {
    const entries = Object.entries(value);
    return entries.length ? entries.flatMap(([key, item]) => flattenValue(item, path ? `${path}.${key}` : key)) : [{path, before: value, after: NO_VALUE}];
  }
  return [{path, before: normalizeMissing(value), after: NO_VALUE}];
}
function comparableFields(before: unknown, after: unknown): ComparableField[] {
  const baseline = new Map(flattenComparableValue(before).map(field => [field.path, field.value]));
  const candidate = new Map(flattenComparableValue(after).map(field => [field.path, field.value]));
  const paths = [...new Set([...baseline.keys(), ...candidate.keys()])];
  return paths.map(path => {
    const beforeValue = baseline.has(path) ? baseline.get(path)! : NO_VALUE;
    const afterValue = candidate.has(path) ? candidate.get(path)! : NO_VALUE;
    return {path, before: beforeValue, after: afterValue, changed: !summaryValuesEqual(beforeValue, afterValue)};
  }).sort((left, right) => Number(right.changed) - Number(left.changed) || fieldPriority(left.path) - fieldPriority(right.path) || left.path.localeCompare(right.path));
}
function flattenComparableValue(value: unknown, path = ""): Array<{path: string; value: SummaryValue}> {
  const expanded = embeddedJSON(value);
  if (Array.isArray(expanded)) {
    if (!expanded.length) return [{path: path || "value", value: expanded}];
    return expanded.flatMap((item, index) => flattenComparableValue(item, `${path}[${index}]`));
  }
  if (isRecord(expanded)) {
    const entries = Object.entries(expanded);
    if (!entries.length) return [{path: path || "value", value: expanded}];
    return entries.flatMap(([key, item]) => flattenComparableValue(item, path ? `${path}.${key}` : key));
  }
  return [{path: path || "value", value: normalizeMissing(expanded)}];
}
function embeddedJSON(value: unknown) {
  if (typeof value !== "string") return value;
  const trimmed = value.trim();
  if (!(trimmed.startsWith("{") || trimmed.startsWith("["))) return value;
  try {
    const parsed = JSON.parse(trimmed);
    return typeof parsed === "object" && parsed !== null ? parsed : value;
  } catch {
    return value;
  }
}
function isRecord(value: SummaryValue): value is Record<string, unknown> { return value !== NO_VALUE && typeof value === "object" && value !== null && !Array.isArray(value); }
function isMissingSummary(value: SummaryValue) { return value === NO_VALUE || value === undefined || value === ""; }
function normalizeMissing(value: SummaryValue): SummaryValue { return isMissingSummary(value) ? NO_VALUE : value; }
function summaryValuesEqual(left: SummaryValue, right: SummaryValue) {
  if (left === NO_VALUE || right === NO_VALUE) return left === right;
  try { return JSON.stringify(left) === JSON.stringify(right); } catch { return Object.is(left, right); }
}
function friendlyField(path: string) {
  if (/messages\[0\]\.content$/.test(path)) return "System instruction";
  if (/messages\[1\]\.content$/.test(path)) return "User message";
  const field = path.split(".").at(-1)?.replace(/\[(\d+)\]/g, " item $1").replaceAll("_", " ") || "Recorded value";
  return field.replace(/^./, letter => letter.toUpperCase());
}
function readablePath(path: string) {
  return path.replace(/\[(\d+)\]/g, (_, index) => ` → item ${Number(index) + 1}`).replaceAll(".", " → ").replaceAll("_", " ");
}
function fieldPriority(path: string) {
  if (/messages\[0\]\.content$/.test(path)) return 0;
  const field = path.split(".").at(-1);
  return ({decision: 1, eligible: 2, reason: 3, model: 4} as Record<string, number>)[field ?? ""] ?? 10;
}
function formatSummaryValue(value: SummaryValue) {
  if (value === NO_VALUE || value === undefined || value === "") return "No value recorded";
  if (value === null) return "Null";
  if (typeof value === "boolean") return value ? "Yes" : "No";
  if (typeof value === "string") return value.length > 180 ? `${value.slice(0, 177)}…` : value;
  if (typeof value === "number") return String(value);
  const compact = pretty(value).replaceAll("\n", " ");
  return compact.length > 180 ? `${compact.slice(0, 177)}…` : compact;
}
function formatGraphValue(value: SummaryValue) {
  const summary = formatSummaryValue(value);
  return summary.length > 104 ? `${summary.slice(0, 101)}…` : summary;
}
function formatDetailedValue(value: SummaryValue) {
  const summary = formatSummaryValue(value);
  return summary.length > 320 ? `${summary.slice(0, 317)}…` : summary;
}
function confidenceClass(value: number) { return value >= .85 ? "high" : value >= .6 ? "medium" : "low"; }
function alignmentSummary(value: number) { return value >= .85 ? "Same operation matched" : value >= .6 ? "Likely operation match" : "Operation match is uncertain"; }
