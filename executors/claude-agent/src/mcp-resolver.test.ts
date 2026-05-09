// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import { resolveMcpServers } from "./mcp-resolver.js";
import type { McpCatalogConfig } from "./mcp-catalog.js";

const sampleCatalog = (): McpCatalogConfig => ({
  mcp_catalog: {
    "project-tracker": {
      transport: "http",
      url: "https://api.example.com",
      headers: { Authorization: "abc123" },
    },
    "workspace-files": {
      transport: "stdio",
      command: "fs-server",
      args: ["--root", "/data"],
    },
    "alpha-tools": {
      transport: "module",
      module: "@project-alpha/tools",
      lifetime: "per-dispatch",
      config: { mode: "default" },
    },
  },
  policy: {
    allow_inline: false,
    allow_modules_from: ["@project-alpha/*"],
  },
});

describe("resolveMcpServers", () => {
  it("resolves a ref entry from the catalog", () => {
    const cfg = sampleCatalog();
    const out = resolveMcpServers(cfg, [{ ref: "project-tracker" }]);
    expect(out).toHaveLength(1);
    expect(out[0].name).toBe("project-tracker");
    expect(out[0].entry.transport).toBe("http");
  });

  it("rejects a missing ref", () => {
    const cfg = sampleCatalog();
    expect(() => resolveMcpServers(cfg, [{ ref: "nonexistent" }])).toThrow(
      /not in catalog/,
    );
  });

  it("rejects inline entries when allow_inline is false", () => {
    const cfg = sampleCatalog();
    expect(() =>
      resolveMcpServers(cfg, [
        {
          name: "inline-srv",
          transport: "http",
          url: "https://other.example.com",
        },
      ]),
    ).toThrow(/allow_inline/);
  });

  it("accepts inline entries when allow_inline is true", () => {
    const cfg = sampleCatalog();
    cfg.policy.allow_inline = true;
    const out = resolveMcpServers(cfg, [
      { name: "inline-srv", transport: "http", url: "https://other.example.com" },
    ]);
    expect(out[0].name).toBe("inline-srv");
    if (out[0].entry.transport === "http") {
      expect(out[0].entry.url).toBe("https://other.example.com");
    }
  });

  it("merges override config into module entries", () => {
    const cfg = sampleCatalog();
    const out = resolveMcpServers(cfg, [
      { ref: "alpha-tools", config: { mode: "verbose", extra: 42 } },
    ]);
    if (out[0].entry.transport === "module") {
      expect(out[0].entry.config).toEqual({ mode: "verbose", extra: 42 });
    }
  });
});
