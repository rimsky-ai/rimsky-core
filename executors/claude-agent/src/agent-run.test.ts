import { describe, it, expect, beforeEach, afterEach } from "vitest";
import pino from "pino";
import { renderTemplate, runAgent } from "./agent-run.js";
import {
  startInternalMcpServer,
  type CallbackServerHandle,
} from "./internal-mcp-server.js";
import type { CliRunner } from "./cli-runner.js";

const logger = pino({ level: "silent" });

describe("renderTemplate", () => {
  it("substitutes userdata / params / deps / reads vars", () => {
    const out = renderTemplate("sys={{userdata.model}} p={{params.x}} d={{deps.a}} r={{reads.b}}", {
      userdata: { model: "sonnet" },
      params: { x: 7 },
      deps: { a: "alpha" },
      reads: { b: { nested: true } },
    });
    expect(out).toBe('sys=sonnet p=7 d=alpha r={"nested":true}');
  });

  it("preserves {{...}} for missing keys", () => {
    const out = renderTemplate("{{userdata.missing}}", {
      userdata: {},
      params: {},
      deps: {},
      reads: {},
    });
    expect(out).toBe("{{userdata.missing}}");
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

  it("returns stub complete outcome without spawning", async () => {
    const outcome = await runAgent({
      runId: "run-1",
      nodeType: "stub-type",
      model: "sonnet",
      systemPrompt: "you are helpful",
      userPromptTemplate: "do it",
      resultSchema: {},
      templateVars: { userdata: {}, params: {}, deps: {}, reads: {} },
      cliRunner: fakeCli,
      callback: cb,
      silenceTimeoutMs: 1000,
      logger,
    });
    expect(outcome.kind).toBe("complete");
    if (outcome.kind === "complete") {
      expect(outcome.result).toEqual({ stub: true });
      expect(outcome.changed).toBe(true);
      expect(outcome.changeSummary).toBe("stub");
    }
  });
});
