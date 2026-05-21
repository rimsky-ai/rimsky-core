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
import { openSourceTree } from "./source-tree.js";
import { createPartitionCache, OpenContext } from "./types.js";
import { decodeAddress } from "@crimefinder/shared";

const logger = pino({ level: "silent" });

describe("openSourceTree", () => {
  let dir: string;
  let ctx: OpenContext;
  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-srctree-"));
    await fs.mkdir(path.join(dir, "src"), { recursive: true });
    await fs.writeFile(path.join(dir, "src", "a.ts"), "x");
    await fs.writeFile(path.join(dir, "src", "b.ts"), "y");
    const store = new JsonlStore({ repoRoot: dir, logger });
    ctx = {
      selector: "@source-tree:pass_id=p_1",
      claimId: "c_3",
      repoRoot: dir,
      store,
      tokens: new SessionTokenRegistry(),
      iterCounter: new IterationCounter(store, logger),
      stateEndpointUrl: "localhost:7081",
      partitionCache: createPartitionCache(),
      config: ConfigSchema.parse({}),
      git: createGitOps(),
      logger,
    };
  });

  it("partitions the tree and caches the zone plan", async () => {
    const r = await openSourceTree(ctx);
    const payload = JSON.parse(new TextDecoder().decode(r.payload));
    expect(payload.zone_count).toBeGreaterThan(0);
    expect(ctx.partitionCache.getZonePlan("p_1")?.length).toBe(payload.zone_count);
  });

  it("issues a no-op-shaped address (parent fan-out has no typed-state surface)", async () => {
    const r = await openSourceTree(ctx);
    const addr = decodeAddress(r.address);
    expect(addr.kind).toBe("no-op");
  });
});
