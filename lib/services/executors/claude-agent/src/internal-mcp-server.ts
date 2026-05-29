// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import http from "node:http";
import crypto from "node:crypto";
import type { AddressInfo } from "node:net";
import type { Logger } from "pino";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { z } from "zod";
import { TokenRegistry } from "./token-registry.js";

/**
 * MCP-HTTP callback server for the rimsky claude-agent.
 *
 * Spec: docs/specs/2026-04-25-stores-redesign-design.md §12, §16.1.
 *
 * The SDK's streamable-HTTP transport is strictly **one session per
 * transport instance** in stateful mode: once `_initialized` is true,
 * the transport rejects further initialize requests with HTTP 400
 * `Invalid Request: Server already initialized`, and validates the
 * `mcp-session-id` header on every non-init request against its single
 * captured sessionId (404 `Session not found` on mismatch). See the
 * SDK source at
 * `node_modules/@modelcontextprotocol/sdk/dist/esm/server/webStandardStreamableHttp.js:422-428`
 * and `:595-605`.
 *
 * That means a singleton transport is wrong for a multi-tenant
 * executor: the first dispatch's CLI initializes and the transport
 * binds to its sessionId; every subsequent dispatch's CLI gets HTTP
 * 400 on its initialize, surfaces it as "MCP server not connected,"
 * and the dispatch wedges until the silence timer fires.
 *
 * This module maintains a `Map<sessionId, SessionEntry>` and routes
 * incoming requests by `mcp-session-id` header:
 *   - request with no header   → new transport + McpServer, init handshake
 *   - request with known sid   → route to that transport
 *   - request with unknown sid → HTTP 404 (orphaned client; should reinit)
 *
 * Tools surfaced (per spec §12 and §16.1, and 2026-05-14 Piece 2):
 *   - `report_complete` (optional `attributes_delta`)
 *   - `report_blocked`
 *   - `report_error`
 *   - `report_park`
 *   - `attributes_read`  — returns dispatch-time attributes snapshot.
 *   - `attributes_set`   — POSTs `{delta}` to the supervisor's
 *     incremental writeback URL.
 *
 * Tools are scoped per-run via the per-run `token` argument validated
 * against `TokenRegistry`. The CLI subprocess receives the token via
 * the `RIMSKY_CALLBACK_TOKEN` env var.
 *
 * Teardown deferral: tool handlers that drive the dispatch terminal
 * (`report_complete` / `report_blocked` / `report_error` /
 * `report_park`) hand teardownCli to a `setTimeout(..., 0)` so the
 * MCP tool response is flushed back to the CLI before the subprocess
 * gets SIGTERM. Mirrors brain's `setTimeout(() => config.onTopicPublished(result), 0)`
 * pattern.
 */
/**
 * MCP server name advertised to the Claude CLI via `--mcp-config`. The CLI
 * namespaces every tool from this server as
 * `mcp__${CALLBACK_MCP_SERVER_NAME}__<toolName>`, so the executor's
 * allowlist derivation (cli-runner.ts) MUST use this same constant to build
 * the fully-qualified tool names — a literal drift here would silently break
 * the `--allowedTools` gate under Claude Code's deferred-MCP permission
 * surface.
 */
export const CALLBACK_MCP_SERVER_NAME = "rimsky-callback";

export interface CallbackServerHandle {
  readonly host: string;
  readonly port: number;
  readonly url: string;
  readonly registry: TokenRegistry;
  close(): Promise<void>;
}

interface SessionEntry {
  transport: StreamableHTTPServerTransport;
  mcp: McpServer;
  lastActivityAt: number;
}

