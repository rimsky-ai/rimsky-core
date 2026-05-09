// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

/**
 * mcp-catalog.ts — startup-config loader for the per-executor MCP server
 * catalog. Templates reference catalog entries by `ref`; the resolver
 * (mcp-resolver.ts) materializes each ref into a transport-ready binding
 * at dispatch time.
 *
 * Config sources, in order:
 *   1. CLAUDE_AGENT_CONFIG env var (path to YAML or JSON).
 *   2. /etc/claude-agent/config.yaml (fallback).
 *   3. {} (no catalog) — only inline mcpServers entries are usable.
 *
 * The loader resolves ${VAR} env-var indirection in `headers` and
 * `env` values at load time so values never carry env-var references
 * downstream.
 */

import * as fs from "node:fs";
import * as path from "node:path";
import * as YAML from "yaml";

export interface McpCatalogConfig {
  mcp_catalog: Record<string, CatalogEntry>;
  policy: McpPolicy;
}

export interface McpPolicy {
  /** When false, inline (non-ref) userdata mcpServers entries are rejected. */
  allow_inline: boolean;
  /** Glob patterns; entries with `module` field must match one. Empty disables module/http-loopback transports. */
  allow_modules_from: string[];
}

export type CatalogEntry =
  | HttpEntry
  | StdioEntry
  | ModuleEntry
  | HttpLoopbackEntry;

export interface HttpEntry {
  transport: "http";
  url: string;
  headers?: Record<string, string>;
}

export interface StdioEntry {
  transport: "stdio";
  command: string;
  args?: string[];
  env?: Record<string, string>;
  lifetime?: "persistent" | "per-dispatch";
}

export interface ModuleEntry {
  transport: "module";
  module: string;
  lifetime: "per-dispatch";
  config?: Record<string, unknown>;
}

export interface HttpLoopbackEntry {
  transport: "http-loopback";
  module: string;
  lifetime: "per-dispatch";
  config?: Record<string, unknown>;
}

const DEFAULT_CONFIG_PATH = "/etc/claude-agent/config.yaml";

/**
 * Load the MCP catalog and policy from the operator-supplied config
 * file. Returns the empty catalog when no file is present.
 */
export function loadMcpCatalogConfig(configPath?: string): McpCatalogConfig {
  const resolved = configPath ?? process.env.CLAUDE_AGENT_CONFIG ?? DEFAULT_CONFIG_PATH;
  if (!fs.existsSync(resolved)) {
    return emptyCatalog();
  }
  const raw = fs.readFileSync(resolved, "utf8");
  const parsed = path.extname(resolved).toLowerCase() === ".json"
    ? JSON.parse(raw)
    : YAML.parse(raw);
  return validateAndExpand(parsed ?? {});
}

function emptyCatalog(): McpCatalogConfig {
  return {
    mcp_catalog: {},
    policy: { allow_inline: false, allow_modules_from: [] },
  };
}

function validateAndExpand(raw: unknown): McpCatalogConfig {
  if (!raw || typeof raw !== "object") {
    return emptyCatalog();
  }
  const r = raw as Record<string, unknown>;
  const policyRaw = (r.policy ?? {}) as Record<string, unknown>;
  const policy: McpPolicy = {
    allow_inline: typeof policyRaw.allow_inline === "boolean" ? policyRaw.allow_inline : false,
    allow_modules_from: Array.isArray(policyRaw.allow_modules_from)
      ? (policyRaw.allow_modules_from as unknown[]).filter((x): x is string => typeof x === "string")
      : [],
  };
  const catalogRaw = (r.mcp_catalog ?? {}) as Record<string, unknown>;
  const catalog: Record<string, CatalogEntry> = {};
  for (const [name, entryRaw] of Object.entries(catalogRaw)) {
    const entry = validateCatalogEntry(name, entryRaw, policy);
    catalog[name] = entry;
  }
  return { mcp_catalog: catalog, policy };
}

