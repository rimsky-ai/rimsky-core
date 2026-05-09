// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, afterEach, beforeEach } from "vitest";
import * as fs from "node:fs";
import * as path from "node:path";
import * as os from "node:os";
import { loadMcpCatalogConfig, expandEnv } from "./mcp-catalog.js";

describe("loadMcpCatalogConfig", () => {
  let tmp: string;
  beforeEach(() => {
    tmp = fs.mkdtempSync(path.join(os.tmpdir(), "mcp-cat-"));
  });
  afterEach(() => {
    fs.rmSync(tmp, { recursive: true, force: true });
  });

  it("returns empty catalog when config file is absent", () => {
    const cfg = loadMcpCatalogConfig(path.join(tmp, "no-such.yaml"));
    expect(cfg.mcp_catalog).toEqual({});
    expect(cfg.policy.allow_inline).toBe(false);
    expect(cfg.policy.allow_modules_from).toEqual([]);
  });

  it("parses an http entry and expands env headers", () => {
    process.env.TEST_TOKEN = "abc123";
    const file = path.join(tmp, "config.yaml");
    fs.writeFileSync(
      file,
      `mcp_catalog:
  project-tracker:
    transport: http
    url: https://api.example.com
    headers:
      Authorization: \${TEST_TOKEN}
policy:
  allow_inline: false
  allow_modules_from: []
`,
    );
    const cfg = loadMcpCatalogConfig(file);
    const e = cfg.mcp_catalog["project-tracker"];
    expect(e.transport).toBe("http");
    if (e.transport === "http") {
      expect(e.url).toBe("https://api.example.com");
      expect(e.headers?.Authorization).toBe("abc123");
    }
  });

  it("rejects module transport without allow_modules_from", () => {
    const file = path.join(tmp, "config.yaml");
    fs.writeFileSync(
      file,
      `mcp_catalog:
  bad:
    transport: module
    module: "@bad/mod"
    lifetime: per-dispatch
policy:
  allow_inline: false
  allow_modules_from: []
`,
    );
    expect(() => loadMcpCatalogConfig(file)).toThrow(/allow_modules_from/);
  });

  it("rejects module not matching allow_modules_from glob", () => {
    const file = path.join(tmp, "config.yaml");
    fs.writeFileSync(
      file,
      `mcp_catalog:
  bad:
    transport: module
    module: "@other/mod"
    lifetime: per-dispatch
policy:
  allow_inline: false
  allow_modules_from: ["@project-alpha/*"]
`,
    );
    expect(() => loadMcpCatalogConfig(file)).toThrow(/matches no entry/);
  });

  it("accepts module matching allow_modules_from glob", () => {
    const file = path.join(tmp, "config.yaml");
    fs.writeFileSync(
      file,
      `mcp_catalog:
  ok:
    transport: module
    module: "@project-alpha/tools"
    lifetime: per-dispatch
policy:
  allow_inline: false
  allow_modules_from: ["@project-alpha/*"]
`,
    );
    const cfg = loadMcpCatalogConfig(file);
    expect(cfg.mcp_catalog.ok.transport).toBe("module");
  });
});

describe("expandEnv", () => {
  beforeEach(() => {
    delete process.env.UNUSED_VAR;
  });

  it("substitutes a defined env var", () => {
    process.env.MY_VAR = "hello";
    expect(expandEnv("X-${MY_VAR}-Y")).toBe("X-hello-Y");
  });

  it("uses default when var is absent", () => {
    expect(expandEnv("${UNUSED_VAR:-fallback}")).toBe("fallback");
  });

  it("collapses missing var with no default to empty", () => {
    expect(expandEnv("X-${UNUSED_VAR}-Y")).toBe("X--Y");
  });
});
