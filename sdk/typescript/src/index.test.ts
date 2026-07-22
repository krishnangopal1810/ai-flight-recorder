import { afterEach, describe, expect, it, vi } from "vitest";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { trace } from "@opentelemetry/api";
import { boundary, registerEntrypoint, setRunProvenance, testing } from "./index.js";

afterEach(() => {
	delete process.env.FLIGHT_REPLAY_PLAN;
	delete process.env.FLIGHT_PROJECT_ID;
	delete process.env.FLIGHT_ENTRYPOINT_JSON;
	delete process.env.OPENAI_API_KEY;
	testing.reset();
});

describe("boundary", () => {
	it("emits canonical dependency and runtime provenance", () => {
		const attributes = new Map<string, unknown>();
		const getSpan = vi.spyOn(trace, "getSpan").mockReturnValue({setAttribute: (key: string, value: unknown) => { attributes.set(key, value); }} as never);
		setRunProvenance({projectId: "support", runtime: "node:22", dependencyLock: {"package-lock.json": "sha256:bbb", "package.json": "sha256:aaa"}, featureFlags: {retrieval: "v2"}});
		expect(attributes.get("flight.runtime")).toBe("node:22");
		expect(attributes.get("flight.dependency_lock")).toBe('{"package-lock.json":"sha256:bbb","package.json":"sha256:aaa"}');
		expect(attributes.get("flight.feature_flags")).toBe('{"retrieval":"v2"}');
		getSpan.mockRestore();
	});

	it("uses CLI metadata without recording credential values", () => {
		const attributes = new Map<string, unknown>();
		const events: Array<[string, Record<string, unknown>]> = [];
		const getSpan = vi.spyOn(trace, "getSpan").mockReturnValue({
			setAttribute: (key: string, value: unknown) => { attributes.set(key, value); },
			addEvent: (name: string, values: Record<string, unknown>) => { events.push([name, values]); },
		} as never);
		process.env.FLIGHT_PROJECT_ID = "support";
		process.env.FLIGHT_ENTRYPOINT_JSON = JSON.stringify({
			schema_version: "1",
			entrypoint: {
				command: ["node", "agent.js"],
				image: `sha256:${"a".repeat(64)}`,
				working_dir: "/workspace",
				timeout: 120_000_000_000,
				environment: ["OPENAI_API_KEY"],
				model_hosts: ["api.openai.com"],
				model_override: true,
				sandboxes: {mail: "https://mail.test.invalid"},
			},
		});
		process.env.OPENAI_API_KEY = "must-not-be-recorded";

		setRunProvenance();
		registerEntrypoint();

		expect(attributes.get("flight.project.id")).toBe("support");
		expect(attributes.get("flight.entrypoint.environment")).toBe('["OPENAI_API_KEY"]');
		expect(attributes.get("flight.entrypoint.model_hosts")).toBe('["api.openai.com"]');
		expect(attributes.get("flight.entrypoint.model_override")).toBe(true);
		expect(JSON.stringify([...attributes])).not.toContain("must-not-be-recorded");
		expect(events[0]?.[1]["flight.sandbox.name"]).toBe("mail");
		getSpan.mockRestore();
	});

  it("calls live outside replay", async () => {
    await expect(boundary({city: "Pune"}, async () => ({temp: 25}), {logicalKey: "weather", boundaryClass: "api", sideEffect: "read_only"})).resolves.toEqual({temp: 25});
  });

	it("does not fail production calls when metadata cannot be canonicalized", async () => {
		const attributes = new Map<string, unknown>();
		const getSpan = vi.spyOn(trace, "getSpan").mockReturnValue({setAttribute: (key: string, value: unknown) => { attributes.set(key, value); }} as never);
		const diagnostic = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
		await expect(boundary({value: 1n}, async () => "ok", {logicalKey: "opaque", boundaryClass: "api"})).resolves.toBe("ok");
		expect(diagnostic).toHaveBeenCalled();
		expect(String(diagnostic.mock.calls[0]?.[0])).not.toContain("1n");
		expect(attributes.get("flight.input.omission.reason")).toBe("serialization_failed");
		expect(attributes.get("flight.input.omission.size")).toBe(0);
		expect(attributes.get("flight.input.omission.media_type")).toBe("application/json");
		diagnostic.mockRestore();
		getSpan.mockRestore();
	});

  it("substitutes an exact replay match", async () => {
    const request = {city: "Pune"};
    const directory = mkdtempSync(join(tmpdir(), "flight-sdk-"));
    const plan = join(directory, "plan.json");
		writeFileSync(plan, JSON.stringify({substitutions: ["weather"], boundaries: [{logical_key: "weather", boundary_class: "api", parent_logical_key: "agent.root", occurrence: 0, request_digest: testing.digest(request), response: {temp: 24}}]}));
    process.env.FLIGHT_REPLAY_PLAN = plan;
    const live = vi.fn();
		await expect(boundary(request, live, {logicalKey: "weather", boundaryClass: "api", parentLogicalKey: "agent.root"})).resolves.toEqual({temp: 24});
    expect(live).not.toHaveBeenCalled();
  });

  it("passes a model input override to a request-aware live callback", async () => {
    const edited = {messages: [{role: "system", content: "approve eligible refunds"}]};
    const directory = mkdtempSync(join(tmpdir(), "flight-sdk-"));
    const plan = join(directory, "plan.json");
    writeFileSync(plan, JSON.stringify({live_boundaries: ["llm.decision"], boundaries: [{logical_key: "llm.decision", boundary_class: "llm", parent_logical_key: "agent.root", occurrence: 0, request_digest: testing.digest(edited), override_target: "request", request_override: edited}]}));
    process.env.FLIGHT_REPLAY_PLAN = plan;
    await expect(boundary({messages: []}, async effectiveRequest => ({received: effectiveRequest}), {logicalKey: "llm.decision", boundaryClass: "llm", parentLogicalKey: "agent.root", replayPolicy: "live"})).resolves.toEqual({received: edited});
  });

  it("fails closed when a model input override reaches a legacy callback", async () => {
    const edited = {prompt: "changed"};
    const directory = mkdtempSync(join(tmpdir(), "flight-sdk-"));
    const plan = join(directory, "plan.json");
    writeFileSync(plan, JSON.stringify({live_boundaries: ["llm.decision"], boundaries: [{logical_key: "llm.decision", boundary_class: "llm", occurrence: 0, request_digest: testing.digest(edited), override_target: "request", request_override: edited}]}));
    process.env.FLIGHT_REPLAY_PLAN = plan;
    await expect(boundary({prompt: "original"}, async () => ({unexpected: true}), {logicalKey: "llm.decision", boundaryClass: "llm", replayPolicy: "live"})).rejects.toThrow("request-aware boundary callback");
  });

  it("applies JSON pointer ignore rules before matching", () => {
    expect(testing.digest({query: "hello", request_id: "one"}, ["/request_id"])).toBe(testing.digest({query: "hello", request_id: "two"}, ["/request_id"]));
  });

	it("uses RFC 8785 digests across SDKs", () => {
		expect(testing.digest({b: 1.0, a: "€"})).toBe("sha256:2c6fbbec1f6bfa5ea5d93187c7f6960ae2fbc3f05c442ebb923b9a6777b85c76");
	});

	it("does not substitute across boundary kind or parent", async () => {
		const request = {city: "Pune"};
		const directory = mkdtempSync(join(tmpdir(), "flight-sdk-"));
		const plan = join(directory, "plan.json");
		writeFileSync(plan, JSON.stringify({substitutions: ["weather"], boundaries: [{logical_key: "weather", boundary_class: "tool", parent_logical_key: "other", occurrence: 0, request_digest: testing.digest(request), response: {temp: 24}}]}));
		process.env.FLIGHT_REPLAY_PLAN = plan;
		await expect(boundary(request, vi.fn(), {logicalKey: "weather", boundaryClass: "api", parentLogicalKey: "agent.root"})).rejects.toThrow("live fallback is disabled");
	});

  it("never falls through for an unmatched mutation", async () => {
    const directory = mkdtempSync(join(tmpdir(), "flight-sdk-"));
    const plan = join(directory, "plan.json");
    writeFileSync(plan, '{"boundaries":[]}');
    process.env.FLIGHT_REPLAY_PLAN = plan;
    await expect(boundary({amount: 100}, vi.fn(), {logicalKey: "payment", boundaryClass: "tool", sideEffect: "mutating"})).rejects.toThrow("live fallback is disabled");
  });
});
