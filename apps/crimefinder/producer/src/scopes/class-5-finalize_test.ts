import { describe, it, expect } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import pino from "pino";
import { JsonlStore } from "../jsonl-store.js";
import { SessionTokenRegistry } from "../state/session-tokens.js";
import { IterationCounter } from "../state/iteration-counter.js";
import { createGitOps } from "../git-ops.js";
import { ConfigSchema } from "../config.js";
import { openClass5Finalize } from "./class-5-finalize.js";
import { createPartitionCache, OpenContext } from "./types.js";

const logger = pino({ level: "silent" });
const NOW = "2026-05-19T12:00:00.000+00:00";

describe("openClass5Finalize", () => {
  it("buckets class-5 statuses correctly", async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-c5-"));
    const store = new JsonlStore({ repoRoot: dir, logger });
    await store.appendFinding({
      kind: "finding",
      id: "a",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z",
      session_id: "s",
      class: "5a",
      effective_class: "5a",
      auto_rerouted: false,
      file: "f.ts",
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
    await store.appendFinding({
      kind: "finding",
      id: "b",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z",
      session_id: "s",
      class: "5b",
      effective_class: "5b",
      auto_rerouted: true,
      file: "g.ts",
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
    await store.appendFinding({
      kind: "status_update",
      id: "su_1",
      ts: NOW,
      ref: "b",
      status: "deferred",
      by_pass: "p_1",
      by_session: "s",
      reason: "later",
    });
    const ctx: OpenContext = {
      selector: "@class-5-finalize:pass_id=p_1",
      claimId: "c",
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
    const r = await openClass5Finalize(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.class_5_open).toBe(1);
    expect(p.class_5_deferred).toBe(1);
    expect(p.class_5_resolved).toBe(0);
  });
});
