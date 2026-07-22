import { context, Span, SpanStatusCode, trace } from "@opentelemetry/api";
import { AsyncLocalStorage } from "node:async_hooks";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import canonicalize from "canonicalize";
import { ensureTelemetry } from "./telemetry.js";

export type SideEffect = "read_only" | "mutating" | "unknown";
export type ReplayPolicy = "substitute" | "live" | "block";

export interface BoundaryOptions {
  logicalKey: string;
  boundaryClass: "llm" | "retriever" | "mcp" | "tool" | "api" | "clock" | "random" | string;
  sideEffect?: SideEffect;
  replayPolicy?: ReplayPolicy;
  sandbox?: string;
  parentLogicalKey?: string;
  ignorePaths?: string[];
}

export interface EntrypointOptions {
	image?: string;
	workingDir?: string;
	dockerfile?: string;
	context?: string;
	modelHosts?: string[];
	environment?: string[];
	sandboxes?: Record<string, string>;
	modelOverride?: boolean;
	inputMode?: string;
	outputMode?: string;
	timeoutSeconds?: number;
	cpuLimit?: number;
	memoryLimitMb?: number;
}

interface RecordedBoundary {
  logical_key: string;
	boundary_class: string;
	parent_logical_key?: string;
  occurrence?: number;
  request_digest: string;
  response_state?: string;
  response?: unknown;
  override_target?: "request" | "response";
  request_override?: unknown;
}

interface ReplayFile {
  boundaries?: RecordedBoundary[];
  live_boundaries?: string[];
  substitutions?: string[];
}

let cachedPlan: { path: string; value: ReplayFile } | undefined;
const occurrences = new Map<string, number>();
const runScope = new AsyncLocalStorage<string>();
const integrationScope = new AsyncLocalStorage<boolean>();
const LIVE = Symbol("flight.live-boundary");

export interface StreamEnvelope {
	schema_version: "1";
	kind: "stream";
	events: Array<{type: string; payload: unknown}>;
	termination: {kind: "completed" | "closed" | "error" | "abandoned"; error_type?: string};
	final?: unknown;
}

export class BoundarySession<R = unknown> {
	readonly plan: ReplayFile | undefined;
	readonly occurrence: number;
	readonly request: R;
	readonly requestOverridden: boolean;
	readonly requestDigest: string;
	private readonly match: RecordedBoundary | undefined;

	constructor(originalRequest: R, readonly options: BoundaryOptions, readonly span?: Span) {
		const sideEffect = options.sideEffect ?? "unknown";
		const replayPolicy = options.replayPolicy ?? "substitute";
		this.plan = loadPlan();
		const occurrenceKey = `${scopeKey()}\u0000${options.parentLogicalKey ?? ""}\u0000${options.boundaryClass}\u0000${options.logicalKey}`;
		this.occurrence = occurrences.get(occurrenceKey) ?? 0;
		occurrences.set(occurrenceKey, this.occurrence + 1);
		const requestOverrides = (this.plan?.boundaries ?? []).filter(item => item.logical_key === options.logicalKey && item.boundary_class === options.boundaryClass && (item.parent_logical_key ?? "") === (options.parentLogicalKey ?? "") && (item.occurrence ?? 0) === this.occurrence && item.override_target === "request" && "request_override" in item);
		if (requestOverrides.length > 1) throw new Error(`unmatched_boundary: ambiguous replay override for ${options.logicalKey}`);
		this.requestOverridden = requestOverrides.length === 1;
		this.request = (this.requestOverridden ? requestOverrides[0]?.request_override : originalRequest) as R;
		let requestDigest = "";
		try { requestDigest = digest(this.request, options.ignorePaths ?? []); }
		catch (error) {
			if (this.plan) throw new Error(`unmatched_boundary: request for ${options.logicalKey} is not canonical JSON`);
			diagnostic("boundary_digest", error);
		}
		this.requestDigest = requestDigest;
		annotate(options, sideEffect, replayPolicy, requestDigest, this.request, span);
		if (this.plan) this.match = matchBoundary(this.plan, options, requestDigest, this.occurrence);
	}

	action(): typeof LIVE | unknown {
		if (!this.plan) return LIVE;
		if (this.match && this.plan.substitutions?.includes(this.options.logicalKey) && (this.match.response_state ?? "captured") === "captured") return this.match.response;
		if ((this.options.replayPolicy ?? "substitute") === "live" && this.plan.live_boundaries?.includes(this.options.logicalKey)) return LIVE;
		throw new Error(`unmatched_boundary: ${this.options.logicalKey} occurrence ${this.occurrence}; live fallback is disabled`);
	}

