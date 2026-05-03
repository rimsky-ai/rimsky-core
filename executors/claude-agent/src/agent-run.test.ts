import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import pino from "pino";
import { renderTemplate, resolveCwd, runAgent } from "./agent-run.js";
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

  it("returns errored invalid_cwd_from_store before any spawn", async () => {
    const outcome = await runAgent({
      runId: "run-1",
      nodeId: "n-1",
      nodeType: "agent",
      model: "sonnet",
      systemPrompt: "you are helpful",
      userPromptTemplate: "do it",
      attributesSchema: {},
      attributes: {},
      templateVars: { userdata: {}, attributes: {} },
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
      expect(outcome.errorClass).toBe("invalid_cwd_from_store");
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
