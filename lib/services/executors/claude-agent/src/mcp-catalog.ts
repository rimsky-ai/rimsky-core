// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// @deliberate: startup MCP-server catalog + `allow_inline` policy for the
// claude-agent executor. The operator wires a
// catalog of named MCP servers at startup (env `RIMSKY_EXECUTOR_MCP_CATALOG`
// → a YAML/JSON file) and an `RIMSKY_EXECUTOR_MCP_ALLOW_INLINE` policy
// (default false). A node's `cli.mcp_servers` then references a catalog
// entry by `{ ref: <name> }` rather than declaring an inline
// `{ name, url, headers }` server; inline servers are permitted only when
// the policy allows them. Each catalog entry declares a `transport`:
// `http` is a remote streamable-HTTP MCP server (url + headers); `stdio`
// is a local MCP server spawned as a subprocess (command + args), wired
// into `--mcp-config` as a `type: "stdio"` leaf; `module` is an in-tree
// MCP module the executor `import()`s at dispatch and fronts on a
// per-dispatch loopback HTTP listener (the Claude CLI only speaks MCP
// over a wire transport, never an in-process object); `http-loopback`
// shares the module-loading shape — the name distinguishes operator
// intent (a server explicitly fronted on a loopback HTTP listener) but
// the stand-up mechanism is identical. The catalog + policy are parsed
// ONCE at startup and threaded into every dispatch via `AgentRunOptions`
// (the carrier for `cliConfig`). Resolution of a `{ ref: }` against the
// catalog — and the per-dispatch stand-up of a module / http-loopback
// listener — happens at the single `hostServers` build site in
// `agent-run.ts`.

import { readFileSync } from "node:fs";
import http from "node:http";
import type { AddressInfo } from "node:net";
import { pathToFileURL } from "node:url";
import { dirname, isAbsolute, resolve as resolvePath } from "node:path";
import { fileURLToPath } from "node:url";
import { parse as parseYaml } from "yaml";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import crypto from "node:crypto";
import type { Logger } from "pino";
import { CliConfigError } from "./cli-config-error.js";
import type { CliToolConfig } from "./cli-runner.js";

/** Directory this module lives in — the base for resolving relative
 *  module specifiers in `module` / `http-loopback` catalog entries. A
 *  catalog written by an operator may name a module relative to the
 *  executor's own source tree (the in-tree fixture path the tests use);
 *  resolving against this directory keeps that stable regardless of the
 *  process cwd. Absolute specifiers and `file://` URLs are used verbatim. */
const MODULE_DIR = dirname(fileURLToPath(import.meta.url));

/** A remote streamable-HTTP MCP server reached over the network. */
export interface HttpCatalogEntry {
  transport: "http";
  url: string;
  headers?: Record<string, string>;
  allowedTools?: string[];
}

/** A local MCP server spawned as a subprocess and wired as a stdio leaf. */
export interface StdioCatalogEntry {
  transport: "stdio";
  command: string;
  args?: string[];
  env?: Record<string, string>;
  allowedTools?: string[];
}

/**
 * An in-tree MCP module the executor loads at dispatch. `module` mounts it
 * on a per-dispatch loopback HTTP listener; `http-loopback` does the same
 * (the distinction is operator-facing intent, not a different mechanism —
 * the Claude CLI only speaks MCP over a wire transport, so a `module`
 * entry must also be fronted on a loopback listener to be reachable).
 */
export interface ModuleCatalogEntry {
  transport: "module" | "http-loopback";
  module: string;
  allowedTools?: string[];
}

export type McpCatalogEntry =
  | HttpCatalogEntry
  | StdioCatalogEntry
  | ModuleCatalogEntry;

/** The parsed startup catalog, keyed by server name. */
export type McpCatalog = Record<string, McpCatalogEntry>;

