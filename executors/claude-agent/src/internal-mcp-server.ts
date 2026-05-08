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
 * Implementation: speaks the MCP "Streamable HTTP" transport via
 * `@modelcontextprotocol/sdk`. The Claude Code CLI's MCP-HTTP client
 * requires the standard MCP handshake (`initialize`, `notifications/...`,
 * etc.); a hand-rolled bare JSON-RPC endpoint silently fails to surface
 * its tools to the CLI, which is why the v1 implementation here was
 * replaced with the SDK-driven one.
 *
 * @source skillprompting/brain/src/mcp-topic-server.ts (working
 * production reference for the same spawn-claude → MCP-HTTP loop).
 *
 * Tools surfaced (per spec §12 and §16.1):
 *   - `report_complete` (optional `attributes_delta`)
 *   - `report_blocked`
 *   - `report_error`
 *   - `attributes_read`  — returns dispatch-time attributes snapshot.
 *   - `attributes_set`   — POSTs `{delta}` to the supervisor's incremental
 *     writeback URL.
 *
 * Tools are scoped per-run via the per-run `token` argument validated
 * against `TokenRegistry`. The CLI subprocess receives the token via the
 * `RIMSKY_CALLBACK_TOKEN` env var.
 *
 * Teardown deferral: tool handlers that drive the dispatch terminal
 * (`report_complete` / `report_blocked` / `report_error`) hand teardownCli
 * to a `setTimeout(..., 0)` so the MCP tool response is flushed back to
 * the CLI before the subprocess gets SIGTERM. This mirrors brain's
 * `setTimeout(() => config.onTopicPublished(result), 0)` pattern.
 */
export interface CallbackServerHandle {
  readonly host: string;
  readonly port: number;
  readonly url: string;
  readonly registry: TokenRegistry;
  close(): Promise<void>;
}

export async function startInternalMcpServer(opts: {
  host?: string;
  port?: number;
  logger: Logger;
}): Promise<CallbackServerHandle> {
  const registry = new TokenRegistry();
  const log = opts.logger.child({ component: "internal-mcp" });

  const mcp = new McpServer({
    name: "rimsky-callback",
    version: "1.0.0",
  });

  registerTools(mcp, registry, log);

  let transport: StreamableHTTPServerTransport | null = null;
  const httpServer = http.createServer(async (req, res) => {
    try {
      if (!transport) {
        transport = new StreamableHTTPServerTransport({
          sessionIdGenerator: () => crypto.randomUUID(),
        });
        await mcp.connect(transport);
      }
      await transport.handleRequest(req, res);
    } catch (err) {
      log.error({ error: String(err) }, "internal-mcp request failed");
      if (!res.headersSent) {
        res.statusCode = 500;
        res.end("internal mcp error");
      }
    }
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

  return {
    host,
    port: actualPort,
    url: `http://${host}:${actualPort}/mcp`,
    registry,
    close: async () => {
      if (transport) {
        try {
          await transport.close();
        } catch {
          /* ignore */
        }
      }
      try {
        await mcp.close();
      } catch {
        /* ignore */
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
      if (!entry) {
        return {
          content: [{ type: "text" as const, text: "unknown_token" }],
          isError: true,
        };
      }
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
      if (!entry) {
        return {
          content: [{ type: "text" as const, text: "unknown_token" }],
          isError: true,
        };
      }
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
      if (!entry) {
        return {
          content: [{ type: "text" as const, text: "unknown_token" }],
          isError: true,
        };
      }
      await entry.onError(args.error_class, args.payload ?? null, deferTeardown);
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
      if (!entry) {
        return {
          content: [{ type: "text" as const, text: "unknown_token" }],
          isError: true,
        };
      }
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
      if (!entry) {
        return {
          content: [{ type: "text" as const, text: "unknown_token" }],
          isError: true,
        };
      }
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
