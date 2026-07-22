import {test, expect} from "@playwright/test";
import {writeFile} from "node:fs/promises";

test.afterEach(async ({page}, testInfo) => {
  const path = testInfo.outputPath("dom.html");
  await writeFile(path, await page.content());
  await testInfo.attach("dom.html", {path, contentType: "text/html"});
});

test("fresh sessions recover through connection controls instead of raw authorization errors", async ({page}) => {
  await page.route("**/api/v1/session", route => route.fulfill({json: session(false)}));
  await page.goto("/runs");
  await expect(page.getByRole("heading", {name: "Connect to your Flight Recorder"})).toBeVisible();
  await expect(page.getByLabel("Service URL")).toBeVisible();
  await expect(page.getByLabel("Access token")).toBeVisible();
  await expect(page.getByRole("button", {name: /demo/i})).toHaveCount(0);
  await expect(page.getByText("exactly one bearer token is required")).toHaveCount(0);
});

test("add run explains the outcome and tracing prerequisite before technical setup", async ({page}, testInfo) => {
  await baseRoutes(page, []);
  await page.goto("/record");
  await expect(page.getByRole("heading", {name: "See what your agent did"})).toBeVisible();
  await expect(page.getByText("We open the timeline automatically")).toBeVisible();
  await expect(page.getByRole("heading", {name: "Where is the agent you want to inspect?"})).toBeVisible();
  await expect(page.getByRole("button", {name: /On this computer/})).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByRole("heading", {name: "Does this agent already create traces?"})).toBeVisible();
  await page.getByRole("button", {name: "No / I’m not sure"}).click();
  await expect(page.getByRole("heading", {name: "Add tracing first"})).toBeVisible();
  await expect(page.getByText("pip install opentelemetry-distro opentelemetry-exporter-otlp")).toBeVisible();
  await page.getByRole("button", {name: "Tracing is installed — continue"}).click();
  const command = page.getByLabel("Your agent command");
  await command.fill("python my_agent.py");
  await expect(page.locator(".generated-command code")).toContainText("record --project my-agent -- opentelemetry-instrument python my_agent.py");
  const screenshotPath = testInfo.outputPath("guided-add-run.png");
  await page.screenshot({path: screenshotPath, fullPage: true});
  await testInfo.attach("guided-add-run.png", {path: screenshotPath, contentType: "image/png"});
  const replaySetup = page.locator("details.replay-enablement");
  await expect(replaySetup.locator("pre")).not.toBeVisible();
  await replaySetup.getByText("Need to change a model or tool value and replay this run?").click();
  await replaySetup.getByRole("checkbox", {name: "Prepare this recording for controlled replay"}).check();
  await expect(replaySetup.locator("pre").first()).toBeVisible();
  await replaySetup.getByRole("button", {name: "TypeScript"}).click();
  await expect(replaySetup).toContainText("@ai-flight-recorder/sdk");
  await replaySetup.getByRole("button", {name: "Python"}).click();
  await expect(replaySetup).toContainText("from flight_recorder import");
  await page.getByRole("button", {name: /Already running/}).click();
  await expect(page.getByRole("heading", {name: "Connect an agent that already sends traces"})).toBeVisible();
  await expect(page.getByLabel("Agent or project name")).toBeVisible();
  await expect(page.getByText("OTEL_EXPORTER_OTLP_ENDPOINT", {exact: false})).toBeVisible();
  const deployedSetup = page.locator("details.replay-enablement");
  await deployedSetup.getByText("Need to change a model or tool value and replay this run?").click();
  await deployedSetup.getByLabel("Immutable deployed image ID").fill(`sha256:${"a".repeat(64)}`);
  await deployedSetup.getByLabel("Container entrypoint JSON argv").fill('["python","/app/agent.py"]');
  await expect(page.locator(".generated-command.standalone")).toContainText("FLIGHT_ENTRYPOINT_JSON");
  await page.getByRole("button", {name: /A recording file/}).click();
  await expect(page.getByRole("heading", {name: "Import a recorded run"})).toBeVisible();
  await expect(page.getByText("Download one from any recorded run")).toBeVisible();
  await expect(page.getByText(/digest tree/i)).toHaveCount(0);
});

