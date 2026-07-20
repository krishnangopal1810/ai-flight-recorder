import type {components} from "./generated/openapi";

export type Completeness = components["schemas"]["completeness-report.schema"];
export type CompletenessState = Completeness["state"];
export type CapsuleSummary = components["schemas"]["CapsuleSummary"];
export type Operation = components["schemas"]["operation"];
export type Artifact = components["schemas"]["artifact"];
export type Boundary = components["schemas"]["boundary.schema"];
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
export type Job = components["schemas"]["Job"];

const headers = (): HeadersInit => {
  const token = sessionStorage.getItem("flight_token");
  return token ? {Authorization: `Bearer ${token}`, Accept: "application/json"} : {Accept: "application/json"};
};

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {...init, headers: {...headers(), ...init?.headers}});
  if (!response.ok) {
    const body = await response.json().catch(() => ({error: {message: `HTTP ${response.status}`}}));
    throw new Error(body.error?.message ?? `HTTP ${response.status}`);
  }
  return response.json() as Promise<T>;
}

export const api = {
  async capsules(): Promise<CapsuleSummary[]> { return (await request<{items: CapsuleSummary[]}>("/api/v1/capsules")).items; },
  capsule(id: string): Promise<Capsule> { return request(`/api/v1/capsules/${encodeURIComponent(id)}`); },
  diff(baseline: string, candidate: string): Promise<DiffResult> { return request("/api/v1/diffs", {method: "POST", headers: {"Content-Type": "application/json"}, body: JSON.stringify({baseline_capsule_id: baseline, candidate_capsule_id: candidate})}); },
  job(id: string): Promise<Job> { return request(`/api/v1/jobs/${encodeURIComponent(id)}`); },
};