/**
 * Parses the startup catalog file referenced by
 * `RIMSKY_EXECUTOR_MCP_CATALOG`. YAML is a superset of JSON, so one parser
 * handles both `.yml`/`.yaml` and `.json`. Returns an empty catalog when
 * the path is empty/unset.
 *
 * A present-but-malformed catalog throws — a misconfigured catalog must
 * fail the executor LOUDLY at startup rather than silently dropping a
 * server a node will later reference by `{ ref: }` (which would surface as
 * an opaque mid-dispatch resolution error).
 */
export function loadCatalogFromEnv(path: string | undefined): McpCatalog {
  if (!path || path.length === 0) return {};
  let raw: string;
  try {
    raw = readFileSync(path, "utf8");
  } catch (e) {
    throw new Error(`failed to read RIMSKY_EXECUTOR_MCP_CATALOG=${path}: ${String(e)}`);
  }
  const parsed = parseYaml(raw) as unknown;
  return parseCatalog(parsed);
}

/**
 * Validates a parsed catalog object into the typed `McpCatalog`. Each entry
 * must declare a known `transport` plus that transport's required fields.
 * Throws on any malformed entry (fail-loud — a dropped catalog server is a
 * silently-unwired reference).
 */
export function parseCatalog(v: unknown): McpCatalog {
  if (v === undefined || v === null) return {};
  if (typeof v !== "object" || Array.isArray(v)) {
    throw new Error(`mcp catalog must be a map of name→entry, got ${typeof v}`);
  }
  const out: McpCatalog = {};
  for (const [name, rawEntry] of Object.entries(v as Record<string, unknown>)) {
    if (!rawEntry || typeof rawEntry !== "object" || Array.isArray(rawEntry)) {
      throw new Error(`mcp catalog entry "${name}" must be an object`);
    }
    out[name] = parseCatalogEntry(name, rawEntry as Record<string, unknown>);
  }
  return out;
}

function parseCatalogEntry(
  name: string,
  e: Record<string, unknown>,
): McpCatalogEntry {
  const transport = e.transport;
  if (transport === "http") {
    if (typeof e.url !== "string" || e.url.length === 0) {
      throw new Error(`mcp catalog entry "${name}" (http) requires a non-empty url`);
    }
    const entry: HttpCatalogEntry = { transport: "http", url: e.url };
    const headers = stringRecordOrUndefined(e.headers);
    if (headers !== undefined) entry.headers = headers;
    const allowed = stringArrayOrUndefined(e.allowed_tools ?? e.allowedTools);
    if (allowed !== undefined) entry.allowedTools = allowed;
    return entry;
  }
  if (transport === "stdio") {
    if (typeof e.command !== "string" || e.command.length === 0) {
      throw new Error(`mcp catalog entry "${name}" (stdio) requires a non-empty command`);
    }
    const entry: StdioCatalogEntry = { transport: "stdio", command: e.command };
    const args = stringArrayOrUndefined(e.args);
    if (args !== undefined) entry.args = args;
    const env = stringRecordOrUndefined(e.env);
    if (env !== undefined) entry.env = env;
    const allowed = stringArrayOrUndefined(e.allowed_tools ?? e.allowedTools);
    if (allowed !== undefined) entry.allowedTools = allowed;
    return entry;
  }
  if (transport === "module" || transport === "http-loopback") {
    if (typeof e.module !== "string" || e.module.length === 0) {
      throw new Error(
        `mcp catalog entry "${name}" (${transport}) requires a non-empty module specifier`,
      );
    }
    const entry: ModuleCatalogEntry = { transport, module: e.module };
    const allowed = stringArrayOrUndefined(e.allowed_tools ?? e.allowedTools);
    if (allowed !== undefined) entry.allowedTools = allowed;
    return entry;
  }
  throw new Error(
    `mcp catalog entry "${name}" has unknown transport ${JSON.stringify(transport)} ` +
      `(expected http | stdio | module | http-loopback)`,
  );
}

/**
 * Parses the `allow_inline` policy from `RIMSKY_EXECUTOR_MCP_ALLOW_INLINE`.
 * Default false: a deployment with a catalog wants its catalog to be the
 * authoritative server source, so inline `{name,url}` servers are rejected
 * unless the operator explicitly opts in (`=1` / `=true`).
 */
