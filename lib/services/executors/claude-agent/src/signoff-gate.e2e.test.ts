// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import http from "node:http";
import crypto from "node:crypto";
import type { AddressInfo } from "node:net";
import pino from "pino";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { z } from "zod";
import {
  startHttpBridge,
  type RunningHttpBridge,
  parseCliConfig,
} from "./http-bridge.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type {
  CliRunner,
  CliHandle,
  CliSpawnRequest,
} from "./cli-runner.js";
import { makeTestSigner } from "./signoff-test-signer.js";

const logger = pino({ level: "silent" });

const DISPATCH_ID = "acc-disp-1";
const ENDPOINTS = [{ url: "x" }];
const ATTRIBUTES_DELTA = { endpoints: ENDPOINTS };

type ToolStatus =
  | { status: "accepted" }
  | { status: "rejected"; errors: Record<string, string[]> };

function parseToolStatus(content: unknown): ToolStatus {
  const arr = content as Array<{ type: string; text?: string }>;
  return JSON.parse(arr[0]!.text ?? "null") as ToolStatus;
}

function makeReportingHandle(
  req: CliSpawnRequest,
  buildArgs: () => Record<string, unknown>,
): CliHandle {
  const exitWaiters: ((r: {
    exitCode: number | null;
    signal: NodeJS.Signals | null;
  }) => void)[] = [];
  let exited = false;
  let result: { exitCode: number | null; signal: NodeJS.Signals | null } | null =
    null;

  const callbackTool = req.tools.find((t) => t.name === "rimsky-callback");
  const url = callbackTool!.url;
  const token = req.env.RIMSKY_CALLBACK_TOKEN;

  const drive = async (): Promise<void> => {
    const transport = new StreamableHTTPClientTransport(new URL(url));
    const client = new Client({
      name: "rimsky-signoff-acceptance-cli",
      version: "1.0.0",
    });
    try {
      await client.connect(transport);
      for (let attempt = 0; attempt < 10; attempt++) {
        const res = await client.callTool({
          name: "report_complete",
          arguments: { token, ...buildArgs() },
        });
        const status = parseToolStatus(res.content);
        if (status.status !== "rejected") break;
      }
    } finally {
      await client.close().catch(() => {});
    }
  };

  setTimeout(() => {
    void (async () => {
      await drive();
      exited = true;
      result = { exitCode: 0, signal: null };
      for (const w of exitWaiters) w(result);
    })();
  }, 5);

  return {
    pid: 31337,
    onStdout: () => {},
    onStderr: () => {},
    onExit: () => {},
    sendSigterm: () => {},
    sendSigkill: () => {},
    waitExit: () =>
      exited && result
        ? Promise.resolve(result)
        : new Promise((resolve) => exitWaiters.push(resolve)),
  };
}

async function waitFor(fn: () => boolean, timeoutMs: number): Promise<void> {
  const start = Date.now();
  while (!fn()) {
    if (Date.now() - start > timeoutMs) throw new Error("waitFor: timed out");
    await new Promise((r) => setTimeout(r, 20));
  }
}

describe("sign-off gate acceptance (real HTTP bridge + real signer)", () => {
  let cb: CallbackServerHandle;
  let bridge: RunningHttpBridge;
  const posts: Array<{ url: string; body: unknown }> = [];

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    posts.length = 0;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await bridge.shutdown();
    await cb.close();
  });

  it("blocks unsigned output with agent/signoff_unobtained", async () => {
    const signer = makeTestSigner();
    const unsignedCli: CliRunner = {
      spawn: async (req: CliSpawnRequest) =>
        makeReportingHandle(req, () => ({
          changed: true,
          attributes_delta: ATTRIBUTES_DELTA,
        })),
    };
    bridge = await startHttpBridge({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: unsignedCli,
      silenceTimeoutMs: 30_000,
      logger,
      postCallback: async (url, body) => {
        posts.push({ url, body });
      },
    });

    const callbackUrl = "http://supervisor.invalid/cb";
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        dispatch_id: DISPATCH_ID,
        attributes: {
          user_prompt: "go",
          cli: {
            required_signoffs: [
              { public_key: signer.publicKeyPem, path: "endpoints" },
            ],
            max_signoff_attempts: 1,
          },
        },
        attributes_schema: {},
        callback_url: callbackUrl,
      }),
    });
    expect(res.status).toBe(202);

    await waitFor(() => posts.length > 0, 5000);
    expect(posts[0]!.url).toBe(callbackUrl);
    const unsignedBody = posts[0]!.body as Record<string, unknown>;
    expect(unsignedBody.success).toBeUndefined();
    const error = unsignedBody.error as Record<string, unknown> | undefined;
    expect(error).toBeDefined();
    expect(error!.error_class).toBe("agent/signoff_unobtained");
    expect(
      ["success", "error", "park"].filter((k) => k in unsignedBody),
    ).toEqual(["error"]);
  });

  it("completes signed output", async () => {
    const signer = makeTestSigner();
    const signedCli: CliRunner = {
      spawn: async (req: CliSpawnRequest) =>
        makeReportingHandle(req, () => ({
          changed: true,
          attributes_delta: ATTRIBUTES_DELTA,
          signoffs: [signer.sign(DISPATCH_ID, ENDPOINTS)],
        })),
    };
    bridge = await startHttpBridge({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: signedCli,
      silenceTimeoutMs: 30_000,
      logger,
      postCallback: async (url, body) => {
        posts.push({ url, body });
      },
    });

    const callbackUrl = "http://supervisor.invalid/cb";
    const res = await fetch(`${bridge.address}/execute`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        dispatch_id: DISPATCH_ID,
        attributes: {
          user_prompt: "go",
          cli: {
            required_signoffs: [
              { public_key: signer.publicKeyPem, path: "endpoints" },
            ],
            max_signoff_attempts: 1,
          },
        },
        attributes_schema: {},
        callback_url: callbackUrl,
      }),
    });
    expect(res.status).toBe(202);

    await waitFor(() => posts.length > 0, 5000);
    const signedBody = posts[0]!.body as Record<string, unknown>;
    expect(signedBody.error).toBeUndefined();
    const success = signedBody.success as Record<string, unknown> | undefined;
    expect(success).toBeDefined();
    expect(success!.attributes_delta).toEqual({ ...ATTRIBUTES_DELTA, session_token: DISPATCH_ID });
    expect(
      ["success", "error", "park"].filter((k) => k in signedBody),
    ).toEqual(["success"]);
  });

});

