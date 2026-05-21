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
import { openFixPartition } from "./fix-partition.js";
import { createPartitionCache, OpenContext } from "./types.js";

const logger = pino({ level: "silent" });

describe("openFixPartition", () => {
  let ctx: OpenContext;
  beforeEach(async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-fix-"));
    const store = new JsonlStore({ repoRoot: dir, logger });
    const cache = createPartitionCache();
    cache.setAffectedZones("p_1", 1, [
      { id: "z_a", label: "src/a", files: ["src/a/x.ts"] },
    ]);
    ctx = {
      selector: "@fix-partition:pass_id=p_1&iter_num=1",
      claimId: "c_fp",
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

  it("returns the affected-zone count under a no-op parent address", async () => {
    // The fan-out PARENT carries no typed-state session; children get their
    // own zone-scoped tokens via openFanOutChild. Mirrors source-tree /
    // re-review-partition / dedup-grouping.
    const r = await openFixPartition(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.iter_num).toBe(1);
    expect(p.affected_zones_count).toBe(1);
    const addr = JSON.parse(new TextDecoder().decode(r.address));
    expect(addr.kind).toBe("no-op");
  });
});