test("the guided capture flow opens a newly received run automatically", async ({page}) => {
  const captured = summary("new-run", "error", "ready");
  let listRequests = 0;
  await page.route("**/api/v1/session", route => route.fulfill({json: session(true)}));
  await page.route(/\/api\/v1\/capsules(?:\?.*)?$/, route => {
    listRequests++;
    return route.fulfill({json: {items: listRequests === 1 ? [] : [captured]}});
  });
  await page.route("**/api/v1/capsules/afr_new-run", route => route.fulfill({json: captured}));
  await page.route(/\/api\/v1\/capsules\/afr_new-run\/playback(?:\?.*)?$/, route => route.fulfill({json: playback(captured)}));
  await page.route(/\/api\/v1\/jobs(?:\?.*)?$/, route => route.fulfill({json: {items: []}}));
  await page.goto("/record");
  await page.getByRole("button", {name: "Yes, tracing is set up"}).click();
  await page.getByRole("button", {name: "Start listening"}).click();
  await expect(page).toHaveURL(/\/runs\/afr_new-run\/understand/);
  await expect(page.getByRole("heading", {name: "Run failed during refund-decision"})).toBeVisible();
});

test("failed run opens on a decision brief and lazy failure-path evidence", async ({page}) => {
  const run = summary("baseline", "error", "blocked");
  let payloadRequests = 0;
  await baseRoutes(page, [run]);
  await page.route(/\/api\/v1\/jobs(?:\?.*)?$/, route => route.fulfill({json: {items: []}}));
  await page.route(/\/api\/v1\/capsules\/afr_baseline\/playback(?:\?.*)?$/, route => route.fulfill({json: playback(run, {
    replay: {state: "blocked", missing: [{code: "boundary_response_missing", boundary_id: "llm-boundary", message: "The policy model response was not recorded."}]},
  })}));
  await page.route("**/api/v1/capsules/afr_baseline/payloads/**", route => {payloadRequests++; return route.fulfill({body: '{"decision":"deny"}', contentType: "application/json"});});
  await page.goto("/runs/afr_baseline/understand");
  await expect(page.getByRole("heading", {name: "Run failed during refund-decision"})).toBeVisible();
  await expect(page.getByRole("button", {name: "Download recording"})).toBeVisible();
  await expect(page.getByRole("heading", {name: "Review missing evidence"})).toBeVisible();
  await expect(page.getByText("The output from “refund-decision” was not fully recorded")).toBeVisible();
  await expect(page.getByRole("tree", {name: "Recorded execution timeline"})).toContainText("refund-decision");
  expect(payloadRequests).toBe(0);
  await page.getByRole("button", {name: /refund-decision llm/}).click();
  await page.getByRole("button", {name: "View recorded value"}).first().click();
  await expect(page.getByText(/"decision": "deny"/)).toBeVisible();
  expect(payloadRequests).toBe(1);

  await page.goto("/runs/afr_baseline/reproduce");
  await expect(page.getByRole("heading", {name: "This run can’t be replayed yet"})).toBeVisible();
  await expect(page.getByText("Replay must return that recorded output instead of contacting the real model.", {exact: false})).toBeVisible();
  await expect(page.getByText("boundary_response_missing", {exact: true})).not.toBeVisible();
  await page.getByText("Technical details", {exact: true}).click();
  await expect(page.getByText("boundary_response_missing", {exact: true})).toBeVisible();
  await page.getByRole("button", {name: "Review the recorded run"}).click();
  await expect(page).toHaveURL(/\/runs\/afr_baseline\/understand\?operation=model/);
});

