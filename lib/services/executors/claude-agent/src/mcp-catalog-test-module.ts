// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { z } from "zod";

export const TOOL_NAME = "echo";

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