export async function startInternalMcpServer(opts: {
  host?: string;
  port?: number;
  logger: Logger;
  /**
   * Idle eviction window. A session with no requests for this long is
   * closed and evicted from the routing map. Default 10 minutes. Set
   * lower in tests to exercise eviction.
   */
  sessionIdleMs?: number;
}): Promise<CallbackServerHandle> {
  const registry = new TokenRegistry();
  const log = opts.logger.child({ component: "internal-mcp" });
  const sessionIdleMs = opts.sessionIdleMs ?? 600_000;

  const sessions = new Map<string, SessionEntry>();

  const evict = (sessionId: string, reason: string): void => {
    const entry = sessions.get(sessionId);
    if (!entry) return;
    sessions.delete(sessionId);
    log.info(
      { session_id: sessionId, reason, active_sessions: sessions.size },
      "mcp.session_closed",
    );
    void entry.transport.close().catch(() => { /* ignore */ });
    void entry.mcp.close().catch(() => { /* ignore */ });
  };

  const createSession = async (): Promise<SessionEntry> => {
    const mcp = new McpServer({
      name: CALLBACK_MCP_SERVER_NAME,
      version: "1.0.0",
    });
    registerTools(mcp, registry, log);
    // `transport` is captured by the onsessioninitialized closure below
    // by reference; the binding is valid by the time the SDK fires that
    // callback from inside handleRequest (post-init handshake).
    const transport = new StreamableHTTPServerTransport({
      sessionIdGenerator: () => crypto.randomUUID(),
      onsessioninitialized: (newSid) => {
        const entry: SessionEntry = {
          transport,
          mcp,
          lastActivityAt: Date.now(),
        };
        sessions.set(newSid, entry);
        log.info(
          { session_id: newSid, active_sessions: sessions.size },
          "mcp.session_opened",
        );
      },
    });
    transport.onclose = () => {
      const sid = transport.sessionId;
      if (sid) evict(sid, "transport_closed");
    };
    await mcp.connect(transport);
    return { transport, mcp, lastActivityAt: Date.now() };
  };

  const httpServer = http.createServer(async (req, res) => {
    try {
      const sidHeader = req.headers["mcp-session-id"];
      const sid = typeof sidHeader === "string" ? sidHeader : undefined;
      if (sid) {
        const entry = sessions.get(sid);
        if (!entry) {
          // Orphaned client (executor restart, eviction, etc.). Surface
          // a 404 so the CLI can re-handshake instead of getting an
          // ambiguous "Server not initialized" 400 from a fresh transport.
          log.warn({ session_id: sid }, "mcp.unknown_session");
          res.statusCode = 404;
          res.setHeader("content-type", "application/json");
          res.end(
            JSON.stringify({
              jsonrpc: "2.0",
              error: { code: -32001, message: "Session not found" },
            }),
          );
          return;
        }
        entry.lastActivityAt = Date.now();
        await entry.transport.handleRequest(req, res);
        return;
      }
      // No sid header — should be an initialize. Mint a fresh session.
      const fresh = await createSession();
      await fresh.transport.handleRequest(req, res);
    } catch (err) {
      log.error({ error: String(err) }, "mcp.request_failed");
      if (!res.headersSent) {
        res.statusCode = 500;
        res.end("internal mcp error");
      }
    }
  });

  // Surface runtime HTTP faults that would otherwise vanish into an
  // unobservable error event. Without these, a malformed request line
  // or a peer-protocol violation can desocket without a log line.
  httpServer.on("clientError", (err, socket) => {
    log.warn({ error: String(err) }, "mcp.client_error");
    try { socket.end("HTTP/1.1 400 Bad Request\r\n\r\n"); } catch { /* ignore */ }
  });
  httpServer.on("error", (err) => {
    log.error({ error: String(err) }, "mcp.http_server_error");
  });

  const host = opts.host ?? "127.0.0.1";
  const port = opts.port ?? 0;
  await new Promise<void>((resolve, reject) => {
    const onErr = (err: Error) => reject(err);
    httpServer.once("error", onErr);
    httpServer.listen(port, host, () => {
      httpServer.off("error", onErr);
      resolve();
    });
  });

  const address = httpServer.address() as AddressInfo;
  const actualPort = address.port;

  // Idle-eviction sweep. Bounded interval (≥ 30s) prevents the sweep
  // itself from becoming a hot loop in tests with a small idle window.
  const sweepIntervalMs = Math.max(30_000, Math.floor(sessionIdleMs / 4));
  const sweepTimer = setInterval(() => {
    const cutoff = Date.now() - sessionIdleMs;
    for (const [sid, entry] of sessions) {
      if (entry.lastActivityAt < cutoff) evict(sid, "idle_timeout");
    }
  }, sweepIntervalMs);
  // Don't keep the process alive solely for the sweep timer.
  sweepTimer.unref();

  return {
    host,
    port: actualPort,
    url: `http://${host}:${actualPort}/mcp`,
    registry,
    close: async () => {
      clearInterval(sweepTimer);
      for (const sid of [...sessions.keys()]) {
        evict(sid, "server_closing");
      }
      await new Promise<void>((resolve, reject) => {
        httpServer.close((err) => (err ? reject(err) : resolve()));
      });
    },
  };
}