export function parsePolicy(v: string | undefined): boolean {
  if (v === undefined) return false;
  const normalized = v.trim().toLowerCase();
  return normalized === "1" || normalized === "true" || normalized === "yes";
}

/**
 * A per-dispatch resolution of a catalog `{ ref: }` into a concrete
 * `CliToolConfig` leaf plus an optional teardown for any transport the
 * resolution stood up (a module / http-loopback loopback listener). The
 * resolver returns the leaf for the spawn `tools` list and a `teardown`
 * the dispatch wires into its per-dispatch cleanup.
 */
export interface ResolvedCatalogServer {
  tool: CliToolConfig;
  /** Explicit per-server allowed tools (catalog `allowed_tools`), or
   *  undefined to auto-allow ALL of the server's tools. */
  allowedTools?: string[];
  /** Tears down any per-dispatch resource the stand-up created (loopback
   *  HTTP listener for module / http-loopback). No-op for http / stdio. */
  teardown: () => Promise<void>;
}

/**
 * Resolves a single catalog entry (named `name`) into a concrete spawn leaf,
 * standing up any per-dispatch transport it needs. Pure for http / stdio
 * (no resource); for module / http-loopback this `import()`s the module and
 * fronts it on a fresh loopback HTTP MCP listener, returning the listener's
 * URL as an `mcp-http` leaf so the Claude CLI can dial it.
 */
export async function resolveCatalogServer(
  name: string,
  entry: McpCatalogEntry,
  logger: Logger,
): Promise<ResolvedCatalogServer> {
  if (entry.transport === "http") {
    return {
      tool: { kind: "mcp-http", name, url: entry.url, headers: entry.headers },
      allowedTools: entry.allowedTools,
      teardown: async () => {},
    };
  }
  if (entry.transport === "stdio") {
    return {
      tool: {
        kind: "mcp-stdio",
        name,
        command: entry.command,
        args: entry.args,
        env: entry.env,
      },
      allowedTools: entry.allowedTools,
      teardown: async () => {},
    };
  }
  // @deliberate: module | http-loopback: load the module and front it on a per-dispatch
  // loopback HTTP MCP listener.
  const listener = await standUpModuleLoopback(name, entry.module, logger);
  return {
    tool: { kind: "mcp-http", name, url: listener.url },
    allowedTools: entry.allowedTools,
    teardown: listener.close,
  };
}

interface LoopbackListener {
  url: string;
  close: () => Promise<void>;
}

/**
 * Imports a catalog module (its `createMcpServer()` factory) and fronts it
 * on a loopback HTTP MCP listener bound to an ephemeral 127.0.0.1 port. The
 * listener is per-dispatch: each request with no `mcp-session-id` mints a
 * fresh streamable-HTTP session backed by its own `McpServer` instance, so
 * the Claude CLI's initialize handshake lands cleanly. Returns the URL the
 * CLI dials and a `close` that tears down every session + the HTTP server.
 *
 * @source src/internal-mcp-server.ts (session-per-transport HTTP loop) —
 * the same streamable-HTTP multiplexing the internal callback server uses,
 * narrowed to a catalog-supplied module's tools.
 */
