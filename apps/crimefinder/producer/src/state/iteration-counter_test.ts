import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import pino from "pino";
import { JsonlStore } from "../jsonl-store.js";
import { IterationCounter } from "./iteration-counter.js";

const logger = pino({ level: "silent" });

describe("IterationCounter", () => {
  let dir: string;
  let store: JsonlStore;

  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-iter-"));
    store = new JsonlStore({ repoRoot: dir, logger });
  });

  it("nextFor writes a marker and returns the incremented number", async () => {
    const c = new IterationCounter(store, logger);
    expect(await c.nextFor("p_1")).toBe(1);
    expect(await c.nextFor("p_1")).toBe(2);
    const passes = await store.readPasses();
    const markers = passes.filter((p) => p.kind === "iter_marker");
    expect(markers).toHaveLength(2);
  });

  it("survives recreate via restore (simulated crash)", async () => {
    const a = new IterationCounter(store, logger);
    await a.nextFor("p_1");
    await a.nextFor("p_1");

    const b = new IterationCounter(store, logger);
    await b.restore();
    expect(b.currentFor("p_1")).toBe(2);
    expect(await b.nextFor("p_1")).toBe(3);
  });

  it("isolates different pass_ids", async () => {
    const c = new IterationCounter(store, logger);
    expect(await c.nextFor("p_a")).toBe(1);
    expect(await c.nextFor("p_b")).toBe(1);
    expect(await c.nextFor("p_a")).toBe(2);
  });

  it("restore is idempotent", async () => {
    const a = new IterationCounter(store, logger);
    await a.nextFor("p_1");

    const b = new IterationCounter(store, logger);
    await b.restore();
    await b.restore();
    expect(b.currentFor("p_1")).toBe(1);
  });

  it("nextFor(passId, claimId) is idempotent on claim_id retries", async () => {
    const c = new IterationCounter(store, logger);
    const first = await c.nextFor("p_1", "claim_xyz");
    const second = await c.nextFor("p_1", "claim_xyz");
    expect(first).toBe(1);
    expect(second).toBe(1);
    const passes = await store.readPasses();
    const markers = passes.filter((p) => p.kind === "iter_marker");
    expect(markers).toHaveLength(1);
  });

  it("nextFor idempotency survives restore", async () => {
    const a = new IterationCounter(store, logger);
    await a.nextFor("p_1", "c_a");

    const b = new IterationCounter(store, logger);
    await b.restore();
    const replayed = await b.nextFor("p_1", "c_a");
    expect(replayed).toBe(1);
  });

  it("two concurrent nextFor for same passId serialize cleanly", async () => {
    const c = new IterationCounter(store, logger);
    const results = await Promise.all([c.nextFor("p_x"), c.nextFor("p_x")]);
    expect(results.sort()).toEqual([1, 2]);
    const passes = await store.readPasses();
    const markers = passes.filter((p) => p.kind === "iter_marker" && p.pass_id === "p_x");
    expect(markers).toHaveLength(2);
  });
});
