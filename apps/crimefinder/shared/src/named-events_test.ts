import { describe, it, expect } from "vitest";
import {
  NAMED_EVENT_NAMES,
  NamedEventEnvelopeSchema,
  makeNamedEvent,
  encodeEventPayload,
  FindingResolvedDataSchema,
  FindingEmittedDataSchema,
} from "./named-events.js";

describe("named-events", () => {
  it("declares the spec's twelve event names (spec lines 584-595)", () => {
    expect(NAMED_EVENT_NAMES.length).toBe(12);
    expect(NAMED_EVENT_NAMES).toContain("pass_opened");
    expect(NAMED_EVENT_NAMES).toContain("pass_closed");
    expect(NAMED_EVENT_NAMES).toContain("zone_started");
    expect(NAMED_EVENT_NAMES).toContain("zone_completed");
    expect(NAMED_EVENT_NAMES).toContain("zone_skipped");
    expect(NAMED_EVENT_NAMES).toContain("finding_emitted");
    expect(NAMED_EVENT_NAMES).toContain("finding_resolved");
    expect(NAMED_EVENT_NAMES).toContain("finding_deferred");
    expect(NAMED_EVENT_NAMES).toContain("finding_dedup_marked");
    expect(NAMED_EVENT_NAMES).toContain("tests_ran");
    expect(NAMED_EVENT_NAMES).toContain("commit_failed");
    expect(NAMED_EVENT_NAMES).toContain("help_requested");
  });

  it("makeNamedEvent builds a valid envelope", () => {
    const env = makeNamedEvent("finding_resolved", {
      passId: "p_1",
      zoneId: "z_1",
      sessionId: "sess_x",
      data: { finding_id: "f_1", commit_sha: "abc", iter_num: 1 },
    });
    expect(env.event).toBe("finding_resolved");
    expect(env.pass_id).toBe("p_1");
  });

  it("envelope round-trips via encodeEventPayload", () => {
    const env = makeNamedEvent("zone_completed", {
      passId: "p_1",
      data: { findings_recorded: 0, coverage_pct: 100 },
    });
    const bytes = encodeEventPayload(env);
    const parsed = JSON.parse(new TextDecoder().decode(bytes));
    expect(NamedEventEnvelopeSchema.parse(parsed)).toEqual(env);
  });

  it("rejects unknown event names", () => {
    expect(() => makeNamedEvent("nope" as any, { passId: "p", data: {} })).toThrow();
  });

  it("per-event data schemas accept canonical shapes", () => {
    expect(
      FindingResolvedDataSchema.parse({ finding_id: "f_x", commit_sha: "abc", iter_num: 1 }),
    ).toBeTruthy();
    expect(
      FindingEmittedDataSchema.parse({
        finding_id: "f_x",
        effective_class: 1,
        auto_rerouted: false,
        file: "src/foo.ts",
      }),
    ).toBeTruthy();
  });
});
