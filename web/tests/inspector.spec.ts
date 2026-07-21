import { test, expect } from "@playwright/test";
import {writeFile} from "node:fs/promises";

test.afterEach(async ({page}, testInfo) => {
	const path = testInfo.outputPath("dom.html");
	await writeFile(path, await page.content());
	await testInfo.attach("dom.html", {path, contentType: "text/html"});
});

test("capsule inspector is navigable from deterministic evidence", async ({page}) => {
  await page.route("**/api/v1/capsules", route => route.fulfill({json: {items: [{capsule_id: "afr_1234567890abcdef", trace_id: "trace", project_id: "demo", service_name: "policy-agent", start_time: "2026-07-21T00:00:00Z", terminal_status: "error", completeness: {playback: {state: "ready"}, replay: {state: "blocked", missing: [{code: "entrypoint_missing", message: "a registered replay entrypoint is required"}]}, diff: {state: "ready"}, bisect: {state: "blocked"}}}]}}));
  await page.route(/\/api\/v1\/capsules\/afr_1234567890abcdef\/playback$/, route => route.fulfill({json: playback({capsule_id: "afr_1234567890abcdef", trace_id: "trace", project_id: "demo", service_name: "policy-agent", start_time: "2026-07-21T00:00:00Z", end_time: "2026-07-21T00:00:01Z", terminal_status: "error", completeness: {playback: {state: "ready"}, replay: {state: "blocked", missing: [{code: "entrypoint_missing", message: "a registered replay entrypoint is required"}, {code: "boundary_response_missing", boundary_id: "boundary_tool", message: "recorded tool response is required"}], ambiguity_details: [{code: "boundary_match_ambiguous", boundary_id: "boundary_duplicate", message: "duplicate logical identity"}]}, diff: {state: "ready"}, bisect: {state: "blocked", missing: [{code: "boundary_response_missing", boundary_id: "boundary_tool", message: "recorded tool response is required"}]}}, integrity: {algorithm: "sha256", manifest_digest: "sha256:abc", root_digest: "sha256:abc", payload_digests: []}, provenance: {}, privacy: {mode: "strict"}, operations: [{span_id: "root", kind: "agent", name: "answer-policy", start_time: "2026-07-21T00:00:00Z", end_time: "2026-07-21T00:00:01Z", status: "error", sequence: 0}]})}));
  await page.route("**/api/v1/capsules/afr_1234567890abcdef/completeness", route => route.fulfill({json: {schema_version: "1.0", capsule_id: "afr_1234567890abcdef", playback: {state: "ready"}, replay: {state: "blocked", missing: [{code: "entrypoint_missing", message: "a registered replay entrypoint is required"}, {code: "boundary_response_missing", boundary_id: "boundary_tool", message: "recorded tool response is required"}], ambiguity_details: [{code: "boundary_match_ambiguous", boundary_id: "boundary_duplicate", message: "duplicate logical identity"}]}, diff: {state: "ready"}, bisect: {state: "blocked", missing: [{code: "boundary_response_missing", boundary_id: "boundary_tool", message: "recorded tool response is required"}]}}}));
  await page.goto("/");
  await expect(page.getByRole("heading", {name: "AI Flight Recorder"})).toBeVisible();
  await page.getByRole("button", {name: /policy-agent/}).click();
  await expect(page.getByRole("tree", {name: "Logical execution tree"})).toContainText("answer-policy");
  await expect(page.getByText("Manifest verified · 0/0 payloads available")).toBeVisible();
  await expect(page.getByRole("group", {name: "replay completeness"})).toContainText("boundary_tool");
  await expect(page.getByRole("group", {name: "replay completeness"})).toContainText("boundary_response_missing");
  await expect(page.getByRole("list", {name: "replay missing prerequisites"}).getByRole("listitem")).toHaveCount(2);
  await expect(page.getByRole("list", {name: "replay ambiguous prerequisites"})).toContainText("boundary_duplicate");
});

