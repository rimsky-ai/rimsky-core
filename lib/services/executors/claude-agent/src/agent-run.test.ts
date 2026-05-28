// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import pino from "pino";
import { resolveCwd, runAgent } from "./agent-run.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type { CliRunner } from "./cli-runner.js";

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
    tools: Array<{ kind: string; name: string; url: string }>;
  }>;
  let fakeCli: CliRunner;

  beforeEach(async () => {
    delete process.env.RIMSKY_EXECUTOR_STUB_MODE;
    cb = await startInternalMcpServer({ logger });
    tmpCwd = mkdtempSync(join(tmpdir(), "agent-run-retry-"));
    writeFileSync(join(tmpCwd, "marker.txt"), "ok");
    resumeInvocations = [];
    // Fake CliHandle that fires "exit 0, no signal" on the next tick
    // and never invokes any registered callback. Both spawn and resume
    // produce the same shape; resume records the call so the test can
    // assert it was used.
    const makeQuietExit0Handle = () => {
      const exitCbs: Array<(code: number | null, signal: NodeJS.Signals | null) => void> = [];
      const exitWaiters: Array<(r: { exitCode: number | null; signal: NodeJS.Signals | null }) => void> = [];
      let exited = false;
      let result: { exitCode: number | null; signal: NodeJS.Signals | null } | null = null;
      // Schedule a clean exit on the next tick.
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
          tools: req.tools.map((t) => ({ kind: t.kind, name: t.name, url: t.url })),
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
    // Resume was attempted exactly once with the runId as session-id.
    expect(resumeInvocations).toHaveLength(1);
    expect(resumeInvocations[0]!.sessionId).toBe(runId);
    expect(resumeInvocations[0]!.prompt).toContain("report_complete");
    // Bug 2 regression: agent-run must pass the rimsky-callback MCP
    // tool config to resume so the resumed subprocess can dial back
    // into the executor's internal MCP server.
    const tool = resumeInvocations[0]!.tools.find((t) => t.name === "rimsky-callback");
    expect(tool).toBeDefined();
    expect(tool!.kind).toBe("mcp-http");
    expect(tool!.url).toMatch(/\/mcp$/);
    // Outcome should be errored, with retry_attempted: true in the payload.
    expect(outcome.kind).toBe("errored");
    if (outcome.kind === "errored") {
      expect(outcome.errorClass).toBe("agent/subprocess_exit/before_complete");
      const payload = outcome.payload as { retry_attempted?: boolean };
      expect(payload.retry_attempted).toBe(true);
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
      expect(outcome.attributesDelta).toEqual({ stub: true });
      expect(outcome.changed).toBe(true);
      expect(outcome.changeSummary).toBe("stub");
    }
  });
});
