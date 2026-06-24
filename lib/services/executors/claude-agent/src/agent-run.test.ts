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
      claimProducers: { content: { kind: "filesystem", handle: { address: dir } } },
      cwdFromStore: "content",
      cwdOverride: undefined,
    });
    expect(out).toEqual({ kind: "ok", cwd: dir });
  });

  it("errors when the named store handle is missing", () => {
    const out = resolveCwd({
      claimProducers: {},
      cwdFromStore: "content",
      cwdOverride: undefined,
    });
    expect(out.kind).toBe("error");
    if (out.kind === "error") {
      expect(out.message).toMatch(/no claim_producer handle named/);
    }
  });

  it("errors when the address is not a string", () => {
    const out = resolveCwd({
      claimProducers: { content: { kind: "filesystem", handle: { address: 42 } } },
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
      claimProducers: { content: { kind: "filesystem", handle: { address: filePath } } },
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
      claimProducers: {
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
      claimProducers: {},
      cwdFromStore: undefined,
      cwdOverride: dir,
    });
    expect(out).toEqual({ kind: "ok", cwd: dir });
  });

  it("returns ok+undefined when neither field is set", () => {
    const out = resolveCwd({
      claimProducers: {},
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
      claimProducers: {},
      cwdFromStore: "content",
      callbackUrl: "",
      cancelToken: "",
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMsDefault: 1000,
      toolUseTimeoutMsDefault: 0,
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
    const makeQuietExit0Handle = () => {
      const exitCbs: Array<(code: number | null, signal: NodeJS.Signals | null) => void> = [];
      const exitWaiters: Array<(r: { exitCode: number | null; signal: NodeJS.Signals | null }) => void> = [];
      let exited = false;
      let result: { exitCode: number | null; signal: NodeJS.Signals | null } | null = null;
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
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
      logger,
    });
    expect(resumeInvocations).toHaveLength(1);
    expect(resumeInvocations[0]!.sessionId).toBe(runId);
    expect(resumeInvocations[0]!.prompt).toContain("report_complete");
    const tool = resumeInvocations[0]!.tools.find((t) => t.name === "rimsky-callback");
    expect(tool).toBeDefined();
    expect(tool!.kind).toBe("mcp-http");
    expect(tool!.url).toMatch(/\/mcp$/);
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/subprocess_exit/before_complete");
      const payload = outcome.payload as { retry_attempted?: boolean };
      expect(payload.retry_attempted).toBe(true);
    }
  });
});

