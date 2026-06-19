// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import {
  ReportCompleteInput,
  ReportBlockedInput,
  ReportErrorInput,
  ReportParkInput,
  DispatchContextReadInput,
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

  it("ReportCompleteInput surfaces the optional signoffs field (sign-off gate bag)", () => {
    const parsed = ReportCompleteInput.parse({
      token: "tok",
      changed: true,
      signoffs: ["c2lnLW9uZQ==", "c2lnLXR3bw=="],
    });
    expect(parsed.signoffs).toEqual(["c2lnLW9uZQ==", "c2lnLXR3bw=="]);

    const noSignoffs = ReportCompleteInput.parse({ token: "tok", changed: true });
    expect(noSignoffs.signoffs).toBeUndefined();

    expect(() =>
      ReportCompleteInput.parse({ token: "tok", changed: true, signoffs: [42] }),
    ).toThrow();

    const reportComplete = TOOL_DEFINITIONS[0]!;
    expect(reportComplete.name).toBe("report_complete");
    const props = (reportComplete.inputSchema as {
      properties: Record<string, { type?: string; items?: { type?: string } }>;
      required?: string[];
    }).properties;
    expect(props).toHaveProperty("signoffs");
    expect(props.signoffs!.type).toBe("array");
    expect(props.signoffs!.items?.type).toBe("string");
    const required = (reportComplete.inputSchema as { required?: string[] }).required ?? [];
    expect(required).not.toContain("signoffs");
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

  it("TOOL_DEFINITIONS exposes the seven tools (incl. report_park + dispatch_context_read)", () => {
    const names = TOOL_DEFINITIONS.map((t) => t.name).sort();
    expect(names).toEqual([
      "attributes_read",
      "attributes_set",
      "dispatch_context_read",
      "report_blocked",
      "report_complete",
      "report_error",
      "report_park",
    ]);
  });

  it("ReportParkInput accepts a typed reason + optional fields", () => {
    const parsed = ReportParkInput.parse({
      token: "tok",
      reason: "await_callback",
      reason_note: "operator review pending",
      resume_at: "2026-05-15T12:00:00Z",
    });
    expect(parsed.reason).toBe("await_callback");
    expect(parsed.reason_note).toBe("operator review pending");
    expect(parsed.resume_at).toBe("2026-05-15T12:00:00Z");
  });

  it("ReportParkInput rejects `unspecified` (placeholder enum) and unknown reasons", () => {
    expect(() =>
      ReportParkInput.parse({ token: "tok", reason: "unspecified" }),
    ).toThrow();
    expect(() =>
      ReportParkInput.parse({ token: "tok", reason: "not_a_real_reason" }),
    ).toThrow();
  });

  it("ReportParkInput accepts a minimal payload (reason only)", () => {
    const parsed = ReportParkInput.parse({
      token: "tok",
      reason: "snooze",
    });
    expect(parsed.token).toBe("tok");
    expect(parsed.reason).toBe("snooze");
    expect(parsed.reason_note).toBeUndefined();
    expect(parsed.resume_at).toBeUndefined();
  });

  it("DispatchContextReadInput requires only token", () => {
    expect(DispatchContextReadInput.parse({ token: "tok" })).toEqual({
      token: "tok",
    });
    expect(() => DispatchContextReadInput.parse({})).toThrow();
  });
});
