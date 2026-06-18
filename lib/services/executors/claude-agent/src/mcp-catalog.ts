// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

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

const MODULE_DIR = dirname(fileURLToPath(import.meta.url));

export interface HttpCatalogEntry {
  transport: "http";
  url: string;
  headers?: Record<string, string>;
  allowedTools?: string[];
}

export interface StdioCatalogEntry {
  transport: "stdio";
  command: string;
  args?: string[];
  env?: Record<string, string>;
  allowedTools?: string[];
}

export interface ModuleCatalogEntry {
  transport: "module" | "http-loopback";
  module: string;
  allowedTools?: string[];
}

export type McpCatalogEntry =
  | HttpCatalogEntry
  | StdioCatalogEntry
  | ModuleCatalogEntry;

export type McpCatalog = Record<string, McpCatalogEntry>;

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

export function parsePolicy(v: string | undefined): boolean {
  if (v === undefined) return false;
  const normalized = v.trim().toLowerCase();
  return normalized === "1" || normalized === "true" || normalized === "yes";
}

export interface ResolvedCatalogServer {
  tool: CliToolConfig;
  allowedTools?: string[];
  teardown: () => Promise<void>;
}

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
  httpServer.timeout = 0;
  httpServer.requestTimeout = 0;
  httpServer.keepAliveTimeout = 24 * 60 * 60 * 1000;
  httpServer.headersTimeout = 24 * 60 * 60 * 1000;
  httpServer.on("clientError", (_err, socket) => {
    try {
      socket.destroy();
    } catch {
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