test("a selected model output can be edited and replayed with recorded tools", async ({page}) => {
  const run = summary("baseline", "error", "ready");
  let replayRequest: Record<string, any> | undefined;
  await baseRoutes(page, [run]);
  await page.route(/\/api\/v1\/capsules\/afr_baseline\/playback(?:\?.*)?$/, route => route.fulfill({json: playback(run)}));
  await page.route("**/api/v1/capsules/afr_baseline/payloads/**", route => route.fulfill({body: '{"decision":"deny"}', contentType: "application/json"}));
  await page.route("**/api/v1/replays", async route => {
    replayRequest = route.request().postDataJSON();
    await route.fulfill({status: 202, json: {id: "job-edit", kind: "replay", state: "queued", request: {}, created_at: "2026-07-21T00:00:00Z", updated_at: "2026-07-21T00:00:00Z"}});
  });
  await page.route("**/api/v1/jobs/job-edit", route => route.fulfill({json: {id: "job-edit", kind: "replay", state: "queued", request: {plan: {source_capsule_id: "afr_baseline"}}, created_at: "2026-07-21T00:00:00Z", updated_at: "2026-07-21T00:00:00Z"}}));
  await page.route("**/api/v1/jobs/job-edit/events?format=json", route => route.fulfill({json: {items: []}}));
  await page.goto("/runs/afr_baseline/understand?operation=model");
  await page.getByRole("button", {name: "Change model output"}).click();
  await expect(page).toHaveURL(/\/reproduce\?.*edit=output/);
  await expect(page.getByRole("heading", {name: "Change one value, then replay the pipeline"})).toBeVisible();
  const editor = page.getByLabel("Candidate JSON value");
  await expect(editor).toHaveValue(/"decision": "deny"/);
  await editor.fill('{"decision":"approve","reason":"eligible"}');
  await page.getByRole("button", {name: "Replay from this change"}).click();
  await expect(page.getByRole("heading", {name: "Replay in progress"})).toBeVisible();
  expect(replayRequest?.overrides).toEqual([{boundary_id: "llm-boundary", target: "response", value: {decision: "approve", reason: "eligible"}}]);
  expect(replayRequest?.substitute_llm).toBe(true);
});

test("replay stays disabled until request-specific preflight recovers", async ({page}) => {
  const run = summary("baseline", "error", "ready");
  await baseRoutes(page, [run]);
  await page.route(/\/api\/v1\/capsules\/afr_baseline\/playback(?:\?.*)?$/, route => route.fulfill({json: playback(run)}));
  await page.route("**/api/v1/capsules/afr_baseline/payloads/**", route => route.fulfill({body: '{"decision":"deny"}', contentType: "application/json"}));
  await page.route("**/api/v1/replays/preflight", async route => {
    const request = route.request().postDataJSON() as Record<string, unknown>;
    const ready = typeof request.candidate_image === "string" && request.candidate_image.startsWith("sha256:");
    await route.fulfill({json: ready ? {
      state: "ready", issues: [], effective: {candidate_image: request.candidate_image, substitutions: ["llm.refund"], live_boundaries: [], allowed_hosts: [], environment_names: []},
    } : {
      state: "blocked", issues: [{code: "candidate_image_mutable", message: "Candidate image is mutable.", remediation: "Provide its sha256 image ID."}], effective: {candidate_image: request.candidate_image, substitutions: [], live_boundaries: [], allowed_hosts: [], environment_names: []},
    }});
  });
  await page.goto("/runs/afr_baseline/reproduce");
  await page.getByRole("button", {name: /Change code or configuration/}).click();
  const candidate = page.getByLabel("Candidate image digest");
  await candidate.fill("agent:latest");
  await expect(page.getByText("Candidate image is mutable.")).toBeVisible();
  await expect(page.getByRole("button", {name: "Replay from this change"})).toBeDisabled();
  await expect(page.getByText(/record again with.*flight record --replay-ready/i)).toBeVisible();
  await candidate.fill(`sha256:${"b".repeat(64)}`);
  await expect(page.getByRole("heading", {name: "Ready for isolated replay"})).toBeVisible();
  await expect(page.getByRole("button", {name: "Replay from this change"})).toBeEnabled();
});