test("first divergence is labeled as a computed observation", async ({page}) => {
	const summaries = ["baseline", "candidate"].map((id, index) => ({capsule_id: `afr_${id}`, trace_id: id, project_id: "demo", service_name: id, start_time: `2026-07-21T00:00:0${index}Z`, terminal_status: "ok", completeness: {playback: {state: "ready"}, replay: {state: "ready"}, diff: {state: "ready"}, bisect: {state: "ready"}}}));
	await page.route("**/api/v1/capsules", route => route.fulfill({json: {items: summaries}}));
	await page.route(/\/api\/v1\/capsules\/afr_baseline\/playback$/, route => route.fulfill({json: playback({...summaries[0], end_time: "2026-07-21T00:00:01Z", integrity: {algorithm: "sha256", manifest_digest: "sha256:abc", root_digest: "sha256:abc", payload_digests: []}, provenance: {}, privacy: {mode: "strict"}, operations: [{span_id: "root", kind: "agent", name: "answer", start_time: "2026-07-21T00:00:00Z", end_time: "2026-07-21T00:00:01Z", status: "ok", sequence: 0}]})}));
	await page.route("**/api/v1/capsules/afr_baseline/completeness", route => route.fulfill({json: {schema_version: "1.0", capsule_id: "afr_baseline", playback: {state: "ready"}, replay: {state: "ready"}, diff: {state: "ready"}, bisect: {state: "ready"}}}));
	await page.route("**/api/v1/diffs", route => route.fulfill({json: {baseline_capsule_id: "afr_baseline", candidate_capsule_id: "afr_candidate", first_divergence_known: true, first_divergence: {kind: "field_changed", path: "operations.root.input_digest", before: "sha256:old", after: "sha256:new", rule: "normalized_exact", confidence: 1}, observations: [{}], alignment_confidence: 1}}));
	await page.goto("/");
	await page.getByRole("button", {name: /baseline/}).click();
	await page.getByLabel("Candidate capsule").selectOption("afr_candidate");
	await expect(page.getByRole("region", {name: "First divergence"})).toContainText("Observation");
	await expect(page.getByRole("region", {name: "First divergence"})).toContainText("computed, not causal");
});

test("durable replay job progress has a stable route", async ({page}) => {
	await page.route("**/api/v1/capsules", route => route.fulfill({json: {items: []}}));
	await page.route("**/api/v1/jobs/job-123", route => route.fulfill({json: {id: "job-123", kind: "replay", state: "completed", created_at: "2026-07-21T00:00:00Z", updated_at: "2026-07-21T00:00:01Z"}}));
	await page.goto("/jobs/job-123");
	await expect(page.getByRole("region", {name: "Replay job progress"})).toContainText("completed");
});