/**
 * Registers the rimsky-callback tool surface on the supplied McpServer.
 * Exported so unit tests can wire the same tools through an InMemoryTransport
 * (mirrors brain's `registerTopicTools` test seam at
 * `skillprompting/brain/src/mcp-topic-server.ts`).
 */
export function registerTools(mcp: McpServer, registry: TokenRegistry, log: Logger): void {
  const tokenField = z.string();

  // Defers a teardown to the next event-loop tick so the MCP tool
  // response is flushed back to the CLI before the subprocess gets
  // SIGTERM. Mirrors brain's `setTimeout(..., 0)` pattern.
  const deferTeardown = (td: () => Promise<void>): void => {
    setTimeout(() => {
      void td().catch((err) => {
        log.warn({ error: String(err) }, "internal-mcp teardown failed");
      });
    }, 0);
  };

  // Centralized invocation log. Fires once per tool call AFTER token
  // lookup (so we can log the rimsky-side runId rather than the raw
  // token, which is the auth secret). Tool args themselves are not
  // logged: `attributes_set` deltas, `report_complete` change_summary,
  // and `report_error` payloads can carry agent-generated text we
  // shouldn't drop into the executor's log stream verbatim.
  const logCall = (name: string, runId: string): void => {
    log.info({ tool: name, run_id: runId }, "mcp.tool_called");
  };
  const unknownToken = (name: string) => {
    log.warn({ tool: name }, "mcp.unknown_token");
    return {
      content: [{ type: "text" as const, text: "unknown_token" }],
      isError: true,
    };
  };

  mcp.tool(
    "report_complete",
    "Report successful completion of this dispatch. Call exactly once at the end of the run. " +
      "`changed: true` if the work modified files; `changed: false` for no-op reports. " +
      "Optional `attributes_delta` carries the terminal-final attribute writeback (omit if you " +
      "already used incremental `attributes_set` calls).",
    {
      token: tokenField,
      attributes_delta: z.record(z.unknown()).optional(),
      changed: z.boolean(),
      change_summary: z.string().nullable().optional(),
    },
    async (args) => {
      const entry = registry.lookup(args.token);
      if (!entry) return unknownToken("report_complete");
      logCall("report_complete", entry.runId);
      const outcome = await entry.onComplete(
        args.attributes_delta ?? null,
        args.changed,
        args.change_summary ?? null,
        deferTeardown,
      );
      return {
        content: [{ type: "text" as const, text: JSON.stringify(outcome) }],
      };
    },
  );

  mcp.tool(
    "report_blocked",
    "Report that work cannot continue (e.g. waiting on an external signal). Treated as agent_blocked.",
    {
      token: tokenField,
      reason: z.string(),
      context: z.unknown().optional(),
    },
    async (args) => {
      const entry = registry.lookup(args.token);
      if (!entry) return unknownToken("report_blocked");
      logCall("report_blocked", entry.runId);
      await entry.onBlocked(args.reason, args.context ?? null, deferTeardown);
      const ack = { status: "accepted" as const };
      return {
        content: [{ type: "text" as const, text: JSON.stringify(ack) }],
      };
    },
  );

  mcp.tool(
    "report_error",
    "Report a terminal error. The dispatch is failed with the supplied error_class.",
    {
      token: tokenField,
      error_class: z.string(),
      payload: z.unknown().optional(),
    },
    async (args) => {
      const entry = registry.lookup(args.token);
      if (!entry) return unknownToken("report_error");
      logCall("report_error", entry.runId);
      await entry.onError(args.error_class, args.payload ?? null, deferTeardown);
      const ack = { status: "accepted" as const };
      return {
        content: [{ type: "text" as const, text: JSON.stringify(ack) }],
      };
    },
  );

  mcp.tool(
    "report_park",
    "Park the dispatch. The supervisor pauses the node until resume_at " +
      "elapses or an invalidate wakes it. ParkReason is the closed " +
      "two-value set (await_callback | snooze) per spec " +
      ".ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md.",
    {
      token: tokenField,
      reason: z.enum([
        "await_callback",
        "snooze",
      ]),
      reason_note: z.string().optional(),
      resume_at: z.string().optional(),
    },
    async (args) => {
      const entry = registry.lookup(args.token);
      if (!entry) return unknownToken("report_park");
      logCall("report_park", entry.runId);
      if (!entry.onPark) {
        return {
          content: [
            {
              type: "text" as const,
              text: "park_not_supported",
            },
          ],
          isError: true,
        };
      }
      await entry.onPark(
        args.reason,
        args.reason_note ?? null,
        args.resume_at ?? null,
        deferTeardown,
      );
      const ack = { status: "accepted" as const };
      return {
        content: [{ type: "text" as const, text: JSON.stringify(ack) }],
      };
    },
  );

  mcp.tool(
    "attributes_read",
    "Read the per-run attributes object as captured at executor spawn. " +
      "Returns the same snapshot for the duration of the run.",
    {
      token: tokenField,
    },
    async (args) => {
      const entry = registry.lookup(args.token);
      if (!entry) return unknownToken("attributes_read");
      logCall("attributes_read", entry.runId);
      const snapshot = entry.attributesAtSpawn;
      return {
        content: [{ type: "text" as const, text: JSON.stringify(snapshot) }],
      };
    },
  );

  mcp.tool(
    "attributes_set",
    "Persist attribute writes to the supervisor via the incremental writeback callback. " +
      "Body is shaped {delta: {field: value, ...}}; the supervisor merges into " +
      "rimsky_node_attributes.data and persists.",
    {
      token: tokenField,
      delta: z.record(z.unknown()),
    },
    async (args) => {
      const entry = registry.lookup(args.token);
      if (!entry) return unknownToken("attributes_set");
      logCall("attributes_set", entry.runId);
      const result = await entry.onAttributesSet(args.delta);
      const ok = result.status >= 200 && result.status < 300;
      const body = ok
        ? { status: "accepted" as const, http_status: result.status }
        : { status: "rejected" as const, http_status: result.status };
      return {
        content: [{ type: "text" as const, text: JSON.stringify(body) }],
        isError: !ok,
      };
    },
  );

  mcp.tool(
    "emit_named_event",
    "Emit a non-terminal named event. The name must be one the executor " +
      "declares (RIMSKY_EXECUTOR_DECLARED_EVENTS); an undeclared name is " +
      "rejected. The payload is an opaque JSON value carried through " +
      "verbatim. Emitting an event does not end the run — you must still " +
      "call report_complete / report_blocked / report_error / report_park.",
    {
      token: tokenField,
      name: z.string(),
      // Inert payload (concept:inertness / @blessed-invariant 21): the
      // handler serializes it to bytes opaquely and buffers it; it never
      // logs, formats, validates-beyond-serialization, or transforms it.
      payload: z.unknown().optional(),
    },
    async (args) => {
      const entry = registry.lookup(args.token);
      if (!entry) return unknownToken("emit_named_event");
      logCall("emit_named_event", entry.runId);
      // Self-consistency guard (NOT rimsky access): reject any name the
      // executor does not declare. Rimsky would otherwise persist an
      // undeclared name as a downstream no-op, so this is early feedback
      // for the template author, not a correctness gate. (Args are not
      // logged: the rejected `name` could carry agent-generated text.)
      if (!entry.declaredEvents.includes(args.name)) {
        return {
          content: [
            {
              type: "text" as const,
              text: JSON.stringify({
                status: "rejected",
                error: "undeclared_event",
                declared_events: entry.declaredEvents,
              }),
            },
          ],
          isError: true,
        };
      }
      // Serialize the payload to bytes opaquely. `undefined` (payload
      // omitted) serializes to the JSON literal `null` so the wire carries
      // a well-formed value; the executor does not inspect it further.
      const payloadBytes = Buffer.from(
        JSON.stringify(args.payload ?? null),
        "utf8",
      );
      entry.emitNamedEvent(args.name, payloadBytes);
      const ack = { status: "accepted" as const };
      return {
        content: [{ type: "text" as const, text: JSON.stringify(ack) }],
      };
    },
  );
}