function validateCatalogEntry(
  name: string,
  raw: unknown,
  policy: McpPolicy,
): CatalogEntry {
  if (!raw || typeof raw !== "object") {
    throw new Error(`mcp_catalog[${name}]: not an object`);
  }
  const e = raw as Record<string, unknown>;
  const transport = e.transport;
  switch (transport) {
    case "http":
      return {
        transport: "http",
        url: requireString(e.url, `mcp_catalog[${name}].url`),
        headers: expandStringMap(asStringMap(e.headers)),
      };
    case "stdio":
      return {
        transport: "stdio",
        command: requireString(e.command, `mcp_catalog[${name}].command`),
        args: asStringArray(e.args),
        env: expandStringMap(asStringMap(e.env)),
        lifetime: asLifetime(e.lifetime),
      };
    case "module": {
      const mod = requireString(e.module, `mcp_catalog[${name}].module`);
      checkModuleAllowed(name, mod, policy);
      return {
        transport: "module",
        module: mod,
        lifetime: "per-dispatch",
        config: asObject(e.config),
      };
    }
    case "http-loopback": {
      const mod = requireString(e.module, `mcp_catalog[${name}].module`);
      checkModuleAllowed(name, mod, policy);
      return {
        transport: "http-loopback",
        module: mod,
        lifetime: "per-dispatch",
        config: asObject(e.config),
      };
    }
    default:
      throw new Error(
        `mcp_catalog[${name}].transport: unknown transport ${JSON.stringify(transport)} (want http | stdio | module | http-loopback)`,
      );
  }
}

function requireString(v: unknown, label: string): string {
  if (typeof v !== "string" || v === "") {
    throw new Error(`${label}: required string`);
  }
  return v;
}

function asStringArray(v: unknown): string[] | undefined {
  if (v === undefined || v === null) return undefined;
  if (!Array.isArray(v)) {
    throw new Error("expected string array");
  }
  return v.filter((x): x is string => typeof x === "string");
}

function asStringMap(v: unknown): Record<string, string> | undefined {
  if (v === undefined || v === null) return undefined;
  if (typeof v !== "object" || Array.isArray(v)) {
    throw new Error("expected string map");
  }
  const out: Record<string, string> = {};
  for (const [k, val] of Object.entries(v)) {
    if (typeof val === "string") out[k] = val;
  }
  return out;
}

function asObject(v: unknown): Record<string, unknown> | undefined {
  if (v === undefined || v === null) return undefined;
  if (typeof v !== "object" || Array.isArray(v)) {
    throw new Error("expected object");
  }
  return v as Record<string, unknown>;
}

function asLifetime(v: unknown): "persistent" | "per-dispatch" | undefined {
  if (v === undefined || v === null) return undefined;
  if (v === "persistent" || v === "per-dispatch") return v;
  throw new Error(`lifetime: must be "persistent" or "per-dispatch", got ${JSON.stringify(v)}`);
}

function expandStringMap(m?: Record<string, string>): Record<string, string> | undefined {
  if (!m) return undefined;
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(m)) {
    out[k] = expandEnv(v);
  }
  return out;
}

/**
 * Resolve ${VAR} and ${VAR:-default} env-var indirection. Unresolved
 * references collapse to the empty string (so a missing optional var
 * does not crash the loader; an unset required var manifests as a
 * downstream connection failure with a clear error).
 */
export function expandEnv(s: string): string {
  return s.replace(/\$\{([A-Z_][A-Z0-9_]*)(?::-([^}]*))?\}/g, (_match, name: string, defaultValue?: string) => {
    const v = process.env[name];
    if (v !== undefined) return v;
    return defaultValue ?? "";
  });
}

function checkModuleAllowed(name: string, mod: string, policy: McpPolicy): void {
  if (policy.allow_modules_from.length === 0) {
    throw new Error(
      `mcp_catalog[${name}]: module/http-loopback transports require policy.allow_modules_from to be non-empty`,
    );
  }
  for (const pattern of policy.allow_modules_from) {
    if (globMatch(mod, pattern)) return;
  }
  throw new Error(
    `mcp_catalog[${name}].module: ${JSON.stringify(mod)} matches no entry in policy.allow_modules_from`,
  );
}

/**
 * Minimal glob matcher: `*` matches any run of non-`/` chars, `**` matches
 * any run including `/`. Used only against npm-package style strings,
 * not filesystem paths.
 */
function globMatch(s: string, pattern: string): boolean {
  const re = new RegExp(
    "^" +
      pattern
        .replace(/[.+?^${}()|[\]\\]/g, "\\$&")
        .replace(/\*\*/g, "::DOUBLESTAR::")
        .replace(/\*/g, "[^/]*")
        .replace(/::DOUBLESTAR::/g, ".*") +
      "$",
  );
  return re.test(s);
}
