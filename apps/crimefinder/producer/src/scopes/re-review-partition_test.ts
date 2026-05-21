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
import { openReReviewPartition } from "./re-review-partition.js";
import { createPartitionCache, OpenContext } from "./types.js";

const logger = pino({ level: "silent" });

describe("openReReviewPartition", () => {
  let ctx: OpenContext;
  beforeEach(async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-rrp-"));
    const store = new JsonlStore({ repoRoot: dir, logger });
    const cache = createPartitionCache();
    cache.setAffectedZones("p_1", 2, [
      { id: "z_a", label: "src/a", files: ["src/a/x.ts"] },
      { id: "z_b", label: "src/b", files: ["src/b/y.ts"] },
    ]);
    ctx = {
      selector: "@re-review-partition:pass_id=p_1&iter_num=2",
      claimId: "c_rrp",
      repoRoot: dir,
      store,
      tokens: new SessionTokenRegistry(),
      iterCounter: new IterationCounter(store, logger),
      stateEndpointUrl: "url",
      partitionCache: cache,
      config: ConfigSchema.parse({}),
      git: createGitOps(),
      logger,
    };
  });

  it("returns the affected-zone count", async () => {
    const r = await openReReviewPartition(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.iter_num).toBe(2);
    expect(p.affected_zones_count).toBe(2);
  });
});
