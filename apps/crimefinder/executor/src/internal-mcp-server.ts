import http from "node:http";
import crypto from "node:crypto";
import os from "node:os";
import path from "node:path";
import fs from "node:fs/promises";
import type { AddressInfo } from "node:net";
import type { Logger } from "pino";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { z } from "zod";
import { GateError } from "@crimefinder/shared";
import { McpTokenRegistry } from "./token-registry.js";
import { TOOL_DEFINITIONS as ALL_TOOL_DEFINITIONS } from "./internal-mcp-tools.js";

// DispatchFn is the executor-side handler the MCP server calls. Auth has
// moved to the HTTP transport (an Authorization: Bearer <token> header
// the runner injects via the MCP client config's `headers:` block), so
// the dispatcher no longer takes a token field — by the time we get
// here, the request was already authenticated.
export type DispatchFn = (
  toolName: string,
  input: unknown,
) => Promise<unknown>;

// Touch callback invoked on every authenticated tool call so the
// silence-watch resets even when the agent is silent on stdout (long
// MCP-driven sessions would otherwise false-trigger silence_timeout).
export type TouchFn = () => void;

export interface McpServerHandle {
  port: number;
  host: string;
  baseUrl: string;
  url: string;
  mcpConfigPath: string;
  registry: McpTokenRegistry;
  token: string;
  close(): Promise<void>;
}

export interface McpServerOptions {
  host?: string;
  logger: Logger;
  dispatch: DispatchFn;
  registry?: McpTokenRegistry;
  // Run ID the issued bearer-token is associated with.
  runId: string;
  // Reset the silence-watch on every authenticated tool call.
  onToolCall?: TouchFn;
}

function asTextResponse(value: unknown): { content: Array<{ type: "text"; text: string }> } {
  return { content: [{ type: "text", text: JSON.stringify(value) }] };
}

function asError(message: string, extra: Record<string, unknown> = {}): {
  content: Array<{ type: "text"; text: string }>;
  isError: true;
} {
  return {
    content: [{ type: "text", text: JSON.stringify({ error: message, ...extra }) }],
    isError: true,
  };
}

// MCP-level tool handler. Auth happens at the HTTP transport (a Bearer
// header check before we hand the request to the MCP server), so the
// per-tool handler doesn't touch the token any more.
function makeHandler<T>(
  schema: z.ZodType<T>,
  toolName: string,
  dispatch: DispatchFn,
  logger: Logger,
) {
  return async (args: unknown) => {
    const parsed = schema.safeParse(args);
    if (!parsed.success) {
      logger.warn({ tool: toolName, err: parsed.error.message }, "mcp_input_invalid");
      return asError("invalid_input", { detail: parsed.error.flatten() });
    }
    try {
      const result = await dispatch(toolName, parsed.data);
      return asTextResponse(result);
    } catch (e) {
      if (e instanceof GateError) {
        return asError(e.envelope.data.crimefinder_error_class, {
          message: e.envelope.message,
          ...e.envelope.data,
        });
      }
      logger.error({ tool: toolName, err: String(e) }, "mcp_dispatch_failed");
      return asError("internal_error", { detail: String(e) });
    }
  };
}

// Tool metadata is the single source of truth in `TOOL_DEFINITIONS`. This
// function iterates over it and registers each MCP tool from one place,
// so descriptions/schemas can't drift between the two surfaces.
function registerTools(
  mcp: McpServer,
  dispatch: DispatchFn,
  logger: Logger,
): void {
  for (const def of ALL_TOOL_DEFINITIONS) {
    // Each TOOL_DEFINITIONS entry has an inputSchema that's a ZodObject; the
    // MCP server wants the per-field shape, not the wrapper.
    const schema = def.inputSchema as z.ZodObject<z.ZodRawShape>;
    mcp.registerTool(
      def.name,
      {
        description: def.description,
        inputSchema: schema.shape,
      },
      makeHandler(schema, def.name, dispatch, logger),
    );
  }
}

