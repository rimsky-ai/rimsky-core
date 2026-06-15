// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import pino from "pino";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { resolveCwd, runAgent } from "./agent-run.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type { CliHandle, CliRunner, CliSpawnRequest } from "./cli-runner.js";
import { makeTestSigner } from "./signoff-test-signer.js";

const logger = pino({ level: "silent" });

describe("resolveCwd", () => {
  let dir: string;

  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), "claude-agent-cwd-"));
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("returns ok+cwd when cwdFromStore points at an existing directory", () => {
    const out = resolveCwd({
      stores: { content: { kind: "filesystem", handle: { address: dir } } },
      cwdFromStore: "content",
      cwdOverride: undefined,
    });
    expect(out).toEqual({ kind: "ok", cwd: dir });
  });

  it("errors when the named store handle is missing", () => {
    const out = resolveCwd({
      stores: {},
      cwdFromStore: "content",
      cwdOverride: undefined,
    });
    expect(out.kind).toBe("error");
    if (out.kind === "error") {
      expect(out.message).toMatch(/no store handle named/);
    }
  });

  it("errors when the address is not a string", () => {
    const out = resolveCwd({
      stores: { content: { kind: "filesystem", handle: { address: 42 } } },
      cwdFromStore: "content",
      cwdOverride: undefined,
    });
    expect(out.kind).toBe("error");
    if (out.kind === "error") {
      expect(out.message).toMatch(/address is not a non-empty string/);
    }
  });

  it("errors when the address is a file, not a directory", () => {
    const filePath = join(dir, "not-a-dir.txt");
    writeFileSync(filePath, "x");
    const out = resolveCwd({
      stores: { content: { kind: "filesystem", handle: { address: filePath } } },
      cwdFromStore: "content",
      cwdOverride: undefined,
    });
    expect(out.kind).toBe("error");
    if (out.kind === "error") {
      expect(out.message).toMatch(/exists but is not a directory/);
    }
  });

  it("errors when the address path does not exist", () => {
    const out = resolveCwd({
      stores: {
        content: { kind: "filesystem", handle: { address: join(dir, "nope") } },
      },
      cwdFromStore: "content",
      cwdOverride: undefined,
    });
    expect(out.kind).toBe("error");
    if (out.kind === "error") {
      expect(out.message).toMatch(/stat .* failed/);
    }
  });

  it("falls back to cwdOverride when cwdFromStore is unset", () => {
    const out = resolveCwd({
      stores: {},
      cwdFromStore: undefined,
      cwdOverride: dir,
    });
    expect(out).toEqual({ kind: "ok", cwd: dir });
  });

  it("returns ok+undefined when neither field is set", () => {
    const out = resolveCwd({
      stores: {},
      cwdFromStore: undefined,
      cwdOverride: undefined,
    });
    expect(out).toEqual({ kind: "ok", cwd: undefined });
  });
});

describe("runAgent in real mode short-circuits on invalid cwd_from_store", () => {
  let cb: CallbackServerHandle;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("cliRunner.spawn must not be called when cwd resolution fails");
    },
  };

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    await cb.close();
  });

  it("returns errored agent/attribute_invalid before any spawn", async () => {
    const outcome = await runAgent({
      runId: "run-1",
      nodeId: "n-1",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "you are helpful",
      userPrompt: "do it",
      attributesSchema: {},
      attributes: {},
      stores: {},
      cwdFromStore: "content",
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMs: 1000,
      logger,
    });
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/attribute_invalid");
    }
  });
});

