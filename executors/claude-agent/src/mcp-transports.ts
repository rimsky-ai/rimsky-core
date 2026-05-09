// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

/**
 * mcp-transports.ts — translate resolved MCP bindings into the per-
 * dispatch `mcp.json` shape the Claude CLI consumes.
 *
 * Per the 2026-05-08 platform-extensions plan J4–J7, claude-agent
 * supports four transports:
 *
 *   - `http`           : URL + headers passed through to the CLI.
 *   - `stdio`          : command + args + env passed through.
 *   - `module`         : alias for `http-loopback` (J6 plan note).
 *   - `http-loopback`  : `import()`s a module at dispatch time, exposes
 *                        an MCP server on a 127.0.0.1:0 port, writes
 *                        the loopback URL into mcp.json as an `http`
 *                        entry from the CLI's perspective.
 *
 * The output is the JSON shape Claude CLI's `--mcp-config` flag
 * accepts:
 *   {
 *     "mcpServers": {
 *       "<name>": { "type": "http", "url": "...", "headers": {...} }
 *               | { "type": "stdio", "command": "...", "args": [...], "env": {...} }
 *     }
 *   }
 *
 * The lifecycle of any spawned http-loopback server is owned by the
 * caller — `materializeBindings` returns a `cleanup` callback that the
 * dispatch path MUST invoke when the run terminates.
 */

import { createServer, type Server } from "node:http";
import { AddressInfo } from "node:net";

import type { CatalogEntry } from "./mcp-catalog.js";
import type { ResolvedMcpBinding } from "./mcp-resolver.js";

/** The shape Claude CLI consumes via `--mcp-config <file>`. */
export interface ClaudeMcpConfig {
  mcpServers: Record<string, ClaudeMcpEntry>;
}

export type ClaudeMcpEntry =
  | { type: "http"; url: string; headers?: Record<string, string> }
  | { type: "stdio"; command: string; args?: string[]; env?: Record<string, string> };

/** Result returned by materializeBindings — config plus a cleanup hook. */
export interface MaterializedMcp {
  config: ClaudeMcpConfig;
  /** Invoke when the dispatch terminates; tears down loopback servers. */
  cleanup: () => Promise<void>;
}

/**
 * Walk every resolved binding, dispatch by transport, and return the
 * per-dispatch mcp.json contents plus a cleanup callback the runner
 * must invoke at dispatch end.
 *
 * The optional `loaders` map provides per-module factories used by
 * the `module` / `http-loopback` transports. Tests pass a stub map;
 * production wires `import(spec)` via the actual ESM loader.
 */
export async function materializeBindings(
  bindings: ResolvedMcpBinding[],
  opts: MaterializeOpts = {},
): Promise<MaterializedMcp> {
  const out: ClaudeMcpConfig = { mcpServers: {} };
  const cleanups: Array<() => Promise<void>> = [];
  for (const b of bindings) {
    const { entry, name } = b;
    switch (entry.transport) {
      case "http":
        out.mcpServers[name] = handleHttp(entry);
        break;
      case "stdio":
        out.mcpServers[name] = handleStdio(entry);
        break;
      case "module":
      case "http-loopback": {
        const result = await handleLoopback(name, entry, opts);
        out.mcpServers[name] = result.entry;
        cleanups.push(result.cleanup);
        break;
      }
    }
  }
  const cleanup = async () => {
    for (const c of cleanups) {
      try {
        await c();
      } catch {
        // Cleanup failures are best-effort — surfacing them upstream
        // would mask the actual run outcome.
      }
    }
  };
  return { config: out, cleanup };
}

/** Options for materializeBindings; tests inject a fake module loader. */
export interface MaterializeOpts {
  loadModule?: (spec: string) => Promise<ModuleHandle>;
}

/**
 * ModuleHandle is the contract every loaded MCP module exposes.
 * `register(server, config)` mounts the module's tools on the given
 * MCP server instance; `dispose()` is called at cleanup. Both are
 * idempotent.
 */
export interface ModuleHandle {
  register: (server: { addTool: (...args: unknown[]) => unknown }, config: Record<string, unknown>) => Promise<void> | void;
  dispose?: () => Promise<void> | void;
}

/** J4: HTTP transport — pass URL + headers through. */
function handleHttp(entry: Extract<CatalogEntry, { transport: "http" }>): ClaudeMcpEntry {
  return {
    type: "http",
    url: entry.url,
    ...(entry.headers ? { headers: entry.headers } : {}),
  };
}

