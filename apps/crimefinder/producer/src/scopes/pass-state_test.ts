import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import pino from "pino";
import { JsonlStore } from "../jsonl-store.js";
import { SessionTokenRegistry } from "../state/session-tokens.js";
import { IterationCounter } from "../state/iteration-counter.js";
import { createGitOps } from "../git-ops.js";
import { ConfigSchema } from "../config.js";
import { openPassState } from "./pass-state.js";
import { createPartitionCache, OpenContext } from "./types.js";
import { decodeAddress } from "@crimefinder/shared";

const logger = pino({ level: "silent" });

async function makeCtx(overrides: Partial<OpenContext> = {}): Promise<OpenContext> {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-pass-state-"));
  const store = new JsonlStore({ repoRoot: dir, logger });
  return {
    selector: "@pass-state:new&mission=convergence%20pass&trigger=manual",
    claimId: "c_1",
    repoRoot: dir,
    store,
    tokens: new SessionTokenRegistry(),
    iterCounter: new IterationCounter(store, logger),
    stateEndpointUrl: "localhost:7081",
    partitionCache: createPartitionCache(),
    config: ConfigSchema.parse({}),
    git: createGitOps(),
    logger,
    ...overrides,
  };
}

describe("openPassState", () => {
  let ctx: OpenContext;
  beforeEach(async () => {
    ctx = await makeCtx();
  });

  it("issues a pass-state address with a session token", async () => {
    const r = await openPassState(ctx);
    const addr = decodeAddress(r.address);
    expect(addr.kind).toBe("pass-state");
    if (addr.kind !== "pass-state") throw new Error("kind");
    expect(addr.pass_id).toMatch(/^p_/);
    expect(addr.session_token).toMatch(/^[a-z2-7]{32}$/);
    expect(ctx.tokens.validate(addr.session_token)?.passId).toBe(addr.pass_id);
  });

  it("writes a pass_started row", async () => {
    const r = await openPassState(ctx);
    const rows = await ctx.store.readPasses();
    const started = rows.find((row) => row.kind === "pass_started");
    expect(started).toBeDefined();
    const payload = JSON.parse(new TextDecoder().decode(r.payload));
    expect(started?.kind === "pass_started" && started.id).toBe(payload.pass_id);
  });

  it("payload carries pass_id for substitution", async () => {
    const r = await openPassState(ctx);
    const payload = JSON.parse(new TextDecoder().decode(r.payload));
    expect(payload.pass_id).toMatch(/^p_/);
  });
});