describe("runAgent retries via resume() when subprocess exits clean without report", () => {
  let cb: CallbackServerHandle;
  let tmpCwd: string;
  let resumeInvocations: Array<{
    sessionId: string;
    prompt: string;
    tools: Array<{ kind: string; name: string; url?: string }>;
  }>;
  let fakeCli: CliRunner;

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    cb = await startInternalMcpServer({ logger });
    tmpCwd = mkdtempSync(join(tmpdir(), "agent-run-retry-"));
    writeFileSync(join(tmpCwd, "marker.txt"), "ok");
    resumeInvocations = [];
    // @deliberate: fake CliHandle that fires "exit 0, no signal" on the next tick
    // and never invokes any registered callback. Both spawn and resume
    // produce the same shape; resume records the call so the test can
    // assert it was used.
    const makeQuietExit0Handle = () => {
      const exitCbs: Array<(code: number | null, signal: NodeJS.Signals | null) => void> = [];
      const exitWaiters: Array<(r: { exitCode: number | null; signal: NodeJS.Signals | null }) => void> = [];
      let exited = false;
      let result: { exitCode: number | null; signal: NodeJS.Signals | null } | null = null;
      // @deliberate: schedule a clean exit on the next tick.
      setTimeout(() => {
        exited = true;
        result = { exitCode: 0, signal: null };
        for (const cb of exitCbs) cb(0, null);
        for (const w of exitWaiters) w(result);
      }, 5);
      return {
        pid: 99999,
        onStdout: () => {},
        onStderr: () => {},
        onExit: (cb: (code: number | null, signal: NodeJS.Signals | null) => void) => {
          exitCbs.push(cb);
        },
        sendSigterm: () => {},
        sendSigkill: () => {},
        waitExit: () =>
          exited && result
            ? Promise.resolve(result)
            : new Promise<{ exitCode: number | null; signal: NodeJS.Signals | null }>((resolve) =>
                exitWaiters.push(resolve),
              ),
      };
    };
    fakeCli = {
      spawn: async () => makeQuietExit0Handle(),
      resume: async (req) => {
        resumeInvocations.push({
          sessionId: req.sessionId,
          prompt: req.prompt,
          tools: req.tools.map((t) => ({
            kind: t.kind,
            name: t.name,
            url: t.kind === "mcp-http" ? t.url : undefined,
          })),
        });
        return makeQuietExit0Handle();
      },
    };
  });

  afterEach(async () => {
    await cb.close();
    rmSync(tmpCwd, { recursive: true, force: true });
  });

  it("invokes resume() exactly once and returns errored with retry_attempted=true when retry also exits without report", async () => {
    const runId = "11111111-2222-3333-4444-555555555555";
    const outcome = await runAgent({
      runId,
      nodeId: "n-1",
      nodeType: "area-pass",
      model: "sonnet",
      systemPrompt: "you are helpful",
      userPrompt: "do it",
      attributesSchema: {},
      attributes: {},
      cwdOverride: tmpCwd,
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMs: 60_000,
      logger,
    });
    // @deliberate: resume was attempted exactly once with the runId as session-id.
    expect(resumeInvocations).toHaveLength(1);
    expect(resumeInvocations[0]!.sessionId).toBe(runId);
    expect(resumeInvocations[0]!.prompt).toContain("report_complete");
    // @deliberate: bug 2 regression: agent-run must pass the rimsky-callback MCP
    // tool config to resume so the resumed subprocess can dial back
    // into the executor's internal MCP server.
    const tool = resumeInvocations[0]!.tools.find((t) => t.name === "rimsky-callback");
    expect(tool).toBeDefined();
    expect(tool!.kind).toBe("mcp-http");
    expect(tool!.url).toMatch(/\/mcp$/);
    // @deliberate: outcome should be errored, with retry_attempted: true in the payload.
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/subprocess_exit/before_complete");
      const payload = outcome.payload as { retry_attempted?: boolean };
      expect(payload.retry_attempted).toBe(true);
    }
  });
});

