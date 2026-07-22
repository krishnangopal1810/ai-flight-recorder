import {describe, expect, it} from "vitest";

import {createOTLPLogging} from "./logging.js";

describe("createOTLPLogging", () => {
  it("returns an isolated standard logger with explicit lifecycle controls", async () => {
    const runtime = createOTLPLogging({
      endpoint: "http://127.0.0.1:4318",
      scopeName: "flight.logging.test",
    });

    expect(runtime.logger).toBeDefined();
    expect(runtime.forceFlush).toBeTypeOf("function");
    expect(runtime.shutdown).toBeTypeOf("function");
    await runtime.forceFlush();
    await runtime.shutdown();
  });
});
