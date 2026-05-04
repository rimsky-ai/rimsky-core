// observability.ts — claude-agent's ExecutorObservability surface.
//
// Records per-dispatch trace events in a bounded in-memory ledger.
// Exposes:
//   - GET /observability/v1/capabilities
//   - GET /observability/v1/trace/{dispatch_id}
//   - GET /observability/v1/trace/{dispatch_id}/stream  (SSE)
//
// Per spec §2: standard vocabulary + free-form fallback. The agent
// emits step_started/step_completed/step_failed/tool_call/error/log
// via `recordEvent`; consumers see them via getTrace / streamTrace.
//
// gRPC service registration is deferred to v2 — the dashboard's
// Hono proxy talks HTTP+JSON regardless, and the conformance probe
// in `core/cmd/rimsky-conformance/` exercises the HTTP+JSON surface.
//
// Bounded retention: per dispatch, all events are retained until
// retention_after_terminal_seconds elapses past terminal; then evicted
// (the trace returns evicted: true, complete: true, events: []).

import { randomUUID } from "node:crypto";
import type { FastifyInstance } from "fastify";

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
  // listeners for live SSE streams
  listeners: Set<(ev: TraceEvent) => void>;
}

const RETENTION_AFTER_TERMINAL_SECONDS = 3600;
const MAX_TRACES = 1024;

/**
 * @agent-contract
 * what: Per-dispatch in-memory trace ledger backing the executor
 *   observability protocol (HTTP+JSON bridge mounted by `mountObservability`).
 * how: `recordEvent(dispatchId, ev)` appends one event; `markComplete(dispatchId)`
 *   stamps the terminal time; `getTrace(dispatchId)` returns a snapshot.
 *   Tests construct a fresh `Observability` per case; the production singleton
 *   is created by `main.ts` and shared with `agent-run.ts` via a callback.
 * handles: bounded retention (eviction at retention + idle check), free-form
 *   categories, SSE listener fan-out.
 * does-not-handle: persistence (process-local only); cross-process replication;
 *   gRPC service hosting (HTTP+JSON only in v1).
 */
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
        // listener failures are not the producer's problem
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
        // ignore
      }
    }
  }

  getTrace(dispatchId: string): Trace {
    const rec = this.records.get(dispatchId);
    if (!rec || this.isEvicted(rec)) {
      // Per spec §2.6: missing dispatches must surface as the same
      // evicted-shape envelope. evicted:true makes "we don't have
      // it" a single observable signal, regardless of whether the
      // dispatch never existed or has been evicted by retention.
      return { dispatch_id: dispatchId, evicted: true, complete: true, events: [] };
    }
    return {
      dispatch_id: dispatchId,
      evicted: false,
      complete: rec.complete,
      events: [...rec.events],
    };
  }

  /**
   * Subscribe to live events for a dispatch. Returns an unsubscribe
   * function. Replay is the caller's concern (the HTTP handler emits
   * the snapshot first, then attaches the listener).
   */
  subscribe(dispatchId: string, cb: (ev: TraceEvent) => void): () => void {
    const rec = this.getOrCreate(dispatchId);
    rec.listeners.add(cb);
    return () => {
      rec.listeners.delete(cb);
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
    // Hard cap on map size: when the bound is exceeded, drop the
    // oldest record regardless of state. Without this, a long run of
    // never-terminal dispatches would grow the ledger unbounded
    // (parallels the same fix on the Go ledgers).
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
  };
}

/**
 * Mounts the observability HTTP routes onto an existing Fastify
 * instance. The same routes are added by `startObservabilityServer`
 * when the executor boots its observability surface as a standalone
 * server; alternatively a parent listener (e.g. the http-bridge) can
 * mount them under itself.
 *
 * httpBridgeUrl, when non-empty, is included in the capabilities
 * response so dashboards can discover the externally-reachable URL
 * for browser-friendly fetch/SSE.
 */
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
      const snapshot = obs.getTrace(dispatchId);
      for (const ev of snapshot.events) send(ev);
      if (snapshot.evicted || snapshot.complete) {
        reply.raw.end();
        return;
      }
      const unsub = obs.subscribe(dispatchId, (ev) => {
        send(ev);
        if (ev.category === "trace_complete") {
          unsub();
          reply.raw.end();
        }
      });
      req.raw.on("close", () => {
        unsub();
      });
    },
  );
}
