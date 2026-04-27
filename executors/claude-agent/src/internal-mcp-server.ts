import http from "node:http";
import type { AddressInfo } from "node:net";
import type { Logger } from "pino";
import { TokenRegistry } from "./token-registry.js";
import {
  TOOL_DEFINITIONS,
  ReportCompleteInput,
  ReportBlockedInput,
  ReportErrorInput,
  AttributesReadInput,
  AttributesSetInput,
} from "./internal-mcp-tools.js";

/**
 * Plain JSON-RPC 2.0 callback endpoint for agentic subprocesses.
 *
 * @source rimsky/src/callback-mcp/server.ts
 *
 * v1 implementation chooses a bare HTTP/JSON-RPC endpoint over the MCP SDK's
 * Streamable-HTTP transport. The executor spawns Claude CLI which speaks
 * MCP-HTTP; only a simple tools/list + tools/call surface is needed.
 *
 * Tools surfaced (per spec §12 and §16.1):
 *   - `report_complete` (optional `attributes_delta`)
 *   - `report_blocked`
 *   - `report_error`
 *   - `attributes_read`  — returns dispatch-time attributes snapshot.
 *   - `attributes_set`   — POSTs `{delta}` to the supervisor's incremental
 *     writeback URL.
 */
export interface CallbackServerHandle {
  readonly host: string;
  readonly port: number;
  readonly url: string;
  readonly registry: TokenRegistry;
  close(): Promise<void>;
}

type ToolResult = {
  content: Array<{ type: "text"; text: string }>;
  isError?: boolean;
  structuredContent?: unknown;
};

interface JsonRpcBody {
  jsonrpc?: string;
  id?: unknown;
  method?: string;
  params?: { name?: string; arguments?: unknown };
}

/**
 * Maximum accepted request body size (bytes). The callback endpoint is
 * loopback-only and trusts the spawned CLI, but a runaway subprocess could
 * still flood the server with megabytes of JSON; this cap bounds peak memory
 * per request.
 */
const MAX_BODY_BYTES = 4 * 1024 * 1024; // 4 MiB

export async function startInternalMcpServer(opts: {
  host?: string;
  port?: number;
  logger: Logger;
}): Promise<CallbackServerHandle> {
  const registry = new TokenRegistry();
  const log = opts.logger.child({ component: "internal-mcp" });

  const server = http.createServer((req, res) => {
    if (req.method !== "POST" || !req.url?.startsWith("/mcp")) {
      res.statusCode = 404;
      res.end("not found");
      return;
    }

    const chunks: Buffer[] = [];
    let total = 0;
    let oversized = false;
    req.on("data", (c) => {
      if (oversized) return;
      const buf = Buffer.from(c);
      total += buf.length;
      if (total > MAX_BODY_BYTES) {
        oversized = true;
        return;
      }
      chunks.push(buf);
    });
    req.on("end", () => {
      if (oversized) {
        sendJson(res, 413, {
          jsonrpc: "2.0",
          id: null,
          error: {
            code: -32600,
            message: `request body exceeds ${MAX_BODY_BYTES} bytes`,
          },
        });
        return;
      }
      void handleRequest(res, Buffer.concat(chunks), registry, log);
    });
  });

  const host = opts.host ?? "127.0.0.1";
  const port = opts.port ?? 0;
  await new Promise<void>((resolve, reject) => {
    const onErr = (err: Error) => reject(err);
    server.once("error", onErr);
    server.listen(port, host, () => {
      server.off("error", onErr);
      resolve();
    });
  });

  const address = server.address() as AddressInfo;
  const actualPort = address.port;

  return {
    host,
    port: actualPort,
    url: `http://${host}:${actualPort}/mcp`,
    registry,
    close: () =>
      new Promise<void>((resolve, reject) => {
        server.close((err) => (err ? reject(err) : resolve()));
      }),
  };
}