test("comparison opens with the first important change and evidence discipline", async ({page}) => {
  const baseline = summary("baseline", "error", "ready", 1);
  const candidate = {...summary("candidate", "ok", "ready"), source_capsule_id: baseline.capsule_id};
  const recordedRequest = {model: "flight-fake-v1", messages: [{role: "system", content: "conservative"}, {role: "user", content: '{"eligibility":{"eligible":true},"incident":{"amount_usd":79,"request_id":"refund-1842"}}'}]};
  const replayRequest = {...recordedRequest, messages: [{role: "system", content: "policy-grounded"}, recordedRequest.messages[1]]};
  const firstChange = observation("operations.model.input_digest", recordedRequest, replayRequest);
  await baseRoutes(page, [baseline, candidate]);
  await page.route(/\/api\/v1\/capsules\/afr_baseline\/playback(?:\?.*)?$/, route => route.fulfill({json: playback(baseline)}));
  await page.route("**/api/v1/diffs", route => route.fulfill({json: {
    baseline_capsule_id: baseline.capsule_id,
    candidate_capsule_id: candidate.capsule_id,
    first_divergence_known: true,
    first_divergence: firstChange,
    observations: [firstChange, observation("operations.model.output_digest", "deny", "approve")],
    alignments: [{baseline_span: "model", candidate_span: "candidate-model", kind: "matched", confidence: 1}],
    hypotheses: [{summary: "The changed instruction may explain the later decision.", supporting: [], validated: false}],
    alignment_confidence: 1,
  }}));
  await page.goto("/runs/afr_baseline/compare/afr_candidate");
  const graph = page.getByRole("figure", {name: "Where the replay split"});
  await expect(graph).toBeVisible();
  await expect(graph).toContainText("Agent under investigation");
  await expect(graph).toContainText("review-refund-request");
  await expect(graph).toContainText("First change");
  await expect(graph).toContainText("Recorded run");
  await expect(graph).toContainText("Candidate replay");
  await expect(graph).toContainText("conservative");
  await expect(graph).toContainText("policy-grounded");
  const primaryFields = page.getByRole("region", {name: "Changed fields", exact: true});
  await expect(primaryFields.getByText("Baseline", {exact: true})).toBeVisible();
  await expect(primaryFields).toContainText("conservative");
  await expect(primaryFields.getByText("Candidate", {exact: true})).toBeVisible();
  await expect(primaryFields).toContainText("policy-grounded");
  await expect(primaryFields.locator("pre").first()).not.toBeVisible();
  await primaryFields.getByText(/Show \d+ unchanged fields and technical data/).click();
  const completeComparison = primaryFields.locator(".complete-value-comparison");
  await expect(completeComparison).toContainText("1 changed field");
  await expect(completeComparison).toContainText("unchanged fields");
  await expect(completeComparison.locator("pre").first()).not.toBeVisible();
  await completeComparison.getByText(/Show \d+ unchanged fields/).click();
  await expect(completeComparison.getByText("Same in both runs").first()).toBeVisible();
  await completeComparison.getByText("View raw JSON").click();
  await expect(completeComparison.locator("pre").first()).toBeVisible();
  await expect(page.getByText("Possible explanations", {exact: true})).toHaveCount(0);
  await expect(page.getByText("The changed instruction may explain the later decision.")).toHaveCount(0);
  await expect(page.getByText("Supporting evidence", {exact: true})).toHaveCount(0);
  const matchExplanation = page.locator("details.comparison-method");
  await expect(matchExplanation.getByText("How were these values matched?", {exact: true})).toBeVisible();
  await expect(matchExplanation.getByText("Normalized exact comparison", {exact: true})).not.toBeVisible();
  await matchExplanation.getByText("How were these values matched?", {exact: true}).click();
  await expect(matchExplanation.getByRole("heading", {name: "refund-decision appears in both runs"})).toBeVisible();
  await expect(matchExplanation).toContainText("Same operation matched");
  await matchExplanation.getByText("Technical matching details", {exact: true}).click();
  await expect(matchExplanation.getByText("Normalized exact comparison", {exact: true})).toBeVisible();
  await expect(matchExplanation).toContainText("Recorded run");
  await expect(matchExplanation).toContainText("Candidate replay");
  await expect(page.getByRole("heading", {name: "Candidate no longer reproduces the incident"})).toBeVisible();
  await expect(page.getByText("1 later effect", {exact: true})).toBeVisible();
  await page.locator("details.downstream-changes > summary").click();
  const laterEffect = page.locator(".downstream-change").first();
  await expect(laterEffect.getByRole("heading", {name: "Model response changed"})).toBeVisible();
  await expect(laterEffect).toContainText("Baseline");
  await expect(laterEffect).toContainText("deny");
  await expect(laterEffect).toContainText("Candidate");
  await expect(laterEffect).toContainText("approve");
  await expect(laterEffect.getByText("100%", {exact: true})).toHaveCount(0);
  await expect(laterEffect.locator(".semantic-label")).toHaveCount(0);
  await expect(laterEffect.locator("pre").first()).not.toBeVisible();
  await laterEffect.getByText("Show technical value details").click();
  await expect(laterEffect).toContainText("1 changed field");
  await expect(laterEffect.locator("pre").first()).not.toBeVisible();
  await laterEffect.getByText("View raw JSON").click();
  await expect(laterEffect.locator("pre").first()).toBeVisible();
});