const CATALOG_MODULE_SPECIFIER = "./mcp-catalog-test-module.js";

function makeCompletingHandle(req: CliSpawnRequest): CliHandle {
  const exitWaiters: ((r: {
    exitCode: number | null;
    signal: NodeJS.Signals | null;
  }) => void)[] = [];
  let exited = false;
  let result: { exitCode: number | null; signal: NodeJS.Signals | null } | null =
    null;

  const callbackTool = req.tools.find((t) => t.name === "rimsky-callback");
  const url = callbackTool!.url;
  const token = req.env.RIMSKY_CALLBACK_TOKEN;

  const drive = async (): Promise<void> => {
    const transport = new StreamableHTTPClientTransport(new URL(url));
    const client = new Client({
      name: "rimsky-catalog-transport-cli",
      version: "1.0.0",
    });
    try {
      await client.connect(transport);
      await client.callTool({
        name: "report_complete",
        arguments: {
          token,
          changed: true,
          attributes_delta: { ok: true },
        },
      });
    } finally {
      await client.close().catch(() => {});
    }
  };

  setTimeout(() => {
    void (async () => {
      await drive();
      exited = true;
      result = { exitCode: 0, signal: null };
      for (const w of exitWaiters) w(result);
    })();
  }, 5);

  return {
    pid: 41100,
    onStdout: () => {},
    onStderr: () => {},
    onExit: () => {},
    sendSigterm: () => {},
    sendSigkill: () => {},
    waitExit: () =>
      exited && result
        ? Promise.resolve(result)
        : new Promise((resolve) => exitWaiters.push(resolve)),
  };
}

describe("MCP catalog module/http-loopback transports (real HTTP bridge)", () => {
  let cb: CallbackServerHandle;
  let bridge: RunningHttpBridge;
  const posts: Array<{ url: string; body: unknown }> = [];

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    posts.length = 0;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await bridge.shutdown();
    await cb.close();
  });

  it("dispatches successfully using a module-transport catalog server and an http-loopback-transport catalog server", async () => {
    const catalog = {
      "echo-module": {
        transport: "module",
        module: CATALOG_MODULE_SPECIFIER,
      },
      "echo-loopback": {
        transport: "http-loopback",
        module: CATALOG_MODULE_SPECIFIER,
      },
    };

    for (const ref of ["echo-module", "echo-loopback"]) {
      posts.length = 0;
      const completingCli: CliRunner = {
        spawn: async (req: CliSpawnRequest) => makeCompletingHandle(req),
      };
      bridge = await startHttpBridge({
        host: "127.0.0.1",
        port: 0,
        callback: cb,
        cliRunner: completingCli,
        silenceTimeoutMs: 30_000,
        logger,
        mcpCatalog: catalog,
        mcpAllowInline: false,
        postCallback: async (url, body) => {
          posts.push({ url, body });
        },
      });

      const callbackUrl = "http://supervisor.invalid/cb";
      const res = await fetch(`${bridge.address}/execute`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          dispatch_id: `catalog-${ref}`,
          attributes: {
            user_prompt: "go",
            cli: {
              mcp_servers: [{ ref }],
            },
          },
          attributes_schema: {},
          callback_url: callbackUrl,
        }),
      });
      expect(res.status).toBe(202);

      await waitFor(() => posts.length > 0, 5000);
      const body = posts[0]!.body as Record<string, unknown>;
      expect(body.error).toBeUndefined();
      expect(body.success).toBeDefined();
      expect(
        ["success", "error", "park"].filter((k) => k in body),
      ).toEqual(["success"]);

      await bridge.shutdown();
    }
  });
});