	capture(response: unknown): void { captureResponse(response, this.span); }
}

export function setRunProvenance(values: {
	projectId?: string;
  repositoryUrl?: string;
  commitSha?: string;
  dirtyDigest?: string;
  buildId?: string;
  runtime?: string;
  dependencyLock?: Record<string, string>;
  featureFlags?: Record<string, unknown>;
} = {}): void {
  safeMetadata("set_run_provenance", () => {
	const projectId = values.projectId ?? process.env.FLIGHT_PROJECT_ID;
	if (!projectId) throw new Error("projectId is required outside flight record");
    const span = trace.getSpan(context.active());
    span?.setAttribute("flight.capture", true);
	span?.setAttribute("flight.project.id", projectId);
    if (values.repositoryUrl) span?.setAttribute("flight.repository.url", values.repositoryUrl);
    if (values.commitSha) span?.setAttribute("flight.git.commit", values.commitSha);
    if (values.dirtyDigest) span?.setAttribute("flight.git.dirty_digest", values.dirtyDigest);
    if (values.buildId) span?.setAttribute("flight.build.id", values.buildId);
    if (values.runtime) span?.setAttribute("flight.runtime", values.runtime);
    if (values.dependencyLock) span?.setAttribute("flight.dependency_lock", canonical(values.dependencyLock));
    if (values.featureFlags) span?.setAttribute("flight.feature_flags", canonical(values.featureFlags));
  });
}

export function registerEntrypoint(command?: string[], options: EntrypointOptions = {}): void {
  safeMetadata("register_entrypoint", () => {
	const defaults = entrypointEnvironment();
	const argv = command ?? stringArray(defaults.command);
	if (!argv.length) throw new Error("entrypoint command is required outside flight record");
	const modelHosts = options.modelHosts ?? stringArray(defaults.model_hosts);
	const environment = options.environment ?? stringArray(defaults.environment);
	const sandboxes = options.sandboxes ?? stringRecord(defaults.sandboxes);
    const span = trace.getSpan(context.active());
	span?.setAttribute("flight.entrypoint.command", JSON.stringify(argv));
	span?.setAttribute("flight.entrypoint.input_mode", options.inputMode ?? stringValue(defaults.input_mode) ?? "stdin_json");
	span?.setAttribute("flight.entrypoint.output_mode", options.outputMode ?? stringValue(defaults.output_mode) ?? "stdout_json");
	const image = options.image ?? stringValue(defaults.image);
	if (image) span?.setAttribute("flight.entrypoint.image", image);
	for (const [key, value] of [
		["flight.entrypoint.working_dir", options.workingDir ?? stringValue(defaults.working_dir)],
		["flight.entrypoint.dockerfile", options.dockerfile ?? stringValue(defaults.dockerfile)],
		["flight.entrypoint.context", options.context ?? stringValue(defaults.context)],
	] as const) if (value) span?.setAttribute(key, value);
	const timeoutNs = options.timeoutSeconds !== undefined ? options.timeoutSeconds * 1_000_000_000 : numberValue(defaults.timeout);
	const cpuLimit = options.cpuLimit ?? numberValue(defaults.cpu_limit);
	const memoryLimitMb = options.memoryLimitMb ?? numberValue(defaults.memory_limit_mb);
	if (timeoutNs) span?.setAttribute("flight.entrypoint.timeout_ns", timeoutNs);
	if (cpuLimit) span?.setAttribute("flight.entrypoint.cpu_limit", cpuLimit);
	if (memoryLimitMb) span?.setAttribute("flight.entrypoint.memory_limit_mb", memoryLimitMb);
	span?.setAttribute("flight.entrypoint.environment", JSON.stringify(environment));
	span?.setAttribute("flight.entrypoint.model_hosts", JSON.stringify(modelHosts));
	span?.setAttribute("flight.entrypoint.model_override", options.modelOverride ?? Boolean(defaults.model_override));
	for (const [name, destination] of Object.entries(sandboxes)) span?.addEvent("flight.sandbox", {"flight.sandbox.name": name, "flight.sandbox.destination": destination});
  });
}

function entrypointEnvironment(): Record<string, unknown> {
	const encoded = process.env.FLIGHT_ENTRYPOINT_JSON;
	if (!encoded) return {};
	const value = JSON.parse(encoded) as {schema_version?: unknown; entrypoint?: unknown};
	if (value.schema_version !== "1" || !value.entrypoint || typeof value.entrypoint !== "object" || Array.isArray(value.entrypoint)) throw new Error("unsupported FLIGHT_ENTRYPOINT_JSON");
	return value.entrypoint as Record<string, unknown>;
}

