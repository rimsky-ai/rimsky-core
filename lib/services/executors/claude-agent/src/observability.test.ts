// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { afterEach, describe, it, expect } from "vitest";
import { Observability, capabilitiesPayload } from "./observability.js";
import { resolveDeclaredTags } from "./expected-attributes-schema.js";

describe("Observability ledger", () => {
  it("records and retrieves events for a dispatch", () => {
    const obs = new Observability();
    obs.recordEvent("d1", { category: "step_started", attributes: { step_id: "a" } });
    obs.recordEvent("d1", { category: "tool_call", attributes: { tool_name: "read" } });
    const t = obs.getTrace("d1");
    expect(t.dispatch_id).toBe("d1");
    expect(t.evicted).toBe(false);
    expect(t.complete).toBe(false);
    expect(t.events.length).toBe(2);
    expect(t.events[0].category).toBe("step_started");
  });

  it("returns evicted shape for unknown dispatch (spec §2.6)", () => {
    const obs = new Observability();
    const t = obs.getTrace("missing");
    expect(t.evicted).toBe(true);
    expect(t.complete).toBe(true);
    expect(t.events).toEqual([]);
  });

  it("appends a trace_complete tail when marked complete", () => {
    const obs = new Observability();
    obs.recordEvent("d1", { category: "log", message: "hi" });
    obs.markComplete("d1");
    const t = obs.getTrace("d1");
    expect(t.complete).toBe(true);
    const last = t.events[t.events.length - 1];
    expect(last.category).toBe("trace_complete");
  });

  it("delivers events to subscribers", () => {
    const obs = new Observability();
    const seen: string[] = [];
    const unsub = obs.subscribe("d1", (ev) => seen.push(ev.category));
    obs.recordEvent("d1", { category: "log" });
    obs.markComplete("d1");
    expect(seen).toEqual(["log", "trace_complete"]);
    unsub();
  });

  it("capabilitiesPayload exposes the documented fields", () => {
    const c = capabilitiesPayload();
    expect(c.supports_trace_get).toBe(true);
    expect(c.supports_trace_stream).toBe(true);
    expect(typeof c.retention_after_terminal_seconds).toBe("number");
  });
});

describe("RIMSKY_EXECUTOR_DECLARED_TAGS env override", () => {
  afterEach(() => {
    delete process.env.RIMSKY_EXECUTOR_DECLARED_TAGS;
  });

  it("defaults to [] when unset", () => {
    delete process.env.RIMSKY_EXECUTOR_DECLARED_TAGS;
    expect(resolveDeclaredTags()).toEqual([]);
    expect(capabilitiesPayload().declared_tags).toEqual([]);
  });

  it('parses "a,b" into ["a","b"] and surfaces it in capabilitiesPayload (HTTP surface)', () => {
    process.env.RIMSKY_EXECUTOR_DECLARED_TAGS = "a,b";
    expect(resolveDeclaredTags()).toEqual(["a", "b"]);
    expect(capabilitiesPayload().declared_tags).toEqual(["a", "b"]);
  });

  it("trims whitespace and drops empty segments", () => {
    process.env.RIMSKY_EXECUTOR_DECLARED_TAGS = " progress , , milestone ,";
    expect(resolveDeclaredTags()).toEqual(["progress", "milestone"]);
    expect(capabilitiesPayload().declared_tags).toEqual([
      "progress",
      "milestone",
    ]);
  });
});
