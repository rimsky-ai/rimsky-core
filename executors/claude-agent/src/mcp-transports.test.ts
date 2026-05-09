// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect } from "vitest";
import { materializeBindings, type ModuleHandle } from "./mcp-transports.js";
import type { ResolvedMcpBinding } from "./mcp-resolver.js";

describe("materializeBindings — http transport (J4)", () => {
  it("passes URL and headers through to the CLI config", async () => {
    const bindings: ResolvedMcpBinding[] = [
      {
        name: "project-tracker",
        entry: {
          transport: "http",
          url: "https://example.com/mcp",
          headers: { Authorization: "Bearer x" },
        },
      },
    ];
    const out = await materializeBindings(bindings);
    expect(out.config.mcpServers["project-tracker"]).toEqual({
      type: "http",
      url: "https://example.com/mcp",
      headers: { Authorization: "Bearer x" },
    });
    await out.cleanup();
  });
});

describe("materializeBindings — stdio transport (J5)", () => {
  it("passes command, args, and env through", async () => {
    const bindings: ResolvedMcpBinding[] = [
      {
        name: "workspace-files",
        entry: {
          transport: "stdio",
          command: "/usr/bin/example",
          args: ["--root", "/tmp"],
          env: { LOG_LEVEL: "debug" },
        },
      },
    ];
    const out = await materializeBindings(bindings);
    expect(out.config.mcpServers["workspace-files"]).toEqual({
      type: "stdio",
      command: "/usr/bin/example",
      args: ["--root", "/tmp"],
      env: { LOG_LEVEL: "debug" },
    });
    await out.cleanup();
  });
});

describe("materializeBindings — loopback transports (J6/J7)", () => {
  it("module and http-loopback produce identical wire shape (J6 alias)", async () => {
    const stub: ModuleHandle = {
      register: (server, config) => {
        server.addTool("ping", () => `pong-${(config as { tag?: string }).tag ?? ""}`);
      },
    };
    const loader = async () => stub;
    const moduleBinding: ResolvedMcpBinding = {
      name: "as-module",
      entry: { transport: "module", module: "@local/test", lifetime: "per-dispatch", config: { tag: "m" } },
    };
    const loopbackBinding: ResolvedMcpBinding = {
      name: "as-loopback",
      entry: {
        transport: "http-loopback",
        module: "@local/test",
        lifetime: "per-dispatch",
        config: { tag: "l" },
      },
    };
    const out = await materializeBindings([moduleBinding, loopbackBinding], { loadModule: loader });
    const m = out.config.mcpServers["as-module"];
    const l = out.config.mcpServers["as-loopback"];
    expect(m.type).toBe("http");
    expect(l.type).toBe("http");
    if (m.type === "http") expect(m.url).toMatch(/^http:\/\/127\.0\.0\.1:\d+\/mcp$/);
    if (l.type === "http") expect(l.url).toMatch(/^http:\/\/127\.0\.0\.1:\d+\/mcp$/);
    await out.cleanup();
  });

  it("loopback server replies to tools/list with the registered tools", async () => {
    const stub: ModuleHandle = {
      register: (server) => {
        server.addTool("hello", () => "world");
      },
    };
    const loader = async () => stub;
    const out = await materializeBindings(
      [
        {
          name: "loopback",
          entry: { transport: "http-loopback", module: "@local/test", lifetime: "per-dispatch" },
        },
      ],
      { loadModule: loader },
    );
    const entry = out.config.mcpServers["loopback"];
    if (entry.type !== "http") throw new Error("expected http entry");
    const res = await fetch(entry.url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/list" }),
    });
    const json = (await res.json()) as { result: { tools: Array<{ name: string }> } };
    expect(json.result.tools.map((t) => t.name)).toEqual(["hello"]);
    await out.cleanup();
  });
});