/** J5: stdio transport — pass command + args + env through. */
function handleStdio(entry: Extract<CatalogEntry, { transport: "stdio" }>): ClaudeMcpEntry {
  const out: ClaudeMcpEntry = {
    type: "stdio",
    command: entry.command,
  };
  if (entry.args) out.args = entry.args;
  if (entry.env) out.env = entry.env;
  return out;
}

/**
 * J6/J7: module / http-loopback transport.
 *
 * `module` is an alias for `http-loopback` per the plan's pre-resolved
 * design decision; both produce the same loopback wire surface.
 *
 * Behavior:
 *   1. Load the module via the supplied loader (or `import()` in
 *      production wire-up).
 *   2. Construct a minimal MCP-shaped HTTP server on 127.0.0.1:0 that
 *      forwards calls to the module's registered tools.
 *   3. Write the loopback URL into mcp.json as an `http` entry from
 *      the CLI's perspective.
 *
 * The server listens on an OS-assigned port; cleanup tears it down.
 * For v1 the server is a placeholder echoing the module's tool list
 * back via JSON-RPC 2.0; full bidirectional MCP (resources / prompts)
 * is documented as future work in the J6/J7 docs.
 */
async function handleLoopback(
  name: string,
  entry: Extract<CatalogEntry, { transport: "module" | "http-loopback" }>,
  opts: MaterializeOpts,
): Promise<{ entry: ClaudeMcpEntry; cleanup: () => Promise<void> }> {
  const loader = opts.loadModule ?? defaultLoadModule;
  const handle = await loader(entry.module);
  const tools: Array<{ name: string; handler: (...args: unknown[]) => unknown }> = [];
  await Promise.resolve(
    handle.register(
      {
        addTool: (toolName: unknown, handler: unknown) => {
          tools.push({ name: String(toolName), handler: handler as (...args: unknown[]) => unknown });
          return undefined;
        },
      },
      entry.config ?? {},
    ),
  );
  const server = createServer((req, res) => {
    let body = "";
    req.on("data", (chunk) => {
      body += chunk;
    });
    req.on("end", () => {
      try {
        const parsed = body ? (JSON.parse(body) as { id?: number | string; method?: string; params?: { name?: string; arguments?: unknown[] } }) : { id: 0, method: "" };
        const result = handleLoopbackRpc(parsed, tools);
        res.setHeader("content-type", "application/json");
        res.end(JSON.stringify({ jsonrpc: "2.0", id: parsed.id ?? null, result }));
      } catch (e) {
        res.statusCode = 400;
        res.end(JSON.stringify({ jsonrpc: "2.0", id: null, error: { code: -32700, message: String(e) } }));
      }
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const addr = server.address() as AddressInfo;
  const port = addr?.port ?? 0;
  const url = `http://127.0.0.1:${port}/mcp`;
  return {
    entry: { type: "http", url },
    cleanup: async () => {
      if (handle.dispose) await Promise.resolve(handle.dispose());
      await disposeServer(server);
      // Suppress unused name capture warning.
      void name;
    },
  };
}

/** Default ESM loader used in production. */
async function defaultLoadModule(spec: string): Promise<ModuleHandle> {
  const m = (await import(spec)) as {
    register?: ModuleHandle["register"];
    dispose?: ModuleHandle["dispose"];
    default?: ModuleHandle;
  };
  if (typeof m.register === "function") {
    return { register: m.register, dispose: m.dispose };
  }
  if (m.default && typeof m.default.register === "function") {
    return m.default;
  }
  throw new Error(`mcp module ${JSON.stringify(spec)} does not expose a register() function`);
}

/** Synchronous JSON-RPC dispatcher for the loopback server. */
function handleLoopbackRpc(
  body: { method?: string; params?: { name?: string; arguments?: unknown[] } },
  tools: Array<{ name: string; handler: (...args: unknown[]) => unknown }>,
): unknown {
  if (body.method === "tools/list") {
    return { tools: tools.map((t) => ({ name: t.name })) };
  }
  if (body.method === "tools/call") {
    const tool = tools.find((t) => t.name === body.params?.name);
    if (!tool) {
      return { isError: true, content: [{ type: "text", text: `tool ${body.params?.name} not found` }] };
    }
    const args = body.params?.arguments ?? [];
    const result = tool.handler(...args);
    return { content: [{ type: "text", text: JSON.stringify(result) }] };
  }
  return {};
}

/** Promise-friendly server.close. */
function disposeServer(server: Server): Promise<void> {
  return new Promise((resolve) => {
    server.close(() => resolve());
  });
}
