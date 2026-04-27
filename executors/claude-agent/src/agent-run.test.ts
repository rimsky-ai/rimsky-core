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
  it("substitutes userdata and attributes vars", () => {
    const out = renderTemplate(
      "sys={{userdata.model}} a={{attributes.area}} obj={{attributes.nested}}",
      {
        userdata: { model: "sonnet" },
        attributes: { area: "alpha", nested: { ok: true } },
      },
    );
    expect(out).toBe('sys=sonnet a=alpha obj={"ok":true}');
  });

  it("preserves {{...}} for missing keys", () => {
    const out = renderTemplate("{{userdata.missing}} {{attributes.gone}}", {
      userdata: {},
      attributes: {},
    });
    expect(out).toBe("{{userdata.missing}} {{attributes.gone}}");
  });

  it("ignores unknown namespaces (preserves verbatim)", () => {
    const out = renderTemplate("{{deps.foo}}", {
      userdata: {},
      attributes: {},
    });
    expect(out).toBe("{{deps.foo}}");
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
      userPromptTemplate: "do it",
      attributesSchema: {},
      attributes: {},
      templateVars: { userdata: {}, attributes: {} },
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