test("comparison opens on the exact value the user changed without hiding earlier replay drift", async ({page}) => {
  const baseline = summary("baseline", "error", "ready", 1);
  const candidate = {...summary("candidate", "error", "ready"), source_capsule_id: baseline.capsule_id};
  await baseRoutes(page, [baseline, candidate]);
  await page.route(/\/api\/v1\/capsules\/afr_baseline\/playback(?:\?.*)?$/, route => route.fulfill({json: playback(baseline)}));
  await page.route("**/api/v1/diffs", route => route.fulfill({json: {
    baseline_capsule_id: baseline.capsule_id,
    candidate_capsule_id: candidate.capsule_id,
    first_divergence_known: true,
    first_divergence: observation("operations.model.input_digest", "conservative", "policy-grounded"),
    observations: [observation("operations.model.input_digest", "conservative", "policy-grounded"), observation("operations.model.output_digest", "deny", "approve")],
    alignments: [], hypotheses: [], alignment_confidence: 1,
  }}));
  await page.goto("/runs/afr_baseline/compare/afr_candidate?boundary=llm-boundary&edit=output");
  const graph = page.getByRole("figure", {name: "Where the replay split"});
  await expect(graph.getByText("Your edit", {exact: true})).toBeVisible();
  await expect(graph).toContainText("Model response changed");
  await expect(page.getByRole("heading", {name: /refund-decision › input/i})).toBeVisible();
  await expect(page.getByText("An earlier difference also occurred")).toBeVisible();
  await expect(page.getByRole("button", {name: "Try another change"})).toBeVisible();
  await expect(page.getByRole("button", {name: "Save as regression"})).toHaveCount(0);
});

test("job progress uses durable stages and never invents a percentage", async ({page}) => {
  await page.route("**/api/v1/session", route => route.fulfill({json: session(true)}));
  await page.route(/\/api\/v1\/jobs(?:\?.*)?$/, route => route.fulfill({json: {items: []}}));
  await page.route("**/api/v1/jobs/job-123", route => route.fulfill({json: {id: "job-123", kind: "replay", state: "completed", request: {plan: {source_capsule_id: "afr_baseline"}}, result: {source_capsule_id: "afr_baseline", candidate_capsule_id: "afr_candidate", outcome: "divergent", substitutions: ["tool.policy"], live_boundaries: [], duration_ms: 410, overrides: [{boundary_id: "llm-boundary", target: "response", value: {decision: "approve"}}], override_results: [{boundary_id: "llm-boundary", target: "response", applied: true, message: "the candidate recorded the edited value"}]}, created_at: "2026-07-21T00:00:00Z", updated_at: "2026-07-21T00:00:01Z"}}));
  await page.route("**/api/v1/jobs/job-123/events?format=json", route => route.fulfill({json: {items: stageEvents("job-123")}}));
  await page.goto("/jobs/job-123");
  await expect(page.getByRole("heading", {name: "Completed"})).toBeVisible();
  await expect(page.getByText("Preflight", {exact: true})).toBeVisible();
  await expect(page.getByText("Starting isolated application", {exact: true})).toBeVisible();
  await expect(page.getByText("Comparing executions", {exact: true})).toBeVisible();
  await expect(page.getByText(/15%|58%|100%/)).toHaveCount(0);
  await expect(page.getByRole("button", {name: "See my change"})).toBeVisible();
});

