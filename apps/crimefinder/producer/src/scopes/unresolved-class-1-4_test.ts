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
import { openUnresolvedClass14 } from "./unresolved-class-1-4.js";
import { createPartitionCache, OpenContext } from "./types.js";

const logger = pino({ level: "silent" });
const NOW = "2026-05-19T12:00:00.000+00:00";

describe("openUnresolvedClass14", () => {
  let ctx: OpenContext;
  let store: JsonlStore;
  beforeEach(async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-unresolved-"));
    store = new JsonlStore({ repoRoot: dir, logger });
    const cache = createPartitionCache();
    cache.setZonePlan("p_1", [
      { id: "z_a", label: "src/a", files: ["src/a/x.ts"] },
      { id: "z_b", label: "src/b", files: ["src/b/y.ts"] },
    ]);
    ctx = {
      selector: "@unresolved-class-1-4:pass_id=p_1",
      claimId: "c_u",
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

  it("returns iter_num 1 on first call and lists affected zones", async () => {
    await store.appendFinding({
      kind: "finding",
      id: "f_x",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_a",
      session_id: "s",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "src/a/x.ts",
      line_start: null,
      line_end: null,
      description: "x",
      fingerprint: "sha256:x",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    const r = await openUnresolvedClass14(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.iter_num).toBe(1);
    expect(p.affected_zones).toEqual(["z_a"]);
    expect(p.skipped).toBe(false);
  });

  it("returns skipped:true when no unresolved class-1-4 work remains", async () => {
    const r = await openUnresolvedClass14(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.iter_num).toBe(1);
    expect(p.affected_zones).toEqual([]);
    expect(p.skipped).toBe(true);
  });
});
