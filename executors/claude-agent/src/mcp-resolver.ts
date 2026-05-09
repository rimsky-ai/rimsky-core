// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

/**
 * mcp-resolver.ts — userdata-side MCP server resolution.
 *
 * Given the loaded catalog and a per-dispatch userdata.cli.mcpServers
 * array, returns the list of resolved bindings ready for the four
 * transport handlers. Refs are looked up; inline entries are accepted
 * only when policy.allow_inline is true.
 */

import type {
  CatalogEntry,
  McpCatalogConfig,
  McpPolicy,
} from "./mcp-catalog.js";

/** A reference into the catalog with optional override config. */
export interface RefEntry {
  ref: string;
  config?: Record<string, unknown>;
}

/** An inline server definition with its own transport details. */
export type InlineEntry = { name: string } & CatalogEntry;

/** A userdata.cli.mcpServers entry, post-shape-validation. */
export type UserdataMcpEntry = RefEntry | InlineEntry;

/** A binding ready to feed into a transport handler. */
export interface ResolvedMcpBinding {
  /** Display name from the catalog key or the inline `name`. */
  name: string;
  /** The resolved entry — catalog values are deep-cloned with overrides applied. */
  entry: CatalogEntry;
}

/**
 * Resolve every userdata mcpServers entry against the loaded catalog
 * and policy. Throws on the first invalid entry.
 */
export function resolveMcpServers(
  catalog: McpCatalogConfig,
  servers: UserdataMcpEntry[],
): ResolvedMcpBinding[] {
  const out: ResolvedMcpBinding[] = [];
  for (const raw of servers ?? []) {
    out.push(resolveOne(catalog, raw));
  }
  return out;
}

function resolveOne(
  catalog: McpCatalogConfig,
  raw: UserdataMcpEntry,
): ResolvedMcpBinding {
  if (isRefEntry(raw)) {
    const base = catalog.mcp_catalog[raw.ref];
    if (!base) {
      throw new Error(`mcp ref ${JSON.stringify(raw.ref)} not in catalog`);
    }
    return {
      name: raw.ref,
      entry: applyOverrides(base, raw.config),
    };
  }
  if (!catalog.policy.allow_inline) {
    throw new Error(
      `inline mcp server ${JSON.stringify(raw.name)} rejected: policy.allow_inline is false`,
    );
  }
  validateInlineAgainstPolicy(raw, catalog.policy);
  const inline = { ...raw } as InlineEntry;
  // Strip the name field so the entry has the same shape as catalog values.
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { name: _name, ...rest } = inline;
  return {
    name: raw.name,
    entry: rest as CatalogEntry,
  };
}

function isRefEntry(x: UserdataMcpEntry): x is RefEntry {
  return (x as RefEntry).ref !== undefined;
}

/**
 * Shallow-merge `overrides` into the catalog entry's `config` field
 * (only entries that have a `config` field accept overrides). Returns a
 * fresh object; never mutates the catalog entry.
 */
function applyOverrides(
  base: CatalogEntry,
  overrides?: Record<string, unknown>,
): CatalogEntry {
  if (!overrides) return cloneEntry(base);
  if (base.transport === "http" || base.transport === "stdio") {
    // No `config` field on http/stdio. Overrides ignored — but we don't
    // throw; the catalog name is still resolvable.
    return cloneEntry(base);
  }
  const cloned = cloneEntry(base);
  // Per-transport `config` field exists on module / http-loopback.
  const c = (cloned as ModuleLike).config ?? {};
  (cloned as ModuleLike).config = { ...c, ...overrides };
  return cloned;
}

interface ModuleLike {
  config?: Record<string, unknown>;
}

function cloneEntry(e: CatalogEntry): CatalogEntry {
  return JSON.parse(JSON.stringify(e)) as CatalogEntry;
}

function validateInlineAgainstPolicy(
  e: InlineEntry,
  policy: McpPolicy,
): void {
  if (e.transport === "module" || e.transport === "http-loopback") {
    if (policy.allow_modules_from.length === 0) {
      throw new Error(
        `inline mcp server ${JSON.stringify(e.name)}: ${e.transport} transport requires policy.allow_modules_from`,
      );
    }
    const mod = (e as { module?: string }).module;
    if (!mod) {
      throw new Error(`inline mcp server ${JSON.stringify(e.name)}: ${e.transport} requires module`);
    }
    let matched = false;
    for (const pattern of policy.allow_modules_from) {
      if (globMatch(mod, pattern)) {
        matched = true;
        break;
      }
    }
    if (!matched) {
      throw new Error(
        `inline mcp server ${JSON.stringify(e.name)}.module: ${JSON.stringify(mod)} matches no entry in policy.allow_modules_from`,
      );
    }
  }
}

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
