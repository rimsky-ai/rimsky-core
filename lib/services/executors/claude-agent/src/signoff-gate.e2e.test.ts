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

const VALIDATOR_TOOL_NAME = "attest";
const VALIDATOR_TOKEN_VALUE = "s3cr3t-validator-token-EXECUTORS5";

interface RunningValidator {
  url: string;
  seenAuth: string[];
  close(): Promise<void>;
}

async function startAuthGatedValidator(
  expectedToken: string,
): Promise<RunningValidator> {
  const seenAuth: string[] = [];

  interface Session {
    transport: StreamableHTTPServerTransport;
    server: McpServer;
  }
  const sessions = new Map<string, Session>();

  const createSession = async (): Promise<Session> => {
    const server = new McpServer({
      name: "auth-gated-validator",
      version: "1.0.0",
    });
    server.tool(
      VALIDATOR_TOOL_NAME,
      "Attests the agent's output; only reachable with a valid bearer token.",
      { claim: z.string() },
      async ({ claim }) => ({
        content: [{ type: "text" as const, text: `attested:${claim}` }],
      }),
    );
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
    const authHeader = req.headers["authorization"];
    const auth = typeof authHeader === "string" ? authHeader : "";
    seenAuth.push(auth);
    if (auth !== `Bearer ${expectedToken}`) {
      res.statusCode = 401;
      res.setHeader("content-type", "application/json");
      res.end(
        JSON.stringify({
          jsonrpc: "2.0",
          error: { code: -32000, message: "unauthorized" },
        }),
      );
      return;
    }
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
    } catch {
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

  return { url, seenAuth, close };
}

function makeValidatorReachingHandle(req: CliSpawnRequest): CliHandle {
  const exitWaiters: ((r: {
    exitCode: number | null;
    signal: NodeJS.Signals | null;
  }) => void)[] = [];
  let exited = false;
  let result: { exitCode: number | null; signal: NodeJS.Signals | null } | null =
    null;

  const callbackTool = req.tools.find((t) => t.name === "rimsky-callback");
  const callbackUrl = callbackTool!.kind === "mcp-http" ? callbackTool!.url : "";
  const token = req.env.RIMSKY_CALLBACK_TOKEN;

  const validatorTool = req.tools.find(
    (t) => t.name !== "rimsky-callback" && t.kind === "mcp-http",
  );

  const drive = async (): Promise<void> => {
    if (validatorTool === undefined || validatorTool.kind !== "mcp-http") {
      throw new Error("validator MCP server missing from spawn tools");
    }
    const vTransport = new StreamableHTTPClientTransport(
      new URL(validatorTool.url),
      { requestInit: { headers: validatorTool.headers ?? {} } },
    );
    const vClient = new Client({
      name: "rimsky-validator-reaching-cli",
      version: "1.0.0",
    });
    try {
      await vClient.connect(vTransport);
      await vClient.callTool({
        name: VALIDATOR_TOOL_NAME,
        arguments: { claim: "ok" },
      });
    } finally {
      await vClient.close().catch(() => {});
    }

    const cbTransport = new StreamableHTTPClientTransport(new URL(callbackUrl));
    const cbClient = new Client({
      name: "rimsky-validator-reaching-cli-cb",
      version: "1.0.0",
    });
    try {
      await cbClient.connect(cbTransport);
      await cbClient.callTool({
        name: "report_complete",
        arguments: { token, changed: true, attributes_delta: { ok: true } },
      });
    } finally {
      await cbClient.close().catch(() => {});
    }
  };

  setTimeout(() => {
    void (async () => {
      await drive().catch(() => {});
      exited = true;
      result = { exitCode: 0, signal: null };
      for (const w of exitWaiters) w(result);
    })();
  }, 5);

  return {
    pid: 51200,
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

describe("validator MCP header ${env:} resolution at spawn (real HTTP bridge + real auth-gated validator)", () => {
  let cb: CallbackServerHandle;
  let bridge: RunningHttpBridge;
  let validator: RunningValidator | undefined;
  const posts: Array<{ url: string; body: unknown }> = [];
  let priorEnv: string | undefined;

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    posts.length = 0;
    priorEnv = process.env.VALIDATOR_TOKEN;
    process.env.VALIDATOR_TOKEN = VALIDATOR_TOKEN_VALUE;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await bridge.shutdown();
    await cb.close();
    if (validator) await validator.close();
    validator = undefined;
    if (priorEnv === undefined) delete process.env.VALIDATOR_TOKEN;
    else process.env.VALIDATOR_TOKEN = priorEnv;
  });

  it("resolves ${env:VAR} in validator mcp_servers headers at spawn so a 401-gated validator is reached, while persisted attributes keep only the reference form", async () => {
    validator = await startAuthGatedValidator(VALIDATOR_TOKEN_VALUE);

    const attributes = {
      user_prompt: "go",
      cli: {
        mcp_servers: [
          {
            name: "validator",
            url: validator.url,
            headers: {
              Authorization: "Bearer ${env:VALIDATOR_TOKEN}",
            },
          },
        ],
      },
    };

    const reachingCli: CliRunner = {
      spawn: async (req: CliSpawnRequest) => makeValidatorReachingHandle(req),
    };
    bridge = await startHttpBridge({
      host: "127.0.0.1",
      port: 0,
      callback: cb,
      cliRunner: reachingCli,
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
        dispatch_id: "exec5-env-ref",
        attributes,
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

    expect(validator.seenAuth).toContain(`Bearer ${VALIDATOR_TOKEN_VALUE}`);
    expect(validator.seenAuth).not.toContain("Bearer ${env:VALIDATOR_TOKEN}");

    const parsed = parseCliConfig(attributes.cli);
    const persistedHeaders = parsed?.mcpServers?.[0];
    const headerValue =
      persistedHeaders && "headers" in persistedHeaders
        ? persistedHeaders.headers?.Authorization
        : undefined;
    expect(headerValue).toBe("Bearer ${env:VALIDATOR_TOKEN}");
    expect(JSON.stringify(parsed)).not.toContain(VALIDATOR_TOKEN_VALUE);
  });
});
