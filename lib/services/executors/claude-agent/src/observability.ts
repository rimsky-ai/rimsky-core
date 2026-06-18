// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { randomUUID } from "node:crypto";
import type { FastifyInstance } from "fastify";
import { expectedAttributesSchemaBytes, resolveDeclaredTags, declaredErrorClasses } from "./expected-attributes-schema.js";

export type Severity = "DEBUG" | "INFO" | "WARN" | "ERROR";

export interface TraceEvent {
  event_id: string;
  parent_event_id?: string;
  timestamp: string;
  severity: Severity;
  category: string;
  message?: string;
  attributes?: Record<string, unknown>;
}

export interface Trace {
  dispatch_id: string;
  evicted: boolean;
  complete: boolean;
  events: TraceEvent[];
}

interface TraceRecord {
  dispatch_id: string;
  events: TraceEvent[];
  complete: boolean;
  terminalAt?: number;
  listeners: Set<(ev: TraceEvent) => void>;
}

const RETENTION_AFTER_TERMINAL_SECONDS = 3600;
const MAX_TRACES = 1024;

export class Observability {
  private records = new Map<string, TraceRecord>();
  private order: string[] = [];

  recordEvent(dispatchId: string, ev: Partial<TraceEvent> & Pick<TraceEvent, "category">) {
    const rec = this.getOrCreate(dispatchId);
    const event: TraceEvent = {
      event_id: ev.event_id ?? randomUUID(),
      parent_event_id: ev.parent_event_id,
      timestamp: ev.timestamp ?? new Date().toISOString(),
      severity: ev.severity ?? "INFO",
      category: ev.category,
      message: ev.message,
      attributes: ev.attributes,
    };
    rec.events.push(event);
    for (const cb of rec.listeners) {
      try {
        cb(event);
      } catch {
      }
    }
    this.evictIfNeeded();
  }

  markComplete(dispatchId: string) {
    const rec = this.getOrCreate(dispatchId);
    if (rec.complete) return;
    rec.complete = true;
    rec.terminalAt = Date.now();
    const tail: TraceEvent = {
      event_id: randomUUID(),
      timestamp: new Date().toISOString(),
      severity: "INFO",
      category: "trace_complete",
    };
    rec.events.push(tail);
    for (const cb of rec.listeners) {
      try {
        cb(tail);
      } catch {
      }
    }
  }

  getTrace(dispatchId: string): Trace {
    const rec = this.records.get(dispatchId);
    if (!rec || this.isEvicted(rec)) {
      return { dispatch_id: dispatchId, evicted: true, complete: true, events: [] };
    }
    return {
      dispatch_id: dispatchId,
      evicted: false,
      complete: rec.complete,
      events: [...rec.events],
    };
  }

  subscribe(dispatchId: string, cb: (ev: TraceEvent) => void): () => void {
    const rec = this.getOrCreate(dispatchId);
    rec.listeners.add(cb);
    return () => {
      rec.listeners.delete(cb);
    };
  }

  subscribeWithSnapshot(
    dispatchId: string,
    cb: (ev: TraceEvent) => void,
  ): { snapshot: Trace; unsubscribe: () => void } {
    const rec = this.getOrCreate(dispatchId);
    if (this.isEvicted(rec)) {
      return {
        snapshot: { dispatch_id: dispatchId, evicted: true, complete: true, events: [] },
        unsubscribe: () => {},
      };
    }
    const snapshot: Trace = {
      dispatch_id: dispatchId,
      evicted: false,
      complete: rec.complete,
      events: [...rec.events],
    };
    rec.listeners.add(cb);
    return {
      snapshot,
      unsubscribe: () => {
        rec.listeners.delete(cb);
      },
    };
  }

  private getOrCreate(dispatchId: string): TraceRecord {
    let rec = this.records.get(dispatchId);
    if (rec) return rec;
    rec = { dispatch_id: dispatchId, events: [], complete: false, listeners: new Set() };
    this.records.set(dispatchId, rec);
    this.order.push(dispatchId);
    this.evictIfNeeded();
    return rec;
  }

  private isEvicted(rec: TraceRecord): boolean {
    if (!rec.complete || !rec.terminalAt) return false;
    return Date.now() - rec.terminalAt > RETENTION_AFTER_TERMINAL_SECONDS * 1000;
  }

  private evictIfNeeded() {
    while (this.records.size > MAX_TRACES) {
      const oldestId = this.order.shift();
      if (!oldestId) break;
      this.records.delete(oldestId);
    }
  }
}

export function capabilitiesPayload(httpBridgeUrl = "") {
  return {
    supports_trace_get: true,
    supports_trace_stream: true,
    retention_after_terminal_seconds: RETENTION_AFTER_TERMINAL_SECONDS,
    custom_ui: null,
    http_bridge_url: httpBridgeUrl,
    expected_attributes_schema: Buffer.from(expectedAttributesSchemaBytes()).toString("base64"),
    declared_tags: resolveDeclaredTags(),
    declared_error_classes: declaredErrorClasses,
  };
}

export function mountObservability(
  app: FastifyInstance,
  obs: Observability,
  httpBridgeUrl = "",
): void {
  app.get("/observability/v1/capabilities", async () =>
    capabilitiesPayload(httpBridgeUrl),
  );

  app.get<{ Params: { dispatch_id: string } }>(
    "/observability/v1/trace/:dispatch_id",
    async (req) => obs.getTrace(req.params.dispatch_id),
  );

  app.get<{ Params: { dispatch_id: string } }>(
    "/observability/v1/trace/:dispatch_id/stream",
    async (req, reply) => {
      const dispatchId = req.params.dispatch_id;
      reply
        .header("Content-Type", "text/event-stream")
        .header("Cache-Control", "no-cache")
        .header("Connection", "keep-alive");
      const send = (ev: TraceEvent) => {
        reply.raw.write(`data: ${JSON.stringify(ev)}\n\n`);
      };
      const result = obs.subscribeWithSnapshot(dispatchId, (ev) => {
        send(ev);
        if (ev.category === "trace_complete") {
          result.unsubscribe();
          reply.raw.end();
        }
      });
      for (const ev of result.snapshot.events) send(ev);
      if (result.snapshot.evicted || result.snapshot.complete) {
        result.unsubscribe();
        reply.raw.end();
        return;
      }
      const idleMs = Number(process.env.RIMSKY_OBS_IDLE_TIMEOUT_MS ?? 5 * 60 * 1000);
      let idleTimer: ReturnType<typeof setTimeout> | null = null;
      const armIdle = () => {
        if (idleMs <= 0) return;
        if (idleTimer !== null) clearTimeout(idleTimer);
        idleTimer = setTimeout(() => {
          reply.raw.write(`: idle_timeout\n\n`);
          result.unsubscribe();
          reply.raw.end();
        }, idleMs);
      };
      armIdle();
      req.raw.on("close", () => {
        if (idleTimer !== null) clearTimeout(idleTimer);
        result.unsubscribe();
      });
    },
  );
}
