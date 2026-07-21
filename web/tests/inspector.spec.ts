import { test, expect } from "@playwright/test";
import {writeFile} from "node:fs/promises";

test.afterEach(async ({page}, testInfo) => {
	const path = testInfo.outputPath("dom.html");
	await writeFile(path, await page.content());
	await testInfo.attach("dom.html", {path, contentType: "text/html"});
});

test("capsule inspector is navigable from deterministic evidence", async ({page}) => {
  await page.route("**/api/v1/capsules", route => route.fulfill({json: {items: [{capsule_id: "afr_1234567890abcdef", trace_id: "trace", project_id: "demo", service_name: "policy-agent", start_time: "2026-07-21T00:00:00Z", terminal_status: "error", completeness: {playback: {state: "ready"}, replay: {state: "blocked", missing: [{code: "entrypoint_missing", message: "a registered replay entrypoint is required"}]}, diff: {state: "ready"}, bisect: {state: "blocked"}}}]}}));
  await page.route(/\/api\/v1\/capsules\/afr_1234567890abcdef$/, route => route.fulfill({json: {capsule_id: "afr_1234567890abcdef", trace_id: "trace", project_id: "demo", service_name: "policy-agent", start_time: "2026-07-21T00:00:00Z", end_time: "2026-07-21T00:00:01Z", terminal_status: "error", completeness: {playback: {state: "ready"}, replay: {state: "blocked", missing: [{code: "entrypoint_missing", message: "a registered replay entrypoint is required"}]}, diff: {state: "ready"}, bisect: {state: "blocked"}}, integrity: {root_digest: "sha256:abc", payload_digests: []}, provenance: {}, privacy: {mode: "strict"}, operations: [{span_id: "root", kind: "agent", name: "answer-policy", status: "error", sequence: 0}]}}));
  await page.route("**/api/v1/capsules/afr_1234567890abcdef/completeness", route => route.fulfill({json: {schema_version: "1.0", capsule_id: "afr_1234567890abcdef", playback: {state: "ready"}, replay: {state: "blocked", missing: [{code: "entrypoint_missing", message: "a registered replay entrypoint is required"}, {code: "boundary_response_missing", boundary_id: "boundary_tool", message: "recorded tool response is required"}], ambiguity_details: [{code: "boundary_match_ambiguous", boundary_id: "boundary_duplicate", message: "duplicate logical identity"}]}, diff: {state: "ready"}, bisect: {state: "blocked", missing: [{code: "boundary_response_missing", boundary_id: "boundary_tool", message: "recorded tool response is required"}]}}}));
  await page.goto("/");
  await expect(page.getByRole("heading", {name: "AI Flight Recorder"})).toBeVisible();
  await page.getByRole("button", {name: /policy-agent/}).click();
  await expect(page.getByRole("tree", {name: "Logical execution tree"})).toContainText("answer-policy");
  await expect(page.getByText("SHA-256 verified")).toBeVisible();
  await expect(page.getByRole("group", {name: "replay completeness"})).toContainText("boundary_tool");
  await expect(page.getByRole("group", {name: "replay completeness"})).toContainText("boundary_response_missing");
  await expect(page.getByRole("list", {name: "replay missing prerequisites"}).getByRole("listitem")).toHaveCount(2);
  await expect(page.getByRole("list", {name: "replay ambiguous prerequisites"})).toContainText("boundary_duplicate");
});

test("first divergence is labeled as a computed observation", async ({page}) => {
	const summaries = ["baseline", "candidate"].map((id, index) => ({capsule_id: `afr_${id}`, trace_id: id, project_id: "demo", service_name: id, start_time: `2026-07-21T00:00:0${index}Z`, terminal_status: "ok", completeness: {playback: {state: "ready"}, replay: {state: "ready"}, diff: {state: "ready"}, bisect: {state: "ready"}}}));
	await page.route("**/api/v1/capsules", route => route.fulfill({json: {items: summaries}}));
	await page.route(/\/api\/v1\/capsules\/afr_baseline$/, route => route.fulfill({json: {...summaries[0], end_time: "2026-07-21T00:00:01Z", integrity: {root_digest: "sha256:abc", payload_digests: []}, provenance: {}, privacy: {mode: "strict"}, operations: [{span_id: "root", kind: "agent", name: "answer", status: "ok", sequence: 0}]}}));
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