for (const viewport of [{width: 390, height: 844}, {width: 768, height: 1024}, {width: 1440, height: 900}]) {
  test(`the real investigation hierarchy remains usable at ${viewport.width}×${viewport.height}`, async ({page}) => {
    await page.setViewportSize(viewport);
    const baseline = summary("baseline", "error", "ready", 1);
    const candidate = {...summary("candidate", "ok", "ready"), source_capsule_id: baseline.capsule_id};
    await baseRoutes(page, [baseline, candidate]);
    await page.route(/\/api\/v1\/capsules\/afr_baseline\/playback(?:\?.*)?$/, route => route.fulfill({json: playback(baseline)}));
    await page.route("**/api/v1/diffs", route => route.fulfill({json: {
      baseline_capsule_id: baseline.capsule_id, candidate_capsule_id: candidate.capsule_id,
      first_divergence_known: true, first_divergence: observation("operations.model.input_digest", "conservative", "policy-grounded"),
      observations: [observation("operations.model.input_digest", "conservative", "policy-grounded")], alignments: [], hypotheses: [], alignment_confidence: 1,
    }}));
    await page.goto("/runs/afr_baseline/understand");
    await expect(page.getByRole("heading", {name: "Run failed during refund-decision"})).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    if (viewport.width === 390) await expect(page.getByLabel("refund-decision details")).toHaveCount(0);
    await page.getByRole("button", {name: "Review first important change"}).first().click();
    await expect(page.getByRole("figure", {name: "Where the replay split"})).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
  });
}

for (const viewport of [{width: 390, height: 844}, {width: 768, height: 1024}, {width: 1440, height: 900}]) {
  test(`trace-correlated logs stay lazy and accessible at ${viewport.width}×${viewport.height}`, async ({page}) => {
    await page.setViewportSize(viewport);
    const run = summary("logs", "error", "ready");
    const evidence = playback(run);
    const logDigest = `sha256:${"b".repeat(64)}`;
    evidence.capsule.logs = [
      {id: `log_${"1".repeat(64)}`, span_id: "model", service_name: "refund-review-agent", timestamp: "2026-07-21T00:00:01.250Z", severity_number: 17, severity_text: "ERROR", event_name: "refund.denied", body: {digest: logDigest, media_type: "application/json", size: 19, state: "captured"}, scope_name: "refund.logs"},
      {id: `log_${"2".repeat(64)}`, service_name: "refund-review-agent", timestamp: "2026-07-21T00:00:00.500Z", severity_number: 9, severity_text: "INFO", event_name: "run.started", scope_name: "refund.logs"},
    ];
    evidence.payloads.push({digest: logDigest, availability: "available", media_type: "application/json", size: 19, references: ["logs[0].body"]});
    let payloadRequests = 0;
    await baseRoutes(page, [run]);
    await page.route(/\/api\/v1\/capsules\/afr_logs\/playback(?:\?.*)?$/, route => route.fulfill({json: evidence}));
    await page.route("**/api/v1/capsules/afr_logs/payloads/**", route => {
      payloadRequests++;
      return route.fulfill({body: '{"message":"denied"}', contentType: "application/json"});
    });
    await page.goto("/runs/afr_logs/understand?operation=model");
    await expect(page.getByRole("heading", {name: "Run logs"})).toBeVisible();
    await expect(page.getByRole("heading", {name: "Logs", exact: true})).toBeVisible();
    await expect(page.getByText("refund.denied")).toBeVisible();
    await expect(page.getByText("+250 ms")).toBeVisible();
    await expect(page.getByText("run.started")).toBeVisible();
    expect(payloadRequests).toBe(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    await page.locator('.trace-logs[aria-label="Logs"]').getByRole("button", {name: "View recorded value"}).click();
    await expect(page.getByText(/"message": "denied"/)).toBeVisible();
    expect(payloadRequests).toBe(1);
  });
}

test("keyboard navigation starts with a working skip link", async ({page}) => {
  await baseRoutes(page, []);
  await page.goto("/runs");
  const skipLink = page.getByRole("link", {name: "Skip to investigation"});
  await expect(skipLink).toBeAttached();
  await page.keyboard.press("Tab");
  await expect(skipLink).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("#main-content")).toBeFocused();
});