function stringArray(value: unknown): string[] { return Array.isArray(value) && value.every(item => typeof item === "string") ? value : []; }
function stringRecord(value: unknown): Record<string, string> { return value && typeof value === "object" && !Array.isArray(value) && Object.values(value).every(item => typeof item === "string") ? value as Record<string, string> : {}; }
function stringValue(value: unknown): string | undefined { return typeof value === "string" ? value : undefined; }
function numberValue(value: unknown): number | undefined { return typeof value === "number" ? value : undefined; }

export function attachArtifact(input: {logicalName: string; type: string; version: string; content: Uint8Array; mediaType: string}): string {
  const digest = `sha256:${createHash("sha256").update(input.content).digest("hex")}`;
  safeMetadata("attach_artifact", () => trace.getSpan(context.active())?.addEvent("flight.artifact", {
    "flight.artifact.logical_name": input.logicalName,
    "flight.artifact.type": input.type,
    "flight.artifact.version": input.version,
    "flight.artifact.digest": digest,
    "flight.artifact.media_type": input.mediaType,
    "flight.artifact.content_base64": Buffer.from(input.content).toString("base64"),
  }));
  return digest;
}

export function declareSandbox(name: string, destination: string): void {
	safeMetadata("declare_sandbox", () => trace.getSpan(context.active())?.addEvent("flight.sandbox", {
		"flight.sandbox.name": name,
		"flight.sandbox.destination": destination,
	}));
}

export function recordEnvironment(names: string[]): void {
	safeMetadata("record_environment", () => {
		const span = trace.getSpan(context.active());
		for (const name of names) {
			const value = process.env[name];
			if (value !== undefined) span?.setAttribute(`flight.environment.${name}.digest`, createHash("sha256").update(value).digest("hex"));
		}
	});
}

export async function boundary<T, R = unknown>(request: R, call: (() => T | Promise<T>) | ((effectiveRequest: R) => T | Promise<T>), options: BoundaryOptions): Promise<T> {
	const session = new BoundarySession(request, options);
	const action = session.action();
	const response = action === LIVE ? await invokeBoundary(call, session.request, session.requestOverridden) : action as T;
	session.capture(response);
	return response;
}

export function boundaryStream<T, R = unknown>(
	request: R,
	call: (() => AsyncIterable<T> | Promise<AsyncIterable<T>>) | ((effectiveRequest: R) => AsyncIterable<T> | Promise<AsyncIterable<T>>),
	options: BoundaryOptions,
	codec: {encode?: (event: T) => unknown; decode?: (event: unknown) => T; final?: () => unknown} = {},
): AsyncIterable<T> {
	const session = new BoundarySession(request, options);
	const encode = codec.encode ?? (value => value);
	const decode = codec.decode ?? (value => value as T);
	return {
		async *[Symbol.asyncIterator](): AsyncIterator<T> {
			const action = session.action();
			if (action !== LIVE) {
				const envelope = parseStreamEnvelope(action);
				for (const event of envelope.events) yield decode(event.payload);
				if (envelope.termination.kind === "completed") return;
				if (envelope.termination.kind === "error") throw new Error(`replayed_stream_error: ${envelope.termination.error_type ?? "IntegrationError"}`);
				throw new Error(`incomplete_stream: recorded stream terminated as ${envelope.termination.kind}`);
			}
			const source = await invokeBoundary(call, session.request, session.requestOverridden);
			const events: Array<{type: string; payload: unknown}> = [];
			let termination: StreamEnvelope["termination"] = {kind: "abandoned"};
			try {
				for await (const event of source) {
					events.push({type: eventType(event), payload: encode(event)});
					yield event;
				}
				termination = {kind: "completed"};
			} catch (error) {
				termination = {kind: "error", error_type: error instanceof Error ? error.name : "unknown"};
				throw error;
			} finally {
				const envelope: StreamEnvelope = {schema_version: "1", kind: "stream", events, termination};
				const final = codec.final?.();
				if (final !== undefined) envelope.final = final;
				session.capture(envelope);
			}
		},
	};
}