describe("runAgent resume path lands report_complete over a real MCP client (#11)", () => {
  // @deliberate: #11 resume path: the CLI exits 0 WITHOUT calling report_complete, the
  // executor resumes the same session, and the resumed CLI dials the
  // SAME per-dispatch internal-MCP server (held alive across resume) and
  // calls report_complete. The dispatch must resolve `complete`, NOT
  // `agent/subprocess_exit/before_complete`. This drives the resume path
  // against a REAL MCP client/server round-trip (not the no-op fake CLI),
  // proving the per-dispatch MCP connection survives the spawn → exit →
  // resume boundary and the terminal `report_complete` lands.
  let tmpCwd: string;
  let fakeCli: CliRunner;

  beforeEach(() => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    tmpCwd = mkdtempSync(join(tmpdir(), "agent-run-resume-"));
    writeFileSync(join(tmpCwd, "marker.txt"), "ok");

    // @deliberate: fake CLI handle whose `waitExit` resolves only once
    // teardown (sendSigterm) is invoked — mirrors a real subprocess that
    // stays alive while doing work, then exits on the executor's terminal
    // teardown. `dialAndReport` lets the resume handler perform a real
    // MCP round-trip after the executor has wired its retry handle.
    const makeControlledHandle = (
      onSpawned: (h: CliHandle) => void,
    ): CliHandle => {
      const exitWaiters: Array<
        (r: { exitCode: number | null; signal: NodeJS.Signals | null }) => void
      > = [];
      let exited = false;
      const resolveExit = (): void => {
        if (exited) return;
        exited = true;
        for (const w of exitWaiters) w({ exitCode: 0, signal: null });
      };
      const handle: CliHandle = {
        pid: 4242,
        onStdout: () => {},
        onStderr: () => {},
        onExit: () => {},
        sendSigterm: () => resolveExit(),
        sendSigkill: () => resolveExit(),
        waitExit: () =>
          exited
            ? Promise.resolve({ exitCode: 0, signal: null })
            : new Promise((resolve) => exitWaiters.push(resolve)),
      };
      onSpawned(handle);
      return handle;
    };

    const dialAndReport = async (
      url: string,
      token: string,
    ): Promise<void> => {
      const transport = new StreamableHTTPClientTransport(new URL(url));
      const client = new Client({
        name: "rimsky-resume-test-cli",
        version: "1.0.0",
      });
      try {
        await client.connect(transport);
        await client.callTool({
          name: "report_complete",
          arguments: { token, changed: true, change_summary: "resumed-done" },
        });
      } finally {
        await client.close().catch(() => {});
      }
      // @deliberate: the resumed CLI does NOT self-exit: a real subprocess stays alive
      // until the executor's terminal teardown (SIGTERM) after
      // report_complete's response is flushed. Driving exit from teardown
      // (sendSigterm → waitExit) mirrors that and avoids racing the
      // deferred terminal resolution.
    };

    fakeCli = {
      // @deliberate: first dispatch: a clean exit 0 with no report (triggers resume).
      spawn: async () => {
        const exitWaiters: Array<
          (r: { exitCode: number | null; signal: NodeJS.Signals | null }) => void
        > = [];
        let result: { exitCode: number | null; signal: NodeJS.Signals | null } | null = null;
        setTimeout(() => {
          result = { exitCode: 0, signal: null };
          for (const w of exitWaiters) w(result);
        }, 5);
        return {
          pid: 1111,
          onStdout: () => {},
          onStderr: () => {},
          onExit: () => {},
          sendSigterm: () => {},
          sendSigkill: () => {},
          waitExit: () =>
            result
              ? Promise.resolve(result)
              : new Promise((resolve) => exitWaiters.push(resolve)),
        };
      },
      // @deliberate: resume: dial the per-dispatch MCP and call report_complete. The
      // MCP round-trip is scheduled AFTER this handler returns so the
      // executor has wired its retry handle (handleRef) before teardown.
      resume: async (req) => {
        const url = req.env.RIMSKY_CALLBACK_URL;
        const token = req.env.RIMSKY_CALLBACK_TOKEN;
        let forceExit: () => void = () => {};
        const handle = makeControlledHandle((h) => {
          forceExit = () => h.sendSigterm();
        });
        setTimeout(() => {
          void dialAndReport(url, token).catch(() => {
            // @deliberate: on dial failure, let the handle exit so the test does not
            // hang; the outcome assertion then catches the regression.
            forceExit();
          });
        }, 0);
        return handle;
      },
    };
  });

  afterEach(() => {
    rmSync(tmpCwd, { recursive: true, force: true });
  });

  it("resolves complete (not subprocess_exit/before_complete) when report_complete lands on resume", async () => {
    const runId = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee";
    const outcome = await runAgent({
      runId,
      nodeId: "n-1",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "you are helpful",
      userPrompt: "do it",
      attributesSchema: {},
      attributes: {},
      cwdOverride: tmpCwd,
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      // @deliberate: no `callback` handle passed: runAgent starts its OWN per-dispatch
      // internal-MCP server, which is the connection under test.
      callback: undefined as never,
      silenceTimeoutMs: 60_000,
      logger,
    });
    expect(outcome.kind).toBe("complete");
    if (outcome.kind === "complete") {
      expect(outcome.changed).toBe(true);
      expect(outcome.changeSummary).toBe("resumed-done");
    }
  });
});

describe("runAgent in stub mode", () => {
  let cb: CallbackServerHandle;
  const fakeCli: CliRunner = {
    spawn: async () => {
      throw new Error("should not be called in stub mode");
    },
  };

  beforeEach(async () => {
    process.env.RIMSKY_EXECUTOR_STUB_MODE = "1";
    cb = await startInternalMcpServer({ logger });
  });

  afterEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    await cb.close();
  });

  it("returns stub complete outcome with attributesDelta without spawning", async () => {
    const outcome = await runAgent({
      runId: "run-1",
      nodeId: "n-1",
      nodeType: "stub-type",
      model: "sonnet",
      systemPrompt: "you are helpful",
      userPrompt: "do it",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMs: 1000,
      logger,
    });
    expect(outcome.kind).toBe("complete");
    if (outcome.kind === "complete") {
      // @constraint: stub mode stamps `session_token: runId` on every
      // terminal Success (mirroring runAgentReal); the session_token
      // field rides along.
      expect(outcome.attributesDelta).toEqual({ stub: true, session_token: "run-1" });
      expect(outcome.changed).toBe(true);
      expect(outcome.changeSummary).toBe("stub");
    }
  });
});