describe("runAgent resume path lands report_complete over a real MCP client (#11)", () => {
  let tmpCwd: string;
  let fakeCli: CliRunner;

  beforeEach(() => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    tmpCwd = mkdtempSync(join(tmpdir(), "agent-run-resume-"));
    writeFileSync(join(tmpCwd, "marker.txt"), "ok");

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
    };

    fakeCli = {
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
      resume: async (req) => {
        const url = req.env.RIMSKY_CALLBACK_URL;
        const token = req.env.RIMSKY_CALLBACK_TOKEN;
        let forceExit: () => void = () => {};
        const handle = makeControlledHandle((h) => {
          forceExit = () => h.sendSigterm();
        });
        setTimeout(() => {
          void dialAndReport(url, token).catch(() => {
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
      callback: undefined as never,
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
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
      silenceTimeoutMsDefault: 1000,
      toolUseTimeoutMsDefault: 0,
      logger,
    });
    expect(outcome.kind).toBe("complete");
    if (outcome.kind === "complete") {
      expect(outcome.attributesDelta).toEqual({ stub: true, session_token: "run-1" });
      expect(outcome.changed).toBe(true);
      expect(outcome.changeSummary).toBe("stub");
    }
  });
});

describe("runAgent sign-off gate (onComplete layer)", () => {
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
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
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
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
      logger,
      dispatchId: "gate-b-run",
      cliConfig: {
        requiredSignoffs: [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
        maxSignoffAttempts: 1,
      },
    });
    expect(outcome.kind).toBe("complete");
    if (outcome.kind === "complete") {
      expect(outcome.attributesDelta).toEqual({ ...DELTA, session_token: "gate-b-run" });
    }
  });

  it("(c) required_signoffs set but dispatchId empty ⇒ errored agent/signoff_unobtained", async () => {
    const cli = makeDrivingCli(
      completeDriver(() => ({
        changed: true,
        attributes_delta: DELTA,
        signoffs: [signer.sign("", ENDPOINTS)],
      })),
    );
    const outcome = await runAgent({
      runId: "gate-c-run",
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
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
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
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
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
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
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

describe("runAgent surfaces dispatch context end-to-end via dispatch_context_read", () => {
  function makeDispatchContextProbeCli(): CliRunner {
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
        setTimeout(() => {
          void (async () => {
            const transport = new StreamableHTTPClientTransport(new URL(url));
            const client = new Client({
              name: "rimsky-dispatch-context-probe",
              version: "1.0.0",
            });
            try {
              await client.connect(transport);
              const res = await client.callTool({
                name: "dispatch_context_read",
                arguments: { token },
              });
              const arr = res.content as Array<{ text?: string }>;
              const observed = JSON.parse(arr[0]!.text ?? "null") as Record<
                string,
                unknown
              >;
              await client.callTool({
                name: "report_complete",
                arguments: {
                  token,
                  changed: false,
                  attributes_delta: { observed },
                },
              });
            } finally {
              await client.close().catch(() => {});
            }
          })().catch(() => resolveExit());
        }, 0);
        return {
          pid: 9876,
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

  async function runProbe(opts: {
    runId: string;
    dispatchId: string;
    runScopeId: string;
    priorDispatchId?: string;
    priorDispatchDisposition?: string;
  }): Promise<Record<string, unknown>> {
    const outcome = await runAgent({
      runId: opts.runId,
      nodeId: "n-probe",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "sys",
      userPrompt: "u",
      attributesSchema: {},
      attributes: {},
      callbackUrl: "",
      cancelToken: "",
      cliRunner: makeDispatchContextProbeCli(),
      callback: undefined as never,
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
      logger,
      dispatchId: opts.dispatchId,
      runScopeId: opts.runScopeId,
      priorDispatchId: opts.priorDispatchId,
      priorDispatchDisposition: opts.priorDispatchDisposition,
    });
    expect(outcome.kind).toBe("complete");
    if (outcome.kind !== "complete") throw new Error("unreachable");
    const observedHolder = outcome.attributesDelta as Record<string, unknown>;
    return observedHolder.observed as Record<string, unknown>;
  }

  beforeEach(() => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
  });

  it("fresh dispatch reports dispatch_id + run_scope_id with no prior", async () => {
    const observed = await runProbe({
      runId: "run-fresh",
      dispatchId: "d-1",
      runScopeId: "rs-1",
    });
    expect(observed).toEqual({
      dispatch_id: "d-1",
      run_scope_id: "rs-1",
      prior_dispatch_id: null,
      prior_dispatch_disposition: null,
    });
  });

  it("retry-after-error dispatch reports prior_dispatch_id + retry_after_error", async () => {
    const observed = await runProbe({
      runId: "run-retry",
      dispatchId: "d-2",
      runScopeId: "rs-1",
      priorDispatchId: "d-1",
      priorDispatchDisposition: "PRIOR_RETRY_AFTER_ERROR",
    });
    expect(observed).toEqual({
      dispatch_id: "d-2",
      run_scope_id: "rs-1",
      prior_dispatch_id: "d-1",
      prior_dispatch_disposition: "retry_after_error",
    });
  });

  it("stale-recovery dispatch reports prior_dispatch_id + stale_recovery", async () => {
    const observed = await runProbe({
      runId: "run-stale",
      dispatchId: "d-3",
      runScopeId: "rs-1",
      priorDispatchId: "d-2",
      priorDispatchDisposition: "PRIOR_STALE_RECOVERY",
    });
    expect(observed).toEqual({
      dispatch_id: "d-3",
      run_scope_id: "rs-1",
      prior_dispatch_id: "d-2",
      prior_dispatch_disposition: "stale_recovery",
    });
  });

  it("recalculate dispatch reports prior_dispatch_id + recalculate", async () => {
    const observed = await runProbe({
      runId: "run-recalc",
      dispatchId: "d-4",
      runScopeId: "rs-1",
      priorDispatchId: "d-3",
      priorDispatchDisposition: "PRIOR_RECALCULATE",
    });
    expect(observed).toEqual({
      dispatch_id: "d-4",
      run_scope_id: "rs-1",
      prior_dispatch_id: "d-3",
      prior_dispatch_disposition: "recalculate",
    });
  });
});