async function baseRoutes(page: import("@playwright/test").Page, runs: Record<string, unknown>[]) {
  await page.route("**/api/v1/session", route => route.fulfill({json: session(true)}));
  await page.route("**/api/v1/replays/preflight", route => route.fulfill({json: {
    state: "ready",
    issues: [],
    effective: {
      candidate_image: `sha256:${"a".repeat(64)}`,
      substitutions: ["llm.refund"],
      live_boundaries: [],
      allowed_hosts: [],
      environment_names: [],
    },
  }}));
  await page.route(/\/api\/v1\/capsules(?:\?.*)?$/, route => route.fulfill({json: {items: runs}}));
  await page.route(/\/api\/v1\/capsules\/([^/?]+)$/, route => {
    const id = route.request().url().split("/").at(-1);
    const run = runs.find(item => item.capsule_id === id);
    return run ? route.fulfill({json: run}) : route.fulfill({status: 404, json: {error: {code: "not_found", message: "run not found"}}});
  });
  await page.route(/\/api\/v1\/jobs(?:\?.*)?$/, route => route.fulfill({json: {items: []}}));
}

function session(connected: boolean) { return {schema_version: "1.0", service_version: "1.0", auth_mode: "token", connected, local_mode_available: false, scopes: connected ? ["read", "replay"] : []}; }
function summary(id: string, terminal: string, readiness: string, candidates = 0) {
  return {capsule_id: `afr_${id}`, trace_id: `trace-${id}`, project_id: "refunds", service_name: "refund-review-agent", start_time: "2026-07-21T00:00:00Z", end_time: "2026-07-21T00:00:02Z", duration_ms: 2000, terminal_status: terminal, failure_operation: terminal === "error" ? "refund-decision" : undefined, replay_readiness: readiness, evidence_warning_count: readiness === "blocked" ? 1 : 0, candidate_replay_count: candidates, requested_model: "flight-fake-v1", completeness: modes(readiness)};
}
function modes(readiness: string) { return {playback: {state: "ready"}, replay: {state: readiness}, diff: {state: "ready"}, bisect: {state: readiness === "ready" ? "ready" : "blocked"}}; }
function playback(run: Record<string, any>, override: Record<string, unknown> = {}) {
  const digest = `sha256:${"a".repeat(64)}`;
  const completeness = {...run.completeness, ...override};
  return {schema_version: "1.0", mode: "playback", executed: false, application_processes_started: 0, outbound_requests: 0, manifest_verified: true, capsule: {...run, integrity: {algorithm: "sha256", manifest_digest: digest, root_digest: digest, payload_digests: [digest]}, provenance: {}, privacy: {mode: "strict"}, operations: [{span_id: "root", kind: "agent", name: "review-refund-request", logical_key: "agent.refund", start_time: run.start_time, end_time: run.end_time, status: "error", sequence: 0}, {span_id: "model", parent_span_id: "root", kind: "llm", name: "refund-decision", logical_key: "llm.refund", start_time: "2026-07-21T00:00:01Z", end_time: run.end_time, status: "error", sequence: 1, input: {digest, media_type: "application/json", size: 19, state: "captured"}, output: {digest, media_type: "application/json", size: 19, state: "captured"}}], boundaries: [{id: "llm-boundary", operation_span_id: "model", logical_key: "llm.refund", class: "llm", request_digest: digest, response: {digest, media_type: "application/json", size: 19, state: "captured"}, side_effect: "read_only", replay_policy: "live", matching_rule: {mode: "exact"}, occurrence: 0}]}, completeness: {schema_version: "1.0", capsule_id: run.capsule_id, ...completeness}, payloads: [{digest, availability: "available", media_type: "application/json", size: 19, references: ["operations[1].input", "operations[1].output", "boundaries[0].response"]}], warnings: []};
}
function observation(path: string, before: unknown, after: unknown) { return {kind: "field_changed", path, before, after, rule: "normalized_exact", confidence: 1, evidence: [{capsule_id: "afr_baseline", span_id: "model", field: path.split(".").at(-1)}, {capsule_id: "afr_candidate", span_id: "candidate-model", field: path.split(".").at(-1)}]}; }
function stageEvents(jobID: string) { return ["preflight", "preparing_candidate", "building_image", "starting_isolated_application", "replaying_recorded_dependencies", "capturing_candidate", "comparing_executions", "complete"].map((stage, index) => ({sequence: index + 1, job_id: jobID, type: "stage", data: {stage, state: stage === "building_image" ? "skipped" : "completed", message: "Verified stage evidence."}, time: `2026-07-21T00:00:0${index}Z`})); }
