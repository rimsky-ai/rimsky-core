import { describe, it, expect } from "vitest";
import { pino } from "pino";
import { runStubAgent } from "./stub-mode.js";

const logger = pino({ level: "silent" });

describe("runStubAgent", () => {
  it("returns the default success outcome when no stub_outcome is supplied", async () => {
    const r = await runStubAgent({
      userdata: {},
      dispatch: async () => ({}),
      logger,
    });
    expect(r.outcome.variant).toBe("success");
    if (r.outcome.variant !== "success") throw new Error("variant");
    expect(r.outcome.attributesDelta).toEqual({ stub: true });
  });

  it("calls each gate in gates_to_call order", async () => {
    const seen: string[] = [];
    await runStubAgent({
      userdata: {
        stub_outcome: {
          gates_to_call: [
            { name: "review_context", input: {} },
            { name: "review_complete", input: {} },
          ],
          terminal: { variant: "success" },
        },
      },
      dispatch: async (tool) => {
        seen.push(tool);
        return {};
      },
      logger,
    });
    expect(seen).toEqual(["review_context", "review_complete"]);
  });

  it("maps an error terminal", async () => {
    const r = await runStubAgent({
      userdata: {
        stub_outcome: {
          gates_to_call: [],
          terminal: { variant: "error", error_class: "silence_timeout" },
        },
      },
      dispatch: async () => ({}),
      logger,
    });
    expect(r.outcome.variant).toBe("error");
    if (r.outcome.variant !== "error") throw new Error("variant");
    expect(r.outcome.errorClass).toBe("silence_timeout");
  });

  it("maps a park terminal", async () => {
    const r = await runStubAgent({
      userdata: {
        stub_outcome: {
          gates_to_call: [],
          terminal: { variant: "park", reason: "waiting" },
        },
      },
      dispatch: async () => ({}),
      logger,
    });
    expect(r.outcome.variant).toBe("park");
  });
});