// @deliberate: onComplete-layer coverage for the sign-off gate. These drive `runAgent`
// with a fake CLI whose handle connects a REAL MCP client to the per-dispatch
// rimsky-callback server and issues a terminal report (report_complete or
// report_error). The gate runs inside `onComplete`; the assertion is the
// resolved AgentOutcome. The fake handle stays alive until teardown
// (sendSigterm) drives its exit, mirroring a real subprocess that lingers
// until the executor's terminal teardown after the report lands — this avoids
// racing the deferred terminal resolution.
describe("runAgent sign-off gate (onComplete layer)", () => {
  // @deliberate: build a fake CLI whose handle dials the per-dispatch MCP and calls a tool.
  // `drive(url, token)` performs the MCP round-trip; the handle resolves exit
  // only when teardown sends SIGTERM (controlled-handle pattern from the #11
  // resume test) so the deferred terminal `safeResolve` wins the race.
  function makeDrivingCli(
    drive: (url: string, token: string) => Promise<void>,
  ): CliRunner {
    return {
      spawn: async (req: CliSpawnRequest) => {
        const exitWaiters: Array<
          (r: { exitCode: number | null; signal: NodeJS.Signals | null }) => void
        > = [];
        let exited = false;
        const resolveExit = (): void => {
          if (exited) return;
          exited = true;
          for (const w of exitWaiters) w({ exitCode: 0, signal: null });
        };
        const url = req.env.RIMSKY_CALLBACK_URL;
        const token = req.env.RIMSKY_CALLBACK_TOKEN;
        // @deliberate: drive the MCP round-trip on the next tick so runAgent has wired the
        // handle (handleRef) before teardown can fire.
        setTimeout(() => {
          void drive(url, token).catch(() => resolveExit());
        }, 0);
        return {
          pid: 7777,
          onStdout: () => {},
          onStderr: () => {},
          onExit: () => {},
          sendSigterm: () => resolveExit(),
          sendSigkill: () => resolveExit(),
          waitExit: () =>
            exited
              ? Promise.resolve({ exitCode: 0, signal: null })
              : new Promise((resolve) => exitWaiters.push(resolve)),
        };
      },
    };
  }

  // @deliberate: report_complete with the given args, re-calling until the gate stops
  // rejecting (accept, or budget-exhausted commit). A hard cap guards against
  // an infinite loop. The CLI does NOT self-exit; teardown drives exit.
  function completeDriver(
    buildArgs: () => Record<string, unknown>,
  ): (url: string, token: string) => Promise<void> {
    return async (url, token) => {
      const transport = new StreamableHTTPClientTransport(new URL(url));
      const client = new Client({
        name: "rimsky-signoff-unit-cli",
        version: "1.0.0",
      });
      try {
        await client.connect(transport);
        for (let attempt = 0; attempt < 10; attempt++) {
          const res = await client.callTool({
            name: "report_complete",
            arguments: { token, ...buildArgs() },
          });
          const arr = res.content as Array<{ text?: string }>;
          const status = JSON.parse(arr[0]!.text ?? "null") as {
            status: string;
          };
          if (status.status !== "rejected") break;
        }
      } finally {
        await client.close().catch(() => {});
      }
    };
  }

  const ENDPOINTS = [{ url: "x" }];
  const DELTA = { endpoints: ENDPOINTS };

  let signer: ReturnType<typeof makeTestSigner>;

  beforeEach(() => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    signer = makeTestSigner();
  });

  it("(a) missing signature ⇒ rejected then errored agent/signoff_unobtained after exhaustion", async () => {
    const cli = makeDrivingCli(
      completeDriver(() => ({ changed: true, attributes_delta: DELTA })),
    );
    const outcome = await runAgent({
      runId: "gate-a-run",
      nodeId: "n-a",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "sys",
      userPrompt: "u",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: cli,
      callback: undefined as never,
      silenceTimeoutMs: 60_000,
      logger,
      dispatchId: "gate-a-run",
      cliConfig: {
        requiredSignoffs: [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
        maxSignoffAttempts: 1,
      },
    });
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/signoff_unobtained");
    }
  });

  it("(b) valid signature ⇒ complete", async () => {
    const cli = makeDrivingCli(
      completeDriver(() => ({
        changed: true,
        attributes_delta: DELTA,
        signoffs: [signer.sign("gate-b-run", ENDPOINTS)],
      })),
    );
    const outcome = await runAgent({
      runId: "gate-b-run",
      nodeId: "n-b",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "sys",
      userPrompt: "u",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: cli,
      callback: undefined as never,
      silenceTimeoutMs: 60_000,
      logger,
      dispatchId: "gate-b-run",
      cliConfig: {
        requiredSignoffs: [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
        maxSignoffAttempts: 1,
      },
    });
    expect(outcome.kind).toBe("complete");
    if (outcome.kind === "complete") {
      // @constraint: every terminal Success augments the committed
      // delta with `session_token: runId` to ride the rimsky attribute
      // carry-forward mechanism.
      expect(outcome.attributesDelta).toEqual({ ...DELTA, session_token: "gate-b-run" });
    }
  });

  it("(c) required_signoffs set but dispatchId empty ⇒ errored agent/signoff_unobtained", async () => {
    // @deliberate: even a VALID signature cannot rescue an empty dispatch_id — the binding
    // id is empty, so the gate refuses to verify and treats it as a usage
    // error rather than letting (un-bindable) output through.
    const cli = makeDrivingCli(
      completeDriver(() => ({
        changed: true,
        attributes_delta: DELTA,
        signoffs: [signer.sign("", ENDPOINTS)],
      })),
    );
    const outcome = await runAgent({
      runId: "gate-c-run", // @deliberate: runId is set; dispatchId is the empty one
      nodeId: "n-c",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "sys",
      userPrompt: "u",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: cli,
      callback: undefined as never,
      silenceTimeoutMs: 60_000,
      logger,
      dispatchId: "",
      cliConfig: {
        requiredSignoffs: [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
        maxSignoffAttempts: 1,
      },
    });
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/signoff_unobtained");
      const payload = outcome.payload as { error?: string };
      expect(payload.error).toMatch(/dispatch_id required/);
    }
  });

  it("(d) anti-tamper: a cli.required_signoffs override in attributes_delta is ignored", async () => {
    // @deliberate: the agent tries to disable its own gate by putting an empty
    // `cli.required_signoffs` inside the delta. The gate reads the gate from
    // DISPATCH-TIME cliConfig, not from the delta, so the otherwise-unsigned
    // delta is still rejected and the run errors.
    const tamperDelta = {
      endpoints: ENDPOINTS,
      cli: { required_signoffs: [] },
    };
    const cli = makeDrivingCli(
      completeDriver(() => ({ changed: true, attributes_delta: tamperDelta })),
    );
    const outcome = await runAgent({
      runId: "gate-d-run",
      nodeId: "n-d",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "sys",
      userPrompt: "u",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: cli,
      callback: undefined as never,
      silenceTimeoutMs: 60_000,
      logger,
      dispatchId: "gate-d-run",
      cliConfig: {
        requiredSignoffs: [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
        maxSignoffAttempts: 1,
      },
    });
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/signoff_unobtained");
    }
  });

  it("(e) gate guards success only: report_error still terminal-errors with its own class", async () => {
    // @deliberate: with the gate configured, an honest report_error is NOT gated — it
    // resolves a terminal errored outcome carrying the agent-supplied class,
    // never agent/signoff_unobtained.
    const errorDriver = async (url: string, token: string): Promise<void> => {
      const transport = new StreamableHTTPClientTransport(new URL(url));
      const client = new Client({
        name: "rimsky-signoff-unit-cli-err",
        version: "1.0.0",
      });
      try {
        await client.connect(transport);
        await client.callTool({
          name: "report_error",
          arguments: {
            token,
            error_class: "agent/validator_rejected",
            payload: { reason: "validator said no" },
          },
        });
      } finally {
        await client.close().catch(() => {});
      }
    };
    const cli = makeDrivingCli(errorDriver);
    const outcome = await runAgent({
      runId: "gate-e-run",
      nodeId: "n-e",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "sys",
      userPrompt: "u",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: cli,
      callback: undefined as never,
      silenceTimeoutMs: 60_000,
      logger,
      dispatchId: "gate-e-run",
      cliConfig: {
        requiredSignoffs: [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
        maxSignoffAttempts: 1,
      },
    });
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/validator_rejected");
    }
  });
});