test("partial playback renders every evidence class without executing the application", async ({page}) => {
	const availableDigest = `sha256:${"a".repeat(64)}`;
	const missingDigest = `sha256:${"b".repeat(64)}`;
	const corruptDigest = `sha256:${"c".repeat(64)}`;
	const evaluationDigest = `sha256:${"d".repeat(64)}`;
	const summary = {capsule_id: "afr_partial", trace_id: "trace", project_id: "incident", service_name: "policy-agent", start_time: "2026-07-21T00:00:00Z", terminal_status: "error", completeness: {playback: {state: "incomplete", missing: [{code: "operation_output_unavailable", message: "recorded output is unavailable"}]}, replay: {state: "blocked"}, diff: {state: "incomplete"}, bisect: {state: "blocked"}}};
	await page.route("**/api/v1/capsules", route => route.fulfill({json: {items: [summary]}}));
	await page.route("**/api/v1/capsules/afr_partial/playback", route => route.fulfill({json: playback({...summary, end_time: "2026-07-21T00:00:02Z", integrity: {algorithm: "sha256", manifest_digest: availableDigest, root_digest: availableDigest, payload_digests: [availableDigest, missingDigest, corruptDigest, evaluationDigest]}, provenance: {}, privacy: {mode: "strict"}, model_configuration: {provider: "openai", requested_model: "recorded-model", response_metadata: {input_tokens: 9, output_tokens: 4, cost_usd: 0.002}}, operations: [{span_id: "root", kind: "agent", name: "answer-policy", logical_key: "agent.root", start_time: "2026-07-21T00:00:00Z", end_time: "2026-07-21T00:00:02Z", status: "error", sequence: 0, input: {digest: availableDigest, media_type: "application/json", size: 19, state: "captured"}, output: {digest: missingDigest, media_type: "application/json", size: 20, state: "captured"}}, {span_id: "model", parent_span_id: "root", kind: "llm", name: "policy-model", logical_key: "llm.policy", start_time: "2026-07-21T00:00:00.100Z", end_time: "2026-07-21T00:00:01.900Z", status: "ok", sequence: 1, attributes: {"gen_ai.usage.input_tokens": "9", "gen_ai.usage.output_tokens": "4", "flight.cost.usd": "0.002"}}], boundaries: [{id: "api", operation_span_id: "root", logical_key: "api.policy", class: "api", request_digest: availableDigest, response: {digest: missingDigest, media_type: "application/json", size: 20, state: "captured"}, side_effect: "read_only", replay_policy: "substitute", matching_rule: {mode: "exact"}, occurrence: 0}], artifacts: [{id: "transcript", type: "transcript", logical_name: "conversation", media_type: "application/json", state: "captured", payload: {digest: corruptDigest, media_type: "application/json", size: 24, state: "captured"}}], evaluations: [{id: "policy-quality", version: "1", score: 0.25, threshold: 0.8, result: {outcome: "fail"}, evidence: {digest: evaluationDigest, media_type: "application/json", size: 15, state: "captured"}}]}, [{digest: availableDigest, availability: "available", media_type: "application/json", size: 19, references: ["operations[0].input"], preview: "{\"prompt\":\"policy\"}", preview_encoding: "json"}, {digest: missingDigest, availability: "missing", media_type: "application/json", size: 20, references: ["boundaries[0].response", "operations[0].output"]}, {digest: corruptDigest, availability: "corrupt", media_type: "application/json", size: 24, references: ["artifacts[0].payload"]}, {digest: evaluationDigest, availability: "available", media_type: "application/json", size: 15, references: ["evaluations[0].evidence"], preview: "{\"grade\":\"F\"}", preview_encoding: "json"}], [{code: "payload_missing", digest: missingDigest, references: ["operations[0].output"], message: "recorded payload bytes are missing"}, {code: "payload_corrupt", digest: corruptDigest, references: ["artifacts[0].payload"], message: "recorded payload bytes failed digest verification"}])}));
	await page.route("**/api/v1/capsules/afr_partial/completeness", route => route.fulfill({json: {schema_version: "1.0", capsule_id: "afr_partial", playback: {state: "incomplete", missing: [{code: "operation_output_unavailable", message: "recorded output is unavailable"}]}, replay: {state: "blocked"}, diff: {state: "incomplete"}, bisect: {state: "blocked"}}}));
	await page.goto("/capsules/afr_partial");
	await expect(page.getByRole("status", {name: "Playback isolation proof"})).toContainText("0 application processes");
	await expect(page.getByRole("status", {name: "Playback isolation proof"})).toContainText("0 outbound requests");
	await expect(page.getByRole("alert", {name: "Playback evidence warnings"})).toContainText("payload_missing");
	await expect(page.getByRole("alert", {name: "Playback evidence warnings"})).toContainText("payload_corrupt");
	await expect(page.getByRole("tree", {name: "Logical execution tree"})).toContainText("answer-policy");
	await expect(page.getByRole("tree", {name: "Logical execution tree"})).toContainText("policy-model");
	await expect(page.getByRole("region", {name: "Input payload"})).toContainText("policy");
	await expect(page.getByRole("region", {name: "Output payload"})).toContainText("missing");
	await expect(page.getByRole("region", {name: "Boundary api.policy"})).toContainText("substitute");
	await expect(page.getByRole("region", {name: "Boundary api.policy"})).toContainText("read_only");
	await expect(page.getByLabel("policy-model cost and usage")).toContainText("0.002");
	await expect(page.getByRole("region", {name: "Artifact evidence"})).toContainText("conversation");
	await expect(page.getByRole("region", {name: "Artifact evidence"})).toContainText("corrupt");
	await expect(page.getByRole("region", {name: "Evaluation evidence"})).toContainText("policy-quality");
	await expect(page.getByRole("region", {name: "Evaluation evidence"})).toContainText("0.25");
	await expect(page.getByRole("group", {name: "playback completeness"})).toContainText("operation_output_unavailable");
});

function playback(capsule: Record<string, unknown>, payloads: unknown[] = [], warnings: unknown[] = []) {
	const modes = capsule.completeness as Record<string, unknown>;
	return {schema_version: "1.0", mode: "playback", executed: false, application_processes_started: 0, outbound_requests: 0, manifest_verified: true, capsule, completeness: {schema_version: "1.0", capsule_id: capsule.capsule_id, playback: modes.playback, replay: modes.replay, diff: modes.diff, bisect: modes.bisect}, payloads, warnings};
}
