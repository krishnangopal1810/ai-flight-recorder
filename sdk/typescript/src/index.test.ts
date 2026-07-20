import { afterEach, describe, expect, it, vi } from "vitest";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { boundary, testing } from "./index.js";

afterEach(() => { delete process.env.FLIGHT_REPLAY_PLAN; testing.reset(); });

describe("boundary", () => {
  it("calls live outside replay", async () => {
    await expect(boundary({city: "Pune"}, async () => ({temp: 25}), {logicalKey: "weather", boundaryClass: "api", sideEffect: "read_only"})).resolves.toEqual({temp: 25});
  });

	it("does not fail production calls when metadata cannot be canonicalized", async () => {
		const diagnostic = vi.spyOn(process.stderr, "write").mockImplementation(() => true);
		await expect(boundary({value: 1n}, async () => "ok", {logicalKey: "opaque", boundaryClass: "api"})).resolves.toBe("ok");
		expect(diagnostic).toHaveBeenCalled();
		expect(String(diagnostic.mock.calls[0]?.[0])).not.toContain("1n");
		diagnostic.mockRestore();
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