async function standUpModuleLoopback(
  name: string,
  moduleSpecifier: string,
  logger: Logger,
): Promise<LoopbackListener> {
  const factory = await loadModuleFactory(name, moduleSpecifier);

  interface Session {
    transport: StreamableHTTPServerTransport;
    server: McpServer;
  }
  const sessions = new Map<string, Session>();

  const createSession = async (): Promise<Session> => {
    const server = factory();
    const transport = new StreamableHTTPServerTransport({
      sessionIdGenerator: () => crypto.randomUUID(),
      onsessioninitialized: (sid) => {
        sessions.set(sid, { transport, server });
      },
    });
    transport.onclose = () => {
      const sid = transport.sessionId;
      if (sid) sessions.delete(sid);
    };
    await server.connect(transport);
    return { transport, server };
  };

  const httpServer = http.createServer(async (req, res) => {
    try {
      const sidHeader = req.headers["mcp-session-id"];
      const sid = typeof sidHeader === "string" ? sidHeader : undefined;
      if (sid) {
        const entry = sessions.get(sid);
        if (!entry) {
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
        await entry.transport.handleRequest(req, res);
        return;
      }
      const fresh = await createSession();
      await fresh.transport.handleRequest(req, res);
    } catch (err) {
      logger.error(
        { error: String(err), catalog_server: name },
        "mcp-catalog.loopback_request_failed",
      );
      if (!res.headersSent) {
        res.statusCode = 500;
        res.end("internal mcp error");
      }
    }
  });
  // @deliberate: a loopback MCP server holds its SSE GET stream open for
  // the whole dispatch; disable Node's idle-socket caps so the held stream
  // is never reaped mid-dispatch (same property the internal callback
  // server pins).
  httpServer.timeout = 0;
  httpServer.requestTimeout = 0;
  httpServer.keepAliveTimeout = 24 * 60 * 60 * 1000;
  httpServer.headersTimeout = 24 * 60 * 60 * 1000;
  httpServer.on("clientError", (_err, socket) => {
    try {
      socket.destroy();
    } catch {
      /* @deliberate: already gone */
    }
  });

  await new Promise<void>((resolveListen, rejectListen) => {
    const onErr = (err: Error): void => rejectListen(err);
    httpServer.once("error", onErr);
    httpServer.listen(0, "127.0.0.1", () => {
      httpServer.off("error", onErr);
      resolveListen();
    });
  });
  const addr = httpServer.address() as AddressInfo;
  const url = `http://127.0.0.1:${addr.port}/`;

  const close = async (): Promise<void> => {
    for (const [, session] of sessions) {
      await session.transport.close().catch(() => {});
      await session.server.close().catch(() => {});
    }
    sessions.clear();
    await new Promise<void>((resolveClose) => {
      httpServer.close(() => resolveClose());
    });
  };

  return { url, close };
}

type McpServerFactory = () => McpServer;

/**
 * Dynamically imports a catalog module and returns its `createMcpServer()`
 * factory. The contract (documented on the in-tree fixture): the module
 * exports a named `createMcpServer()` (or a default export) returning a
 * registered `McpServer`. A relative specifier is resolved against this
 * module's directory; an absolute path / `file://` URL is used verbatim.
 */
async function loadModuleFactory(
  name: string,
  specifier: string,
): Promise<McpServerFactory> {
  const importUrl = toImportUrl(specifier);
  let mod: Record<string, unknown>;
  try {
    mod = (await import(importUrl)) as Record<string, unknown>;
  } catch (e) {
    throw new CliConfigError(
      `mcp catalog server "${name}" module "${specifier}" failed to import: ${String(e)}`,
    );
  }
  const factory = (mod.createMcpServer ?? mod.default) as unknown;
  if (typeof factory !== "function") {
    throw new CliConfigError(
      `mcp catalog server "${name}" module "${specifier}" must export ` +
        `a createMcpServer() factory (or a default export)`,
    );
  }
  return factory as McpServerFactory;
}

function toImportUrl(specifier: string): string {
  if (specifier.startsWith("file://")) return specifier;
  if (isAbsolute(specifier)) return pathToFileURL(specifier).href;
  // @deliberate: relative specifiers resolve against this module's directory so a
  // catalog can name an in-tree module path independent of process cwd.
  return pathToFileURL(resolvePath(MODULE_DIR, specifier)).href;
}

function stringRecordOrUndefined(
  v: unknown,
): Record<string, string> | undefined {
  if (!v || typeof v !== "object" || Array.isArray(v)) return undefined;
  const out: Record<string, string> = {};
  for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
    if (typeof val === "string") out[k] = val;
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

function stringArrayOrUndefined(v: unknown): string[] | undefined {
  if (!Array.isArray(v)) return undefined;
  const out: string[] = [];
  for (const item of v) {
    if (typeof item === "string" && item.length > 0) out.push(item);
  }
  return out.length > 0 ? out : undefined;
}
