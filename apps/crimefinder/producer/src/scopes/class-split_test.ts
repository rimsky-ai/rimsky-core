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
import { openClassSplit } from "./class-split.js";
import { createPartitionCache, OpenContext } from "./types.js";

const logger = pino({ level: "silent" });
const NOW = "2026-05-19T12:00:00.000+00:00";

describe("openClassSplit", () => {
  let ctx: OpenContext;
  let store: JsonlStore;
  beforeEach(async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-cs-"));
    store = new JsonlStore({ repoRoot: dir, logger });
    ctx = {
      selector: "@class-split:pass_id=p_1",
      claimId: "c_cs",
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

  it("reports class_1_4_remaining true when unresolved class-1 exists", async () => {
    await store.appendFinding({
      kind: "finding",
      id: "a",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_1",
      session_id: "s",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "x.ts",
      line_start: null,
      line_end: null,
      description: "x",
      fingerprint: "sha256:a",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    const r = await openClassSplit(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.class_1_4_remaining).toBe(true);
    expect(p.class_5_findings).toHaveLength(0);
  });

  it("reports class_5_findings when class-5 is present", async () => {
    await store.appendFinding({
      kind: "finding",
      id: "b",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_1",
      session_id: "s",
      class: "5a",
      effective_class: "5a",
      auto_rerouted: false,
      file: "y.ts",
      line_start: null,
      line_end: null,
      description: "x",
      fingerprint: "sha256:b",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    const r = await openClassSplit(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.class_1_4_remaining).toBe(false);
    expect(p.class_5_findings).toHaveLength(1);
  });
});
