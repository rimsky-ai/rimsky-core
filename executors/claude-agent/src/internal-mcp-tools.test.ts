import { describe, it, expect } from "vitest";
import {
  ReportCompleteInput,
  ReportBlockedInput,
  ReportErrorInput,
  TOOL_DEFINITIONS,
} from "./internal-mcp-tools.js";

describe("internal-mcp-tools schemas", () => {
  it("ReportCompleteInput accepts a full complete payload", () => {
    const parsed = ReportCompleteInput.parse({
      token: "tok",
      result: { ok: true },
      changed: true,
      change_summary: "did thing",
    });
    expect(parsed.token).toBe("tok");
    expect(parsed.changed).toBe(true);
  });

  it("ReportCompleteInput rejects missing fields", () => {
    expect(() => ReportCompleteInput.parse({ token: "tok" })).toThrow();
  });

  it("ReportBlockedInput accepts optional context", () => {
    const parsed = ReportBlockedInput.parse({ token: "tok", reason: "stuck" });
    expect(parsed.reason).toBe("stuck");
  });

  it("ReportErrorInput accepts optional payload", () => {
    const parsed = ReportErrorInput.parse({
      token: "tok",
      error_class: "boom",
    });
    expect(parsed.error_class).toBe("boom");
  });

  it("TOOL_DEFINITIONS exposes the three report tools", () => {
    const names = TOOL_DEFINITIONS.map((t) => t.name).sort();
    expect(names).toEqual(["report_blocked", "report_complete", "report_error"]);
  });
});
