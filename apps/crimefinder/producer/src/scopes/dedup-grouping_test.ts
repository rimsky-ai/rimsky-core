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
import { openDedupGrouping } from "./dedup-grouping.js";
import { createPartitionCache, OpenContext } from "./types.js";

const logger = pino({ level: "silent" });
const NOW = "2026-05-19T12:00:00.000+00:00";

function f(id: string, file: string) {
  return {
    kind: "finding" as const,
    id,
    ts: NOW,
    pass_id: "p_1",
    zone_id: "z_1",
    session_id: "s",
    class: 1 as const,
    effective_class: 1 as const,
    auto_rerouted: false,
    file,
    line_start: null,
    line_end: null,
    description: id,
    fingerprint: `sha256:${id}`,
    concept_slug: null,
    tension_slug: null,
    confidence: "high" as const,
    status: "open" as const,
    originating_zone_id: null,
  };
}

describe("openDedupGrouping", () => {
  let ctx: OpenContext;
  beforeEach(async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-dedup-"));
    const store = new JsonlStore({ repoRoot: dir, logger });
    await store.appendFinding(f("a", "src/x.ts"));
    await store.appendFinding(f("b", "src/x.ts"));
    await store.appendFinding(f("c", "src/y.ts"));
    ctx = {
      selector: "@dedup-grouping:pass_id=p_1",
      claimId: "c_d",
      repoRoot: dir,
      store,
      tokens: new SessionTokenRegistry(),
      iterCounter: new IterationCounter(store, logger),
      stateEndpointUrl: "url",
      partitionCache: createPartitionCache(),
      config: ConfigSchema.parse({}),
      git: createGitOps(),
      logger,
    };
  });

  it("returns batch_count and caches batches", async () => {
    const r = await openDedupGrouping(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.batch_count).toBeGreaterThan(0);
    expect(ctx.partitionCache.getDedupBatches("p_1")).toBeDefined();
  });
});
