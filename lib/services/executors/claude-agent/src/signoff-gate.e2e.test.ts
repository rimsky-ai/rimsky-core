// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// @constraint: drives the REAL HTTP-bridge `/execute` entry point (not `runAgent`
// in isolation) so the `dispatch_id → binding_id` plumbing is exercised
// end-to-end: the bridge threads the raw `dispatch_id` into `runAgent`, the
// gate binds to it, and the product-faithful observable — the real
// `AsyncCallbackBody` POSTed to the callback URL — is asserted. The CLI
// subprocess and the LLM behind it are faked (a `CliRunner` fake that connects
// a real MCP client and calls `report_complete`); the gate, the Ed25519
// signer, and the HTTP entry point are all real.
//
// Two runs prove the gate:
//   - Unsigned output is blocked: report_complete with no valid signoff is
//     rejected in-session each attempt; after max_signoff_attempts the bridge
//     POSTs AsyncCallbackBody{ error: { error_class: "agent/signoff_unobtained" } }.
//   - Signed output completes: report_complete carrying a real Ed25519
//     signature over `domain ‖ dispatch_id ‖ canonical(endpoints)` is verified
//     and the bridge POSTs AsyncCallbackBody{ success: {...} }.

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
import type { PostAttributesFn } from "./attributes-tools.js";
import { makeTestSigner } from "./signoff-test-signer.js";

const logger = pino({ level: "silent" });

const DISPATCH_ID = "acc-disp-1";
/**
 * The bound output value. The gate is configured on `path: endpoints`, so the
 * signature must be over the value AT that path (the array), not the whole
 * delta.
 */
const ENDPOINTS = [{ url: "x" }];
const ATTRIBUTES_DELTA = { endpoints: ENDPOINTS };

type ToolStatus =
  | { status: "accepted" }
  | { status: "rejected"; errors: Record<string, string[]> };

function parseToolStatus(content: unknown): ToolStatus {
  const arr = content as Array<{ type: string; text?: string }>;
  return JSON.parse(arr[0]!.text ?? "null") as ToolStatus;
}

/**
 * Build a fake CliHandle (mirrors lifecycle.e2e.test.ts::makeFakeHandle) whose
 * `beforeExit` connects a real MCP client to the per-dispatch rimsky-callback
 * server and drives `report_complete`. The arguments supplied to
 * report_complete are produced by `buildArgs` per attempt (so the unsigned
 * case can re-submit the same un-signed delta until the budget is exhausted).
 */
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
      // @deliberate: re-call report_complete until the gate stops rejecting (either it
      // accepts a valid signoff, or the correction budget is exhausted and the
      // run commits — both surface as a non-"rejected" status). A hard cap
      // prevents an infinite loop if the gate misbehaves.
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

  // @deliberate: drive the report flow on the next tick (after runAgent registers
  // .onStdout/.onStderr and the handle is wired), then exit.
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

/**
 * Like makeReportingHandle, but the fake CLI first performs an INCREMENTAL
 * writeback via the `attributes_set` MCP tool (the path `report_complete`'s own
 * tool description tells the agent to prefer) and THEN calls `report_complete`
 * with `attributes_delta` OMITTED. This reproduces the real incremental-writeback
 * dispatch shape the sign-off gate must bind to: the bound output lives only in
 * the accumulated `attributes_set` writebacks, never on the terminal-final
 * delta. `setDelta` is the value the agent writes incrementally; `buildArgs`
 * produces the (delta-less) report_complete arguments per attempt.
 */
