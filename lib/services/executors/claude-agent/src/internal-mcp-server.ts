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
  sessionIdleMs?: number;
  socketTimeoutMs?: number;
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
    void entry.transport.close().catch(() => {});
    void entry.mcp.close().catch(() => {});
  };

  const createSession = async (): Promise<SessionEntry> => {
    const mcp = new McpServer({
      name: CALLBACK_MCP_SERVER_NAME,
      version: "1.0.0",
    });
    registerTools(mcp, registry, log);
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

  if (opts.socketTimeoutMs !== undefined) {
    httpServer.timeout = opts.socketTimeoutMs;
  }

  httpServer.timeout = 0;
  httpServer.requestTimeout = 0;
  httpServer.keepAliveTimeout = 24 * 60 * 60 * 1000;
  httpServer.headersTimeout = 24 * 60 * 60 * 1000;

  httpServer.on("clientError", (err, socket) => {
    log.warn({ error: String(err) }, "mcp.client_error");
    try {
      if (!socket.destroyed && socket.writable) {
        socket.end("HTTP/1.1 400 Bad Request\r\n\r\n");
      } else {
        socket.destroy();
      }
    } catch (endErr) {
      log.debug({ error: String(endErr) }, "mcp.client_error_end_failed");
    }
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

  const sweepIntervalMs = Math.max(30_000, Math.floor(sessionIdleMs / 4));
  const sweepTimer = setInterval(() => {
    const cutoff = Date.now() - sessionIdleMs;
    for (const [sid, entry] of sessions) {
      if (entry.lastActivityAt < cutoff) evict(sid, "idle_timeout");
    }
  }, sweepIntervalMs);
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

export function registerTools(mcp: McpServer, registry: TokenRegistry, log: Logger): void {
  const tokenField = z.string();

  const deferTeardown = (td: () => Promise<void>): void => {
    setTimeout(() => {
      void td().catch((err) => {
        log.warn({ error: String(err) }, "internal-mcp teardown failed");
      });
    }, 0);
  };

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
      signoffs: z.array(z.string()).optional(),
    },
    async (args) => {
      const entry = registry.lookup(args.token);
      if (!entry) return unknownToken("report_complete");
      logCall("report_complete", entry.runId);
      const outcome = await entry.onComplete(
        args.attributes_delta ?? null,
        args.changed,
        args.change_summary ?? null,
        args.signoffs ?? null,
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
    "dispatch_context_read",
    "Read the dispatch's identity and disposition as captured at executor " +
      "spawn. Returns dispatch_id and run_scope_id (always present as " +
      "strings, possibly empty in non-supervised invocations), and " +
      "prior_dispatch_id + prior_dispatch_disposition (one of " +
      "stale_recovery / retry_after_error / recalculate) when this dispatch " +
      "supersedes a predecessor. Returns the same snapshot for the " +
      "duration of the run.",
    {
      token: tokenField,
    },
    async (args) => {
      const entry = registry.lookup(args.token);
      if (!entry) return unknownToken("dispatch_context_read");
      logCall("dispatch_context_read", entry.runId);
      return {
        content: [
          { type: "text" as const, text: JSON.stringify(entry.dispatchContext) },
        ],
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

}
