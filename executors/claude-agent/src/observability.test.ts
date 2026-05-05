// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import { Observability, capabilitiesPayload } from "./observability.js";

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
    // Per spec §2.6, missing dispatches surface as the evicted-shape
    // envelope: evicted=true, complete=true, events=[]. This makes
    // "we don't have it" a single observable signal regardless of
    // whether the dispatch never existed or was evicted by retention.
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
