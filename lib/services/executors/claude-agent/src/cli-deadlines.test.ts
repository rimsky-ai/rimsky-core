// Copyright © 2026 Fall Guy Consulting.
// Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
// license. See LICENSE.agpl and COPYRIGHT at the repo root.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import pino from "pino";

import { runAgent } from "./agent-run.js";
import { parseCliConfig as parseCliConfigServer } from "./server.js";
import { parseCliConfig as parseCliConfigBridge } from "./http-bridge.js";
import { startInternalMcpServer, type CallbackServerHandle } from "./internal-mcp-server.js";
import { CliConfigError } from "./cli-config-error.js";
import type { CliRunner, CliHandle } from "./cli-runner.js";

const PARSERS = [
  { name: "server.ts", parse: parseCliConfigServer },
  { name: "http-bridge.ts", parse: parseCliConfigBridge },
];

describe("parseCliConfig deadline fields", () => {
  for (const { name, parse } of PARSERS) {
    describe(name, () => {
      it("parses silence_timeout_ms and tool_use_timeout_ms when present", () => {
        const out = parse({ silence_timeout_ms: 120_000, tool_use_timeout_ms: 600_000 });
        expect(out?.silenceTimeoutMs).toBe(120_000);
        expect(out?.toolUseTimeoutMs).toBe(600_000);
      });

      it("leaves fields undefined when absent so the deployment default wins", () => {
        const out = parse({ bare: true });
        expect(out?.silenceTimeoutMs).toBeUndefined();
        expect(out?.toolUseTimeoutMs).toBeUndefined();
      });

      it("accepts zero (explicit disable) as a valid value", () => {
        const out = parse({ silence_timeout_ms: 0, tool_use_timeout_ms: 0 });
        expect(out?.silenceTimeoutMs).toBe(0);
        expect(out?.toolUseTimeoutMs).toBe(0);
      });

      it("throws on a negative silence_timeout_ms", () => {
        expect(() => parse({ silence_timeout_ms: -1 })).toThrow(CliConfigError);
      });

      it("throws on a non-integer silence_timeout_ms", () => {
        expect(() => parse({ silence_timeout_ms: 1.5 })).toThrow(CliConfigError);
      });

      it("throws on a non-number silence_timeout_ms", () => {
        expect(() => parse({ silence_timeout_ms: "30s" })).toThrow(CliConfigError);
      });

      it("throws on a negative tool_use_timeout_ms", () => {
        expect(() => parse({ tool_use_timeout_ms: -10 })).toThrow(CliConfigError);
      });
    });
  }
});

const logger = pino({ level: "silent" });

interface ControlledHandle extends CliHandle {
  emitStdout(s: string): void;
  finishExit(exitCode: number): void;
  sigtermReceived(): boolean;
}

function makeControlledHandle(): ControlledHandle {
  const stdoutCbs: Array<(chunk: string) => void> = [];
  const exitCbs: Array<(code: number | null, signal: NodeJS.Signals | null) => void> = [];
  const exitWaiters: Array<(r: { exitCode: number | null; signal: NodeJS.Signals | null }) => void> = [];
  let exited = false;
  let exitResult: { exitCode: number | null; signal: NodeJS.Signals | null } | null = null;
  let sigterm = false;
  return {
    pid: 12345,
    onStdout: (cb) => stdoutCbs.push(cb),
    onStderr: () => {},
    onExit: (cb) => exitCbs.push(cb),
    sendSigterm: () => { sigterm = true; },
    sendSigkill: () => {
      if (!exited) {
        exited = true;
        exitResult = { exitCode: null, signal: "SIGKILL" };
        for (const cb of exitCbs) cb(null, "SIGKILL");
        for (const w of exitWaiters) w(exitResult);
      }
    },
    waitExit: () =>
      exited && exitResult
        ? Promise.resolve(exitResult)
        : new Promise((resolve) => exitWaiters.push(resolve)),
    emitStdout: (s: string) => {
      for (const cb of stdoutCbs) cb(s);
    },
    finishExit: (code: number) => {
      if (exited) return;
      exited = true;
      exitResult = { exitCode: code, signal: null };
      for (const cb of exitCbs) cb(code, null);
      for (const w of exitWaiters) w(exitResult);
    },
    sigtermReceived: () => sigterm,
  };
}