async function handleRequest(
  res: http.ServerResponse,
  rawBody: Buffer,
  registry: TokenRegistry,
  log: Logger,
): Promise<void> {
  let body: JsonRpcBody;
  try {
    body = JSON.parse(rawBody.toString("utf-8")) as JsonRpcBody;
  } catch {
    sendJson(res, 400, {
      jsonrpc: "2.0",
      id: null,
      error: { code: -32700, message: "parse error" },
    });
    return;
  }

  const id = body.id ?? null;

  if (body.jsonrpc !== "2.0" || typeof body.method !== "string") {
    sendJson(res, 200, {
      jsonrpc: "2.0",
      id,
      error: { code: -32600, message: "invalid request" },
    });
    return;
  }

  const teardowns: Array<() => Promise<void>> = [];
  const scheduleTeardown = (td: () => Promise<void>) => {
    teardowns.push(td);
  };

  let response: unknown;
  try {
    if (body.method === "tools/list") {
      response = { tools: TOOL_DEFINITIONS };
    } else if (body.method === "tools/call") {
      response = await dispatchToolCall(
        registry,
        body.params?.name,
        body.params?.arguments,
        scheduleTeardown,
      );
    } else {
      sendJson(res, 200, {
        jsonrpc: "2.0",
        id,
        error: { code: -32601, message: "method not found" },
      });
      return;
    }
  } catch (e) {
    log.error({ error: String(e) }, "internal-mcp dispatch failed");
    sendJson(res, 200, {
      jsonrpc: "2.0",
      id,
      error: { code: -32000, message: String(e) },
    });
    return;
  }

  const payload = JSON.stringify({ jsonrpc: "2.0", id, result: response });
  res.statusCode = 200;
  res.setHeader("Content-Type", "application/json");
  res.setHeader("Content-Length", String(Buffer.byteLength(payload)));

  let teardownsRan = false;
  const runTeardowns = (): void => {
    if (teardownsRan) return;
    teardownsRan = true;
    void Promise.allSettled(teardowns.map((t) => t())).catch(() => {
      /* allSettled never rejects. */
    });
  };
  res.on("finish", runTeardowns);
  res.on("close", runTeardowns);

  res.end(payload);
}

function sendJson(
  res: http.ServerResponse,
  status: number,
  body: unknown,
): void {
  const payload = JSON.stringify(body);
  res.statusCode = status;
  res.setHeader("Content-Type", "application/json");
  res.setHeader("Content-Length", String(Buffer.byteLength(payload)));
  res.end(payload);
}

async function dispatchToolCall(
  registry: TokenRegistry,
  name: string | undefined,
  args: unknown,
  scheduleTeardown: (td: () => Promise<void>) => void,
): Promise<ToolResult> {
  if (name === "report_complete") {
    const parsed = ReportCompleteInput.parse(args);
    const entry = registry.lookup(parsed.token);
    if (!entry) {
      return {
        content: [{ type: "text", text: "unknown_token" }],
        isError: true,
      };
    }
    const outcome = await entry.onComplete(
      parsed.attributes_delta ?? null,
      parsed.changed,
      parsed.change_summary ?? null,
      scheduleTeardown,
    );
    return {
      content: [{ type: "text", text: JSON.stringify(outcome) }],
      structuredContent: outcome,
    };
  }

  if (name === "report_blocked") {
    const parsed = ReportBlockedInput.parse(args);
    const entry = registry.lookup(parsed.token);
    if (!entry) {
      return {
        content: [{ type: "text", text: "unknown_token" }],
        isError: true,
      };
    }
    await entry.onBlocked(
      parsed.reason,
      parsed.context ?? null,
      scheduleTeardown,
    );
    const ack = { status: "accepted" as const };
    return {
      content: [{ type: "text", text: JSON.stringify(ack) }],
      structuredContent: ack,
    };
  }

  if (name === "report_error") {
    const parsed = ReportErrorInput.parse(args);
    const entry = registry.lookup(parsed.token);
    if (!entry) {
      return {
        content: [{ type: "text", text: "unknown_token" }],
        isError: true,
      };
    }
    await entry.onError(
      parsed.error_class,
      parsed.payload ?? null,
      scheduleTeardown,
    );
    const ack = { status: "accepted" as const };
    return {
      content: [{ type: "text", text: JSON.stringify(ack) }],
      structuredContent: ack,
    };
  }

  if (name === "attributes_read") {
    const parsed = AttributesReadInput.parse(args);
    const entry = registry.lookup(parsed.token);
    if (!entry) {
      return {
        content: [{ type: "text", text: "unknown_token" }],
        isError: true,
      };
    }
    const snapshot = entry.attributesAtSpawn;
    return {
      content: [{ type: "text", text: JSON.stringify(snapshot) }],
      structuredContent: { attributes: snapshot },
    };
  }

  if (name === "attributes_set") {
    const parsed = AttributesSetInput.parse(args);
    const entry = registry.lookup(parsed.token);
    if (!entry) {
      return {
        content: [{ type: "text", text: "unknown_token" }],
        isError: true,
      };
    }
    const result = await entry.onAttributesSet(parsed.delta);
    const ok = result.status >= 200 && result.status < 300;
    const body = ok
      ? { status: "accepted" as const, http_status: result.status }
      : { status: "rejected" as const, http_status: result.status };
    return {
      content: [{ type: "text", text: JSON.stringify(body) }],
      structuredContent: body,
      isError: !ok,
    };
  }

  throw new Error(`unknown tool: ${name}`);
}