function makeIncrementalReportingHandle(
  req: CliSpawnRequest,
  setDelta: Record<string, unknown>,
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
      name: "rimsky-signoff-acceptance-cli-incremental",
      version: "1.0.0",
    });
    try {
      await client.connect(transport);
      // @deliberate: (1) Incremental writeback first — the supervisor records this delta;
      // the gate must reconstruct it as the effective bound value.
      const setRes = await client.callTool({
        name: "attributes_set",
        arguments: { token, delta: setDelta },
      });
      const setStatus = parseToolStatus(setRes.content);
      if (setStatus.status === "rejected") {
        throw new Error(
          `attributes_set unexpectedly rejected: ${JSON.stringify(setStatus)}`,
        );
      }
      // @deliberate: (2) report_complete with attributes_delta OMITTED (incremental path).
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
    pid: 31338,
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
    // @deliberate: real mode: the gate only runs in runAgentReal (stub mode short-circuits).
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    posts.length = 0;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await bridge.shutdown();
    await cb.close();
  });

  // @deliberate: two independent runs prove the gate. Each test starts its own bridge with
  // its own cliRunner; the beforeEach (resets `posts`, starts `cb`) and
  // afterEach (`bridge.shutdown()`) give each test framework-level isolation,
  // so there is no shared-buffer / shared-bridge coupling between them. The
  // shared DISPATCH_ID is safe precisely because the runs never overlap.

  it("blocks unsigned output with agent/signoff_unobtained", async () => {
    const signer = makeTestSigner();
    const unsignedCli: CliRunner = {
      spawn: async (req: CliSpawnRequest) =>
        makeReportingHandle(req, () => ({
          changed: true,
          attributes_delta: ATTRIBUTES_DELTA,
          // @deliberate: no `signoffs` — the gate must reject every attempt and, after
          // max_signoff_attempts, commit the run as agent/signoff_unobtained.
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
    // @deliberate: AsyncCallbackBody is a one-of: the unsigned outcome must carry exactly
    // the `error` key — never a stray `success` or `park` alongside it.
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
          // @deliberate: sign the value at the configured path (`endpoints`), bound to the
          // dispatch_id the Execute request carries — the exact bytes the gate
          // re-derives.
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
    // @deliberate: every terminal Success augments the committed delta with
    // `session_token: <runId>` (here equal to DISPATCH_ID) to ride the
    // rimsky attribute carry-forward mechanism (per the 2026-06-14
    // carry-forward design).
    expect(success!.attributes_delta).toEqual({ ...ATTRIBUTES_DELTA, session_token: DISPATCH_ID });
    // @deliberate: exactly one outcome key — `success`, never a stray `error`/`park`.
    expect(
      ["success", "error", "park"].filter((k) => k in signedBody),
    ).toEqual(["success"]);
  });

  // @deliberate: the agent produces its bound output via the incremental
  // `attributes_set` MCP callback and then calls `report_complete` with
  // `attributes_delta` OMITTED (null) — the path `report_complete`'s own tool
  // description tells the agent to prefer. The sign-off gate MUST bind the
  // run's REAL effective bound output (the accumulated incremental writeback at
  // `path: endpoints`), not whatever rides the (absent) terminal-final delta.
  //
  // Two sub-cases, each driving the REAL HTTP-bridge `/execute` entry point
  // with its own bridge + cliRunner and a distinct dispatch_id (so the two
  // sequential runs never collide on a shared id), assert the OBSERVABLE
  // outcome on the callback URL:
  //
  //   - Case A (stale/unsigned): the signature is produced over the OLD broken
  //     bytes — the literal canonical `"null"` (i.e. over `undefined`, the value
  //     today's `valueAtPath(null, "endpoints")` resolves to). Because the gate
  //     must bind the REAL accumulated value `[{url:"x"}]`, that stale signature
  //     does NOT verify and the run MUST be rejected with
  //     `agent/signoff_unobtained`. (RED: today the gate verifies over `"null"`,
  //     so the stale signature spuriously PASSES → no error → this assertion
  //     fails.)
  //
  //   - Case B (correctly signed): the signature is produced over the canonical
  //     bytes of the ACTUAL accumulated value at `path: endpoints`
  //     (`[{url:"x"}]`). The run MUST resolve to success whose committed delta
  //     includes `endpoints`. (RED: today the gate verifies over `"null"`, so a
  //     signature over the real value does NOT match → the run is rejected.)
  it("sign-off gate binds the accumulated incremental writeback when report_complete omits attributes_delta", async () => {
    // @deliberate: a succeeding writeback POST is required for the gate's accumulation to
    // observe the incremental delta (the `attributes_set` tool only reports
    // "accepted" on a 2xx, mirroring a live supervisor acknowledging the
    // writeback). Without this the real default would POST to the invalid
    // supervisor URL and fail.
    const okPostAttributes: PostAttributesFn = async () => ({ status: 200 });
    const SET_DELTA = { endpoints: ENDPOINTS };

    // @deliberate: Case A: stale signature over the literal "null" must be rejected.
    {
      const signerA = makeTestSigner();
      const dispatchA = "acc-incremental-A";
      // @deliberate: sign over `undefined` → buildSignoffMessage canonicalizes to "null":
      // the exact old broken bytes today's gate (wrongly) verifies against.
      const staleSig = signerA.sign(dispatchA, undefined);
      const staleCli: CliRunner = {
        spawn: async (req: CliSpawnRequest) =>
          makeIncrementalReportingHandle(req, SET_DELTA, () => ({
            changed: true,
            // @deliberate: attributes_delta OMITTED — incremental writeback path.
            signoffs: [staleSig],
          })),
      };
      bridge = await startHttpBridge({
        host: "127.0.0.1",
        port: 0,
        callback: cb,
        cliRunner: staleCli,
        silenceTimeoutMs: 30_000,
        logger,
        postCallback: async (url, body) => {
          posts.push({ url, body });
        },
        postAttributes: okPostAttributes,
      });

      const callbackUrl = "http://supervisor.invalid/cb";
      const res = await fetch(`${bridge.address}/execute`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          dispatch_id: dispatchA,
          attributes: {
            user_prompt: "go",
            cli: {
              required_signoffs: [
                { public_key: signerA.publicKeyPem, path: "endpoints" },
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
      const bodyA = posts[0]!.body as Record<string, unknown>;
      // @deliberate: the stale signature binds the OLD bytes, not the real accumulated
      // value, so the gate must reject the run.
      expect(bodyA.success).toBeUndefined();
      const errorA = bodyA.error as Record<string, unknown> | undefined;
      expect(errorA).toBeDefined();
      expect(errorA!.error_class).toBe("agent/signoff_unobtained");
      expect(
        ["success", "error", "park"].filter((k) => k in bodyA),
      ).toEqual(["error"]);

      await bridge.shutdown();
    }

    // @deliberate: Case B: signature over the REAL accumulated value must succeed.
    posts.length = 0;
    {
      const signerB = makeTestSigner();
      const dispatchB = "acc-incremental-B";
      // @deliberate: sign the value at the configured path (`endpoints`) = the ACTUAL
      // accumulated incremental writeback (`[{url:"x"}]`), bound to dispatchB.
      const realSig = signerB.sign(dispatchB, ENDPOINTS);
      const signedCli: CliRunner = {
        spawn: async (req: CliSpawnRequest) =>
          makeIncrementalReportingHandle(req, SET_DELTA, () => ({
            changed: true,
            // @deliberate: attributes_delta OMITTED — incremental writeback path.
            signoffs: [realSig],
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
        postAttributes: okPostAttributes,
      });

      const callbackUrl = "http://supervisor.invalid/cb";
      const res = await fetch(`${bridge.address}/execute`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          dispatch_id: dispatchB,
          attributes: {
            user_prompt: "go",
            cli: {
              required_signoffs: [
                { public_key: signerB.publicKeyPem, path: "endpoints" },
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
      const bodyB = posts[0]!.body as Record<string, unknown>;
      expect(bodyB.error).toBeUndefined();
      const successB = bodyB.success as Record<string, unknown> | undefined;
      expect(successB).toBeDefined();
      // @deliberate: the committed delta must carry the accumulated incremental writeback —
      // the gate bound `endpoints`, so the supervisor commits it.
      const committed = successB!.attributes_delta as
        | Record<string, unknown>
        | null
        | undefined;
      expect(committed).toBeDefined();
      expect(committed).not.toBeNull();
      expect(committed!.endpoints).toEqual(ENDPOINTS);
      expect(
        ["success", "error", "park"].filter((k) => k in bodyB),
      ).toEqual(["success"]);
    }
  });
});

// @deliberate: the executor is started with a startup MCP-server catalog. The
// catalog declares a `module`-transport server and an `http-loopback`-transport
// server, each resolving to a tiny in-tree MCP module exposing ONE tool.
// A node references each by `{ ref: <name> }`; the executor must resolve the
// ref, stand up the transport (in-process `import()` for `module`; a loopback
// HTTP MCP listener for `http-loopback`), and reach terminal success.
//
// Driven through the REAL HTTP-bridge `/execute` entry point (the same harness
// the sign-off-gate acceptance uses): the fake CLI connects a real MCP client
// to the per-dispatch rimsky-callback server and calls `report_complete`, and
// the OBSERVABLE outcome is the real `AsyncCallbackBody.success` POSTed to the
// callback URL. The catalog + `allow_inline` policy thread through
// `startHttpBridge` config (→ runAgent), mirroring the startup-catalog path.
//
// RED today: there is no startup catalog, no `{ref:}` resolution branch, and
// neither the `module` nor the `http-loopback` transport is implemented, so a
// node referencing a catalog server by `{ref:}` never resolves — spawn
// assembly fails and the dispatch does NOT reach success.

/**
 * The catalog's `module` / `http-loopback` entries resolve to this in-tree
 * fixture module (exports `createMcpServer()` exposing one `echo` tool).
 * Referenced as a module specifier the executor's catalog loader can import.
 */
const CATALOG_MODULE_SPECIFIER = "./mcp-catalog-test-module.js";

/**
 * Build a fake CliHandle that drives `report_complete` to terminal success.
 * Mirrors makeReportingHandle above but with no sign-off gate in play — the
 * run completes on the first report_complete. The point under test is that
 * the catalog-resolved transport server was wired into the spawn (so the
 * dispatch could run at all), proven by the run reaching terminal success.
 */
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

  // @deliberate: one test exercises BOTH transports (module + http-loopback) so the named
  // gate `-t 'dispatches successfully using a module-transport'` covers the
  // whole transport-stand-up surface in a single run.
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

    // @deliberate: drive both transports sequentially against their own bridge so the two
    // runs never share a dispatch and the catalog stand-up/teardown is
    // exercised independently for each transport.
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
              // @deliberate: reference the catalog server by {ref:}; the executor resolves
              // it, stands up the transport, and wires the server's `echo`
              // tool into the spawn.
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
      // @deliberate: the dispatch must reach terminal success — proving the catalog ref
      // resolved and the transport stood up (a failed stand-up would error
      // the dispatch before report_complete could land).
      expect(body.error).toBeUndefined();
      expect(body.success).toBeDefined();
      expect(
        ["success", "error", "park"].filter((k) => k in body),
      ).toEqual(["success"]);

      await bridge.shutdown();
    }
  });
});

// @deliberate: a node wires an auth-gated validator MCP server whose connection header
// carries an `${env:VALIDATOR_TOKEN}` reference rather than a plaintext
// secret:
//
//     cli.mcp_servers: [{ name, url, headers: { Authorization: "Bearer ${env:VALIDATOR_TOKEN}" } }]
//
// The executor MUST resolve `${env:VALIDATOR_TOKEN}` against its process
// environment at SPAWN time — when assembling the `--mcp-config` the CLI
// reads to dial the server — so the validator receives the real bearer token
// on the wire. The reference form (`${env:...}`) MUST remain in the parsed
// `cli.mcp_servers` shape the supervisor persists/traces, so the credential
// never lands in persisted node attributes.
//
// This drives the REAL HTTP-bridge `/execute` entry point (the same harness
// as the sign-off-gate acceptance). The fake CLI is the product-faithful
// stand-in for the real `claude` binary reading `--mcp-config`: it dials the
// validator using EXACTLY the headers the executor handed it in the spawn
// `tools` (`req.tools` ⇆ `--mcp-config`). The validator is a REAL
// streamable-HTTP MCP server that returns HTTP 401 unless it receives
// `Authorization: Bearer <resolved-token>`; only a successful tool call lets
// the fake CLI reach `report_complete` → terminal success. So the OBSERVABLE
// outcome — `AsyncCallbackBody.success` POSTed to the callback URL — proves
// the header was resolved to the real token on the wire.
//
// RED today: the header map is copied verbatim into the spawn `tools` with no
// `${env:}` resolution (cli-runner.ts `headers: t.headers ?? {}`,
// agent-run.ts `headers: s.headers`). The fake CLI therefore dials the
// validator with the literal `Bearer ${env:VALIDATOR_TOKEN}`, the validator
// returns 401, the tool call fails, and the dispatch resolves to an Error
// (never success) — so the success assertion fails.

const VALIDATOR_TOOL_NAME = "attest";
const VALIDATOR_TOKEN_VALUE = "s3cr3t-validator-token-EXECUTORS5";

interface RunningValidator {
  url: string;
  /** Authorization header values seen on the wire (every inbound request). */
  seenAuth: string[];
  close(): Promise<void>;
}

/**
 * Stand up a REAL streamable-HTTP MCP validator that 401s every request whose
 * `Authorization` header is not exactly `Bearer <expectedToken>`. When the
 * header matches, it speaks MCP normally and exposes one `attest` tool. The
 * auth gate runs in the raw `http.createServer` handler — BEFORE the MCP
 * transport sees the request — so an unresolved `${env:...}` bearer never
 * completes the MCP initialize handshake.
 *
 * @source src/mcp-catalog.ts::standUpModuleLoopback (session-per-transport
 * streamable-HTTP loop); narrowed to one tool + a header auth gate. The
 * auth gate is the new surface this test needs.
 */
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
    // @deliberate: the auth gate: reject anything that is not the exact resolved bearer.
    // A verbatim `Bearer ${env:VALIDATOR_TOKEN}` (today's unresolved form)
    // does NOT match, so the MCP handshake never proceeds.
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
  // @deliberate: hold the SSE GET stream open for the whole dispatch (mirrors the
  // loopback listener's socket-cap disabling).
  httpServer.timeout = 0;
  httpServer.requestTimeout = 0;
  httpServer.keepAliveTimeout = 24 * 60 * 60 * 1000;
  httpServer.headersTimeout = 24 * 60 * 60 * 1000;
  httpServer.on("clientError", (_err, socket) => {
    try {
      socket.destroy();
    } catch {
      /* @deliberate: already gone */
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

/**
 * Build a fake CliHandle that is the product-faithful stand-in for the real
 * `claude` binary reading `--mcp-config`: it dials the host validator MCP
 * server (the entry in `req.tools` other than `rimsky-callback`) using the
 * EXACT headers the executor assembled for that server, calls its `attest`
 * tool, and — only if that succeeds — drives `report_complete` to terminal
 * success. If the validator rejects the connection with 401 (because the
 * `${env:...}` bearer was copied verbatim and never resolved), the validator
 * tool call throws and the CLI exits WITHOUT reporting complete, so the
 * dispatch never reaches success.
 */
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

  // @deliberate: the host validator server is the non-callback http server in the spawn
  // tools. Its `headers` are the resolved `--mcp-config` form the CLI dials
  // with — the spawn-boundary surface under test.
  const validatorTool = req.tools.find(
    (t) => t.name !== "rimsky-callback" && t.kind === "mcp-http",
  );

  const drive = async (): Promise<void> => {
    // @deliberate: (1) Reach the auth-gated validator using EXACTLY the headers the
    // executor assembled for `--mcp-config`. A 401 (unresolved bearer)
    // throws here and we never report complete → the dispatch errors.
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

    // @deliberate: (2) Validator reached → report terminal success via the callback.
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
      // @deliberate: swallow a validator-unreachable failure: the CLI simply exits
      // without reporting complete (exactly what a real CLI does when its
      // configured MCP server is unreachable), and the bridge resolves the
      // dispatch as an Error. The OBSERVABLE outcome on the callback URL is
      // what the test asserts, not this internal failure.
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

    // @deliberate: the node's cli.mcp_servers wires the validator with an ${env:}-referenced
    // bearer header — never the plaintext token.
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
      // @deliberate: inline server with no catalog: allow_inline left unset (permissive
      // legacy path) so the inline validator entry is accepted at dispatch.
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
    // @deliberate: validator reached → header resolved to the real token on the wire →
    // the dispatch reaches terminal success.
    expect(body.error).toBeUndefined();
    expect(body.success).toBeDefined();
    expect(
      ["success", "error", "park"].filter((k) => k in body),
    ).toEqual(["success"]);

    // @deliberate: the validator must have seen the RESOLVED bearer on the wire, never the
    // literal reference form — direct proof the resolution happened at spawn.
    expect(validator.seenAuth).toContain(`Bearer ${VALIDATOR_TOKEN_VALUE}`);
    expect(validator.seenAuth).not.toContain("Bearer ${env:VALIDATOR_TOKEN}");

    // @deliberate: the attributes-derived form the supervisor persists/traces (the parsed
    // cli.mcp_servers shape) MUST keep only the ${env:} reference — never the
    // plaintext token.
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
