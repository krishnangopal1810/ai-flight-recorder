import { context, trace } from "@opentelemetry/api";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import canonicalize from "canonicalize";

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

export function setRunProvenance(values: {
  projectId: string;
  repositoryUrl?: string;
  commitSha?: string;
  dirtyDigest?: string;
  buildId?: string;
  runtime?: string;
  dependencyLock?: Record<string, string>;
  featureFlags?: Record<string, unknown>;
}): void {
  safeMetadata("set_run_provenance", () => {
    const span = trace.getSpan(context.active());
    span?.setAttribute("flight.capture", true);
    span?.setAttribute("flight.project.id", values.projectId);
    if (values.repositoryUrl) span?.setAttribute("flight.repository.url", values.repositoryUrl);
    if (values.commitSha) span?.setAttribute("flight.git.commit", values.commitSha);
    if (values.dirtyDigest) span?.setAttribute("flight.git.dirty_digest", values.dirtyDigest);
    if (values.buildId) span?.setAttribute("flight.build.id", values.buildId);
    if (values.runtime) span?.setAttribute("flight.runtime", values.runtime);
    if (values.dependencyLock) span?.setAttribute("flight.dependency_lock", canonical(values.dependencyLock));
    if (values.featureFlags) span?.setAttribute("flight.feature_flags", canonical(values.featureFlags));
  });
}

export function registerEntrypoint(command: string[], options: {image?: string; modelHosts?: string[]; inputMode?: string; outputMode?: string} = {}): void {
  safeMetadata("register_entrypoint", () => {
    const span = trace.getSpan(context.active());
    span?.setAttribute("flight.entrypoint.command", JSON.stringify(command));
    span?.setAttribute("flight.entrypoint.input_mode", options.inputMode ?? "stdin_json");
    span?.setAttribute("flight.entrypoint.output_mode", options.outputMode ?? "stdout_json");
    if (options.image) span?.setAttribute("flight.entrypoint.image", options.image);
    span?.setAttribute("flight.entrypoint.model_hosts", JSON.stringify(options.modelHosts ?? []));
  });
}

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

export async function boundary<T>(request: unknown, call: (() => T | Promise<T>) | ((effectiveRequest: unknown) => T | Promise<T>), options: BoundaryOptions): Promise<T> {
  const sideEffect = options.sideEffect ?? "unknown";
  const replayPolicy = options.replayPolicy ?? "substitute";
	const plan = loadPlan();
	const occurrenceKey = `${options.parentLogicalKey ?? ""}\u0000${options.boundaryClass}\u0000${options.logicalKey}`;
  const occurrence = occurrences.get(occurrenceKey) ?? 0;
  occurrences.set(occurrenceKey, occurrence + 1);
	const requestOverride = (plan?.boundaries ?? []).filter(item => item.logical_key === options.logicalKey && item.boundary_class === options.boundaryClass && (item.parent_logical_key ?? "") === (options.parentLogicalKey ?? "") && (item.occurrence ?? 0) === occurrence && item.override_target === "request" && "request_override" in item);
	if (requestOverride.length > 1) throw new Error(`unmatched_boundary: ambiguous replay override for ${options.logicalKey}`);
	const requestOverridden = requestOverride.length === 1;
	const effectiveRequest = requestOverridden ? requestOverride[0]?.request_override : request;
	let requestDigest = "";
	try { requestDigest = digest(effectiveRequest, options.ignorePaths ?? []); }
	catch (error) {
		if (plan) throw new Error(`unmatched_boundary: request for ${options.logicalKey} is not canonical JSON`);
		diagnostic("boundary_digest", error);
	}
	annotate(options, sideEffect, replayPolicy, requestDigest, effectiveRequest);
	if (!plan) { const response = await invokeBoundary(call, effectiveRequest, false); captureResponse(response); return response; }
  const candidates = (plan.boundaries ?? []).filter(item => item.logical_key === options.logicalKey && item.boundary_class === options.boundaryClass && (item.parent_logical_key ?? "") === (options.parentLogicalKey ?? "") && (item.occurrence ?? 0) === occurrence);
  const exact = candidates.filter(item => item.request_digest === requestDigest);
  if (exact.length === 1 && plan.substitutions?.includes(options.logicalKey) && (exact[0]?.response_state ?? "captured") === "captured") { captureResponse(exact[0]?.response); return exact[0]?.response as T; }
  if (exact.length > 1 || candidates.length > 1) throw new Error(`unmatched_boundary: ambiguous replay match for ${options.logicalKey}`);
	if (replayPolicy === "live" && plan.live_boundaries?.includes(options.logicalKey)) { const response = await invokeBoundary(call, effectiveRequest, requestOverridden); captureResponse(response); return response; }
  throw new Error(`unmatched_boundary: ${options.logicalKey} occurrence ${occurrence}; live fallback is disabled`);
}

async function invokeBoundary<T>(call: (() => T | Promise<T>) | ((effectiveRequest: unknown) => T | Promise<T>), request: unknown, overrideRequired: boolean): Promise<T> {
	if (call.length > 0) return (call as (effectiveRequest: unknown) => T | Promise<T>)(request);
	if (overrideRequired) throw new Error("unsafe_replay: model input override requires a request-aware boundary callback");
	return (call as () => T | Promise<T>)();
}

export const openAIBoundary = <T>(logicalKey: string, request: unknown, call: () => T | Promise<T>) => boundary(request, call, {logicalKey, boundaryClass: "llm", sideEffect: "read_only", replayPolicy: "live"});
export const mcpBoundary = <T>(logicalKey: string, request: unknown, call: () => T | Promise<T>, sideEffect: SideEffect = "unknown") => boundary(request, call, {logicalKey, boundaryClass: "mcp", sideEffect, replayPolicy: "substitute"});

function annotate(options: BoundaryOptions, sideEffect: SideEffect, replayPolicy: ReplayPolicy, requestDigest: string, request: unknown): void {
  safeMetadata("declare_boundary", () => {
    const span = trace.getSpan(context.active());
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

function captureResponse(response: unknown): void {
	safeMetadata("capture_boundary_response", () => captureContent(trace.getSpan(context.active()), "flight.output", response, "capture_boundary_response"));
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

export const testing = { reset(): void { cachedPlan = undefined; occurrences.clear(); }, digest };
