// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// Test fixture for S-executors-mcp-catalog-transports / EXECUTORS-2.3 (RED→GREEN).
//
// A tiny in-tree MCP module exposing ONE tool (`echo`). The catalog's
// `module` and `http-loopback` transports resolve a `{ ref: <name> }` entry
// to a module path; the executor either `import()`s this module directly
// (module transport) or stands up a loopback HTTP MCP listener backed by it
// (http-loopback transport). Either way the resolved server contributes its
// `echo` tool to the dispatch.
//
// The export shape is the contract the catalog's module loader binds to:
// a named `createMcpServer()` factory returning a registered `McpServer`.
// The GREEN pass implements the loader that calls this; this fixture only
// exists so the catalog has a real module to resolve and stand up.

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { z } from "zod";

/** The single tool this fixture module exposes. */
export const TOOL_NAME = "echo";

/**
 * Builds a registered MCP server exposing one `echo` tool. The catalog's
 * module / http-loopback transport loader invokes this to obtain the server
 * it either mounts in-process (module) or fronts on a loopback HTTP listener
 * (http-loopback).
 */
export function createMcpServer(): McpServer {
  const server = new McpServer({
    name: "catalog-test-module",
    version: "1.0.0",
  });
  server.tool(
    TOOL_NAME,
    "Echoes its input string back.",
    { text: z.string() },
    async ({ text }) => ({
      content: [{ type: "text" as const, text }],
    }),
  );
  return server;
}

export default createMcpServer;
