import { describe, it, expect } from "vitest";
import {
  GATE_ERROR_CLASSES,
  EXECUTOR_ERROR_CLASSES,
  makeGateError,
  GateError,
} from "./error-classes.js";

describe("error-classes", () => {
  it("has the expected gate error vocabulary", () => {
    expect(GATE_ERROR_CLASSES).toContain("finding_not_found");
    expect(GATE_ERROR_CLASSES).toContain("commit_failed");
    expect(GATE_ERROR_CLASSES).toContain("concept_citation_missing");
    expect(GATE_ERROR_CLASSES).toContain("coverage_below_threshold");
    expect(GATE_ERROR_CLASSES).toContain("coverage_above_threshold");
    expect(GATE_ERROR_CLASSES).toContain("wrong_session_role");
    expect(GATE_ERROR_CLASSES).toContain("coverage_file_missing");
    expect(GATE_ERROR_CLASSES).toContain("coverage_file_escaped");
    expect(GATE_ERROR_CLASSES).toContain("invalid_status");
    expect(GATE_ERROR_CLASSES).toContain("invalid_request");
    expect(GATE_ERROR_CLASSES.length).toBe(18);
  });

  it("has the expected executor error vocabulary", () => {
    expect(EXECUTOR_ERROR_CLASSES).toEqual([
      "silence_timeout",
      "tool_error",
      "commit_failed",
      "tests_failed",
    ]);
  });

  it("makeGateError builds an MCP application-error envelope", () => {
    const e = makeGateError("finding_not_found", "no such finding", false, { finding_id: "f_x" });
    expect(e.code).toBe(-32000);
    expect(e.message).toBe("no such finding");
    expect(e.data.crimefinder_error_class).toBe("finding_not_found");
    expect(e.data.retryable).toBe(false);
    expect(e.data.finding_id).toBe("f_x");
  });

  it("GateError carries its envelope", () => {
    const env = makeGateError("commit_failed", "git rejected", false);
    const err = new GateError(env);
    expect(err.envelope).toBe(env);
    expect(err.message).toBe("git rejected");
    expect(err.name).toBe("GateError");
  });
});