export async function recordRun<T, R = unknown>(
	logicalKey: string,
	runInput: R,
	call: (() => T | Promise<T>) | ((effectiveInput: R) => T | Promise<T>),
	options: {tracer?: ReturnType<typeof trace.getTracer>; attributes?: Record<string, string | number | boolean>} = {},
): Promise<T> {
	await ensureTelemetry();
	const tracer = options.tracer ?? trace.getTracer("ai-flight-recorder", "0.1.0");
	return tracer.startActiveSpan(logicalKey, async span => {
		span.setAttribute("flight.capture", true);
		span.setAttribute("flight.logical_key", logicalKey);
		span.setAttribute("flight.operation.kind", "agent");
		span.setAttribute("openinference.span.kind", "AGENT");
		span.setAttribute("gen_ai.operation.name", "invoke_agent");
		for (const [key, value] of Object.entries(options.attributes ?? {})) span.setAttribute(key, value);
		captureContent(span, "flight.input", runInput, "capture_run_input");
		try {
			return await runScope.run(`run:${createHash("sha256").update(`${Date.now()}:${Math.random()}`).digest("hex")}`, async () => {
				const result = await invokeBoundary(call, runInput, false);
				captureContent(span, "flight.output", result, "capture_run_output");
				span.setStatus({code: SpanStatusCode.OK});
				return result;
			});
		} catch (error) {
			span.recordException(error as Error);
			span.setStatus({code: SpanStatusCode.ERROR, message: error instanceof Error ? error.name : "unknown"});
			throw error;
		} finally { span.end(); }
	});
}

export function integrationActive(): boolean { return integrationScope.getStore() === true; }
export function withIntegration<T>(callback: () => T): T { return integrationScope.run(true, callback); }
export const liveBoundary = LIVE;

async function invokeBoundary<R, T>(call: (() => T | Promise<T>) | ((effectiveRequest: R) => T | Promise<T>), request: R, overrideRequired: boolean): Promise<T> {
	if (call.length > 0) return (call as (effectiveRequest: R) => T | Promise<T>)(request);
	if (overrideRequired) throw new Error("unsafe_replay: model input override requires a request-aware boundary callback");
	return (call as () => T | Promise<T>)();
}

export const openAIBoundary = <T, R = unknown>(logicalKey: string, request: R, call: (() => T | Promise<T>) | ((effectiveRequest: R) => T | Promise<T>)) => boundary(request, call, {logicalKey, boundaryClass: "llm", sideEffect: "read_only", replayPolicy: "live"});
export const mcpBoundary = <T, R = unknown>(logicalKey: string, request: R, call: (() => T | Promise<T>) | ((effectiveRequest: R) => T | Promise<T>), sideEffect: SideEffect = "unknown") => boundary(request, call, {logicalKey, boundaryClass: "mcp", sideEffect, replayPolicy: "substitute"});

function annotate(options: BoundaryOptions, sideEffect: SideEffect, replayPolicy: ReplayPolicy, requestDigest: string, request: unknown, targetSpan?: Span): void {
  safeMetadata("declare_boundary", () => {
    const span = targetSpan ?? trace.getSpan(context.active());
    span?.setAttribute("flight.logical_key", options.logicalKey);
    span?.setAttribute("flight.boundary.logical_key", options.logicalKey);
    span?.setAttribute("flight.boundary.class", options.boundaryClass);
    if (options.parentLogicalKey) span?.setAttribute("flight.boundary.parent_logical_key", options.parentLogicalKey);
    span?.setAttribute("flight.boundary.side_effect", sideEffect);
    span?.setAttribute("flight.boundary.replay_policy", replayPolicy);
    span?.setAttribute("flight.boundary.request_digest", requestDigest);
    span?.setAttribute("flight.boundary.matching_mode", "exact");
    span?.setAttribute("flight.boundary.ignore_paths", JSON.stringify(options.ignorePaths ?? []));
    captureContent(span, "flight.input", request, "capture_boundary_request");
    if (options.sandbox) span?.setAttribute("flight.boundary.sandbox", options.sandbox);
  });
}

function captureResponse(response: unknown, targetSpan?: Span): void {
	safeMetadata("capture_boundary_response", () => captureContent(targetSpan ?? trace.getSpan(context.active()), "flight.output", response, "capture_boundary_response"));
}

function captureContent(span: ReturnType<typeof trace.getSpan>, attribute: string, value: unknown, operation: string): void {
	if (!span) return;
	try { span.setAttribute(attribute, canonical(value)); }
	catch (error) {
		span.setAttribute(`${attribute}.omission.reason`, "serialization_failed");
		span.setAttribute(`${attribute}.omission.size`, omissionSize(value));
		span.setAttribute(`${attribute}.omission.media_type`, "application/json");
		diagnostic(operation, error);
	}
}

function omissionSize(value: unknown): number {
	if (typeof value === "string") return Buffer.byteLength(value);
	if (value instanceof Uint8Array) return value.byteLength;
	try {
		const encoded = JSON.stringify(value);
		return encoded === undefined ? 0 : Buffer.byteLength(encoded);
	} catch { return 0; }
}

