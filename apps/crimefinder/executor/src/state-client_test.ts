import { describe, it, expect } from "vitest";
import { pino } from "pino";
import { StateClient } from "./state-client.js";

const logger = pino({ level: "silent" });

describe("StateClient", () => {
  it("constructs without throwing and exposes typed methods", () => {
    const client = new StateClient({
      endpoint: "127.0.0.1:0",
      sessionToken: "any",
      logger,
    });
    expect(typeof client.appendFinding).toBe("function");
    expect(typeof client.runTests).toBe("function");
    expect(typeof client.commitFix).toBe("function");
    client.close();
  });
});
