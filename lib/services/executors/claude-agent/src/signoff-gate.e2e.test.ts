// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

// Pass 5 acceptance test for the claude-agent sign-off gate.
//
// This drives the REAL HTTP-bridge `/execute` entry point (not `runAgent`
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
import pino from "pino";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import {
  startHttpBridge,
  type RunningHttpBridge,
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
// The bound output value. The gate is configured on `path: endpoints`, so the
// signature must be over the value AT that path (the array), not the whole
// delta.
const ENDPOINTS = [{ url: "x" }];
const ATTRIBUTES_DELTA = { endpoints: ENDPOINTS };

type ToolStatus =
  | { status: "accepted" }
  | { status: "rejected"; errors: Record<string, string[]> };

function parseToolStatus(content: unknown): ToolStatus {
  const arr = content as Array<{ type: string; text?: string }>;
  return JSON.parse(arr[0]!.text ?? "null") as ToolStatus;
}

// Build a fake CliHandle (mirrors lifecycle.e2e.test.ts::makeFakeHandle) whose
// `beforeExit` connects a real MCP client to the per-dispatch rimsky-callback
// server and drives `report_complete`. The arguments supplied to
// report_complete are produced by `buildArgs` per attempt (so the unsigned
// case can re-submit the same un-signed delta until the budget is exhausted).
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
      // Re-call report_complete until the gate stops rejecting (either it
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

  // Drive the report flow on the next tick (after runAgent registers
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
    // Real mode: the gate only runs in runAgentReal (stub mode short-circuits).
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    posts.length = 0;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await bridge.shutdown();
    await cb.close();
  });

  // Two independent runs prove the gate. Each test starts its own bridge with
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
          // No `signoffs` — the gate must reject every attempt and, after
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
    // AsyncCallbackBody is a one-of: the unsigned outcome must carry exactly
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
          // Sign the value at the configured path (`endpoints`), bound to the
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
    expect(success!.attributes_delta).toEqual(ATTRIBUTES_DELTA);
    // Exactly one outcome key — `success`, never a stray `error`/`park`.
    expect(
      ["success", "error", "park"].filter((k) => k in signedBody),
    ).toEqual(["success"]);
  });
});