function loadPlan(): ReplayFile | undefined {
  const planPath = process.env.FLIGHT_REPLAY_PLAN;
  if (!planPath) return undefined;
  if (!cachedPlan || cachedPlan.path !== planPath) {
    try { cachedPlan = {path: planPath, value: JSON.parse(readFileSync(planPath, "utf8")) as ReplayFile}; }
    catch { throw new Error("unsafe_replay: replay plan cannot be loaded"); }
  }
  return cachedPlan.value;
}

function matchBoundary(plan: ReplayFile, options: BoundaryOptions, requestDigest: string, occurrence: number): RecordedBoundary | undefined {
	const candidates = (plan.boundaries ?? []).filter(item => item.logical_key === options.logicalKey && item.boundary_class === options.boundaryClass && (item.parent_logical_key ?? "") === (options.parentLogicalKey ?? ""));
	const exact = candidates.filter(item => item.request_digest === requestDigest);
	if (exact.length === 1) return exact[0];
	const repeated = exact.filter(item => (item.occurrence ?? 0) === occurrence);
	if (repeated.length === 1) return repeated[0];
	if (exact.length > 1) throw new Error(`unmatched_boundary: ambiguous replay match for ${options.logicalKey}`);
	return undefined;
}

function scopeKey(): string {
	const explicit = runScope.getStore();
	if (explicit) return explicit;
	const span = trace.getSpan(context.active());
	const spanContext = span && typeof span.spanContext === "function" ? span.spanContext() : undefined;
	if (spanContext && trace.isSpanContextValid(spanContext)) return `trace:${spanContext.traceId}`;
	return "global";
}

export function parseStreamEnvelope(value: unknown): StreamEnvelope {
	if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("unsafe_replay: recorded stream envelope is malformed");
	const candidate = value as Partial<StreamEnvelope>;
	if (candidate.schema_version !== "1" || candidate.kind !== "stream" || !Array.isArray(candidate.events) || !candidate.termination || !["completed", "closed", "error", "abandoned"].includes(candidate.termination.kind ?? "")) throw new Error("unsafe_replay: recorded stream envelope is malformed");
	for (const event of candidate.events) if (!event || typeof event !== "object" || typeof event.type !== "string" || !("payload" in event)) throw new Error("unsafe_replay: recorded stream event is malformed");
	return candidate as StreamEnvelope;
}

function eventType(value: unknown): string {
	if (value && typeof value === "object" && "type" in value && typeof (value as {type?: unknown}).type === "string") return (value as {type: string}).type;
	return value && typeof value === "object" ? value.constructor.name : typeof value;
}

function safeMetadata(operation: string, callback: () => void): void {
  try { callback(); }
	catch (error) { diagnostic(operation, error); }
}

function diagnostic(operation: string, error: unknown): void {
	process.stderr.write(JSON.stringify({level: "warning", code: "flight_sdk_metadata", operation, error_type: error instanceof Error ? error.name : "unknown"}) + "\n");
}

function canonical(value: unknown): string {
	const encoded = canonicalize(value);
	if (encoded === undefined) throw new Error("validation: value is not RFC 8785 JSON");
	return encoded;
}

function digest(value: unknown, ignorePaths: string[] = []): string { return `sha256:${createHash("sha256").update(canonical(applyIgnores(value, ignorePaths))).digest("hex")}`; }

function applyIgnores(value: unknown, paths: string[]): unknown {
  if (paths.length === 0) return value;
  const cloned = JSON.parse(JSON.stringify(value)) as unknown;
  for (const pointer of paths) {
    if (!pointer.startsWith("/")) throw new Error(`unmatched_boundary: invalid JSON pointer ${pointer}`);
    const parts = pointer.slice(1).split("/").map(part => part.replace(/~1/g, "/").replace(/~0/g, "~"));
    let parent: unknown = cloned;
    for (const part of parts.slice(0, -1)) {
      if (Array.isArray(parent)) parent = parent[Number(part)];
      else if (parent && typeof parent === "object") parent = (parent as Record<string, unknown>)[part];
      else { parent = undefined; break; }
    }
    const last = parts.at(-1);
    if (last === undefined || parent === undefined || parent === null) continue;
    if (Array.isArray(parent)) { const index = Number(last); if (Number.isInteger(index) && index >= 0 && index < parent.length) parent.splice(index, 1); }
    else if (typeof parent === "object") delete (parent as Record<string, unknown>)[last];
  }
  return cloned;
}

export const testing = {
	reset(): void { cachedPlan = undefined; occurrences.clear(); },
	digest,
	occurrenceKeys(): string[] { return [...occurrences.keys()]; },
};