export async function startInternalMcpServer(opts: McpServerOptions): Promise<McpServerHandle> {
  const registry = opts.registry ?? new McpTokenRegistry();
  const logger = opts.logger.child({ component: "internal-mcp" });
  const mcp = new McpServer({ name: "crimefinder-callback", version: "1.0.0" });
  // Wrap dispatch to fire the silence-watch touch callback on every
  // authenticated tool invocation (#18).
  const wrappedDispatch: DispatchFn = async (tool, input) => {
    opts.onToolCall?.();
    return opts.dispatch(tool, input);
  };
  registerTools(mcp, wrappedDispatch, logger);

  // Mint a single per-run bearer-token and require it on every HTTP
  // request. This is the auth boundary: by the time a tool handler runs,
  // the request has already been authenticated at the transport.
  const bearer = registry.issue(opts.runId);

  let transport: StreamableHTTPServerTransport | null = null;
  const httpServer = http.createServer(async (req, res) => {
    try {
      // Health is unauthenticated for liveness probes.
      if (req.method === "GET" && req.url === "/healthz") {
        res.statusCode = 200;
        res.end("ok");
        return;
      }
      const authHeader = req.headers["authorization"] ?? "";
      const header = Array.isArray(authHeader) ? authHeader[0] : authHeader;
      const presented = header.toLowerCase().startsWith("bearer ")
        ? header.slice(7).trim()
        : "";
      if (!presented || !registry.validate(presented) || presented !== bearer) {
        logger.warn(
          { hasHeader: Boolean(header), method: req.method, url: req.url },
          "mcp_unauthenticated_request",
        );
        res.statusCode = 401;
        res.setHeader("content-type", "application/json");
        res.end(JSON.stringify({ error: "unknown_token" }));
        return;
      }
      if (!transport) {
        transport = new StreamableHTTPServerTransport({
          sessionIdGenerator: () => crypto.randomUUID(),
        });
        await mcp.connect(transport);
      }
      await transport.handleRequest(req, res);
    } catch (e) {
      logger.error({ err: String(e) }, "mcp_request_failed");
      if (!res.headersSent) {
        res.statusCode = 500;
        res.end("internal mcp error");
      }
    }
  });
  const host = opts.host ?? "127.0.0.1";
  await new Promise<void>((resolve, reject) => {
    httpServer.once("error", reject);
    httpServer.listen(0, host, () => {
      httpServer.off("error", reject);
      resolve();
    });
  });
  const addr = httpServer.address() as AddressInfo;
  const url = `http://${host}:${addr.port}/mcp`;
  const baseUrl = `http://${host}:${addr.port}`;

  // Write a temp MCP config file referencing this URL with the bearer
  // header pre-baked, so claude CLI dials with the right Authorization.
  const cfgPath = path.join(os.tmpdir(), `crimefinder-mcp-${crypto.randomUUID()}.json`);
  await fs.writeFile(
    cfgPath,
    JSON.stringify({
      mcpServers: {
        crimefinder: {
          type: "http",
          url,
          headers: {
            Authorization: `Bearer ${bearer}`,
          },
        },
      },
    }),
    "utf-8",
  );

  return {
    host,
    port: addr.port,
    url,
    baseUrl,
    mcpConfigPath: cfgPath,
    registry,
    token: bearer,
    async close() {
      registry.revoke(bearer);
      if (transport) {
        try {
          await transport.close();
        } catch {
          // ignore
        }
      }
      try {
        await mcp.close();
      } catch {
        // ignore
      }
      await new Promise<void>((resolve, reject) => {
        httpServer.close((err) => (err ? reject(err) : resolve()));
      });
      try {
        await fs.unlink(cfgPath);
      } catch {
        // ignore
      }
    },
  };
}

// Re-export TOOL_DEFINITIONS so consumers that only import the server file
// can still introspect the vocabulary without reaching into the tools module.
export { TOOL_DEFINITIONS } from "./internal-mcp-tools.js";
