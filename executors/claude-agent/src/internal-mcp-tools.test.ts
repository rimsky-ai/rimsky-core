import { describe, it, expect } from "vitest";
import {
  ReportCompleteInput,
  ReportBlockedInput,
  ReportErrorInput,
  AttributesReadInput,
  AttributesSetInput,
  TOOL_DEFINITIONS,
} from "./internal-mcp-tools.js";

describe("internal-mcp-tools schemas", () => {
  it("ReportCompleteInput accepts a payload with attributes_delta", () => {
    const parsed = ReportCompleteInput.parse({
      token: "tok",
      attributes_delta: { ok: true },
      changed: true,
      change_summary: "did thing",
    });
    expect(parsed.token).toBe("tok");
    expect(parsed.changed).toBe(true);
    expect(parsed.attributes_delta).toEqual({ ok: true });
  });

  it("ReportCompleteInput accepts a payload without attributes_delta (incremental pattern)", () => {
    const parsed = ReportCompleteInput.parse({
      token: "tok",
      changed: true,
    });
    expect(parsed.attributes_delta).toBeUndefined();
  });

  it("ReportCompleteInput rejects missing required fields", () => {
    expect(() => ReportCompleteInput.parse({ token: "tok" })).toThrow();
    expect(() =>
      ReportCompleteInput.parse({ changed: true } as unknown),
    ).toThrow();
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

  it("AttributesReadInput requires only token", () => {
    const parsed = AttributesReadInput.parse({ token: "tok" });
    expect(parsed.token).toBe("tok");
  });

  it("AttributesSetInput requires token and delta object", () => {
    const parsed = AttributesSetInput.parse({
      token: "tok",
      delta: { x: 1, nested: { y: 2 } },
    });
    expect(parsed.delta).toEqual({ x: 1, nested: { y: 2 } });

    expect(() => AttributesSetInput.parse({ token: "tok" })).toThrow();
    expect(() =>
      AttributesSetInput.parse({ token: "tok", delta: "no" }),
    ).toThrow();
  });

  it("TOOL_DEFINITIONS exposes the five tools", () => {
    const names = TOOL_DEFINITIONS.map((t) => t.name).sort();
    expect(names).toEqual([
      "attributes_read",
      "attributes_set",
      "report_blocked",
      "report_complete",
      "report_error",
    ]);
  });
});