describe("silence + tool-in-flight detector", () => {
  let cb: CallbackServerHandle;
  let tmpCwd: string;
  let handle: ControlledHandle;
  let cliRunner: CliRunner;

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    cb = await startInternalMcpServer({ logger });
    tmpCwd = mkdtempSync(join(tmpdir(), "cli-deadlines-"));
    writeFileSync(join(tmpCwd, "marker.txt"), "ok");
    handle = makeControlledHandle();
    cliRunner = { spawn: async () => handle };
  });

  afterEach(async () => {
    await cb.close();
    rmSync(tmpCwd, { recursive: true, force: true });
  });

  it("default 0 disables the silence detector — agent doesn't kill a silent CLI", async () => {
    const runPromise = runAgent({
      runId: "11111111-2222-3333-4444-555555555555",
      nodeId: "n-1",
      nodeType: "test-node",
      model: "sonnet",
      systemPrompt: "x",
      userPrompt: "x",
      attributesSchema: {},
      attributes: {},
      cwdOverride: tmpCwd,
      callbackUrl: "",
      cancelToken: "",
      cliRunner,
      callback: cb,
      silenceTimeoutMsDefault: 0,
      toolUseTimeoutMsDefault: 0,
      logger,
      sessionToken: "",
    });
    await new Promise((r) => setTimeout(r, 400));
    expect(handle.sigtermReceived()).toBe(false);
    handle.finishExit(0);
    await runPromise;
  });

  it("per-node cli.silence_timeout_ms overrides the deployment default", async () => {
    const runPromise = runAgent({
      runId: "22222222-2222-3333-4444-555555555555",
      nodeId: "n-2",
      nodeType: "test-node",
      model: "sonnet",
      systemPrompt: "x",
      userPrompt: "x",
      attributesSchema: {},
      attributes: { cli: { silence_timeout_ms: 200 } },
      cliConfig: { silenceTimeoutMs: 200 },
      cwdOverride: tmpCwd,
      callbackUrl: "",
      cancelToken: "",
      cliRunner,
      callback: cb,
      silenceTimeoutMsDefault: 60_000,
      toolUseTimeoutMsDefault: 0,
      logger,
      sessionToken: "",
    });
    const outcome = await runPromise;
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/timeout");
    }
  });

  it("pauses silence detector while a tool_use is open and resumes on tool_use_end", async () => {
    const runPromise = runAgent({
      runId: "33333333-2222-3333-4444-555555555555",
      nodeId: "n-3",
      nodeType: "test-node",
      model: "sonnet",
      systemPrompt: "x",
      userPrompt: "x",
      attributesSchema: {},
      attributes: { cli: { silence_timeout_ms: 200 } },
      cliConfig: { silenceTimeoutMs: 200 },
      cwdOverride: tmpCwd,
      callbackUrl: "",
      cancelToken: "",
      cliRunner,
      callback: cb,
      silenceTimeoutMsDefault: 0,
      toolUseTimeoutMsDefault: 0,
      logger,
      sessionToken: "",
    });
    await new Promise((r) => setTimeout(r, 50));
    handle.emitStdout(
      JSON.stringify({
        type: "assistant",
        message: {
          content: [{ type: "tool_use", id: "toolu_X", name: "Bash" }],
        },
      }) + "\n",
    );
    await new Promise((r) => setTimeout(r, 500));
    expect(handle.sigtermReceived()).toBe(false);
    handle.finishExit(0);
    await runPromise;
  });

  it("fires agent/tool_use_timeout when a tool stays open beyond the budget", async () => {
    const runPromise = runAgent({
      runId: "44444444-2222-3333-4444-555555555555",
      nodeId: "n-4",
      nodeType: "test-node",
      model: "sonnet",
      systemPrompt: "x",
      userPrompt: "x",
      attributesSchema: {},
      attributes: { cli: { tool_use_timeout_ms: 200 } },
      cliConfig: { toolUseTimeoutMs: 200 },
      cwdOverride: tmpCwd,
      callbackUrl: "",
      cancelToken: "",
      cliRunner,
      callback: cb,
      silenceTimeoutMsDefault: 0,
      toolUseTimeoutMsDefault: 0,
      logger,
      sessionToken: "",
    });
    await new Promise((r) => setTimeout(r, 50));
    handle.emitStdout(
      JSON.stringify({
        type: "assistant",
        message: {
          content: [{ type: "tool_use", id: "toolu_Y", name: "Bash" }],
        },
      }) + "\n",
    );
    const outcome = await runPromise;
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/tool_use_timeout");
      expect((outcome.payload as Record<string, unknown>).tool_use_id).toBe("toolu_Y");
      expect((outcome.payload as Record<string, unknown>).tool_name).toBe("Bash");
    }
  });

  it("tool_use_end clears the in-flight state, so subsequent stdout silence is bounded again", async () => {
    const runPromise = runAgent({
      runId: "55555555-2222-3333-4444-555555555555",
      nodeId: "n-5",
      nodeType: "test-node",
      model: "sonnet",
      systemPrompt: "x",
      userPrompt: "x",
      attributesSchema: {},
      attributes: { cli: { silence_timeout_ms: 200 } },
      cliConfig: { silenceTimeoutMs: 200 },
      cwdOverride: tmpCwd,
      callbackUrl: "",
      cancelToken: "",
      cliRunner,
      callback: cb,
      silenceTimeoutMsDefault: 0,
      toolUseTimeoutMsDefault: 0,
      logger,
      sessionToken: "",
    });
    await new Promise((r) => setTimeout(r, 30));
    handle.emitStdout(
      JSON.stringify({
        type: "assistant",
        message: { content: [{ type: "tool_use", id: "toolu_Z", name: "Bash" }] },
      }) + "\n",
    );
    await new Promise((r) => setTimeout(r, 100));
    handle.emitStdout(
      JSON.stringify({
        type: "user",
        message: { content: [{ type: "tool_result", tool_use_id: "toolu_Z", content: "done" }] },
      }) + "\n",
    );
    const outcome = await runPromise;
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/timeout");
    }
  });
});
