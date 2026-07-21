import type {components} from "./generated/openapi";

export type Completeness = components["schemas"]["completeness-report.schema"];
export type CapsuleCompleteness = components["schemas"]["CapsuleCompleteness"];
export type CapsuleSummary = components["schemas"]["CapsuleSummary"];
export type SessionInfo = components["schemas"]["SessionInfo"];
export type Job = components["schemas"]["Job"];
export type JobSummary = components["schemas"]["JobSummary"];
export type JobEvent = components["schemas"]["JobEvent"];
export type CreateReplayRequest = components["schemas"]["CreateReplayRequest"];
export type Operation = components["schemas"]["operation"];
export type Boundary = components["schemas"]["boundary.schema"];
export type Playback = components["schemas"]["playback.schema"];
export type PlaybackPayload = Playback["payloads"][number];
type GeneratedCapsule = components["schemas"]["RunCapsule"];
export type Capsule = Pick<GeneratedCapsule,
  "schema_version" | "capsule_id" | "source_capsule_id" | "trace_id" | "project_id" | "service_name" |
  "start_time" | "end_time" | "expires_at" | "terminal_status" | "integrity" | "completeness" |
  "provenance" | "model_configuration" | "artifacts" | "operations" | "boundaries" | "privacy" |
  "evaluations" | "replay_history" | "extensions"
>;
export type Observation = components["schemas"]["observation"];
type GeneratedDiff = components["schemas"]["Diff"];
export type DiffResult = Pick<GeneratedDiff,
  "baseline_capsule_id" | "candidate_capsule_id" | "alignments" | "observations" |
  "first_divergence" | "first_divergence_known" | "hypotheses" | "alignment_confidence"
>;

export interface RunFilters {
  search?: string;
  project?: string;
  service?: string;
  outcome?: string;
  readiness?: string;
  after?: string;
  order?: "attention" | "newest";
  cursor?: string;
  limit?: number;
}

export interface RunPage {
  items: CapsuleSummary[];
  next_cursor?: string;
}

export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public remediation = "Try the request again.",
  ) {
    super(message);
  }
}

const configuredOrigin = () => sessionStorage.getItem("flight_service_url")?.replace(/\/$/, "") ?? window.location.origin;
const headers = (): HeadersInit => {
  const token = sessionStorage.getItem("flight_token");
  return token ? {Authorization: `Bearer ${token}`, Accept: "application/json"} : {Accept: "application/json"};
};
const endpoint = (path: string) => `${configuredOrigin()}${path}`;

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(endpoint(path), {...init, headers: {...headers(), ...init?.headers}});
  } catch {
    throw new APIError(0, "offline", "Flight Recorder could not be reached.", "Check the service URL and confirm the server is running.");
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({error: {code: "http_error", message: `HTTP ${response.status}`}}));
    throw new APIError(
      response.status,
      body.error?.code ?? "http_error",
      body.error?.message ?? `HTTP ${response.status}`,
      body.error?.remediation,
    );
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}

function query(values: Record<string, string | number | boolean | undefined>): string {
  const params = new URLSearchParams();
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== "") params.set(key, String(value));
  });
  const encoded = params.toString();
  return encoded ? `?${encoded}` : "";
}

export const connection = {
  get serviceURL() { return configuredOrigin(); },
  configure(serviceURL: string, token: string) {
    sessionStorage.setItem("flight_service_url", serviceURL.replace(/\/$/, ""));
    if (token) sessionStorage.setItem("flight_token", token);
    else sessionStorage.removeItem("flight_token");
  },
  disconnect() {
    sessionStorage.removeItem("flight_service_url");
    sessionStorage.removeItem("flight_token");
  },
};

export const api = {
  session(): Promise<SessionInfo> { return request("/api/v1/session"); },
  capsules(filters: RunFilters = {}): Promise<RunPage> {
    return request(`/api/v1/capsules${query({...filters, order: filters.order ?? "attention", limit: filters.limit ?? 100})}`);
  },
  capsule(id: string): Promise<Capsule> { return request(`/api/v1/capsules/${encodeURIComponent(id)}`); },
  playback(id: string, includePreviews = false): Promise<Playback> {
    return request(`/api/v1/capsules/${encodeURIComponent(id)}/playback${query({include_previews: includePreviews})}`);
  },
  completeness(id: string): Promise<CapsuleCompleteness> { return request(`/api/v1/capsules/${encodeURIComponent(id)}/completeness`); },
  payload(id: string, digest: string): Promise<string> {
    return fetch(endpoint(`/api/v1/capsules/${encodeURIComponent(id)}/payloads/${encodeURIComponent(digest)}`), {headers: headers()}).then(async response => {
      if (!response.ok) throw new APIError(response.status, "payload_unavailable", "Recorded value is unavailable.", "Restore the verified bundle or inspect unaffected evidence.");
      return response.text();
    });
  },
  diff(baseline: string, candidate: string): Promise<DiffResult> {
    return request("/api/v1/diffs", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({baseline_capsule_id: baseline, candidate_capsule_id: candidate})});
  },
  createReplay(body: CreateReplayRequest): Promise<Job> {
    return request("/api/v1/replays", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify(body)});
  },
  job(id: string): Promise<Job> { return request(`/api/v1/jobs/${encodeURIComponent(id)}`); },
  jobs(sourceCapsuleID?: string): Promise<JobSummary[]> {
    return request<{items: JobSummary[]}>(`/api/v1/jobs${query({source_capsule_id: sourceCapsuleID, limit: 100})}`).then(result => result.items);
  },
  jobEvents(id: string): Promise<JobEvent[]> {
    return request<{items: JobEvent[]}>(`/api/v1/jobs/${encodeURIComponent(id)}/events?format=json`).then(result => result.items);
  },
  async exportCapsule(id: string, filename = `${id}.tar.zst`): Promise<void> {
    const response = await fetch(endpoint(`/api/v1/capsules/${encodeURIComponent(id)}/export`), {headers: headers()});
    if (!response.ok) throw new APIError(response.status, "export_failed", "Evidence export failed.");
    const link = document.createElement("a");
    link.href = URL.createObjectURL(await response.blob());
    link.download = filename;
    link.click();
    URL.revokeObjectURL(link.href);
  },
  importCapsule(file: File): Promise<Capsule> {
    return request("/api/v1/capsules/import", {method: "POST", headers: {"Content-Type": "application/zstd"}, body: file});
  },
};
