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
import { openAggregateFindings } from "./aggregate-findings.js";
import { createPartitionCache, OpenContext } from "./types.js";

const logger = pino({ level: "silent" });
const NOW = "2026-05-19T12:00:00.000+00:00";

function findingRow(id: string, file: string, cls: 1 | 2 | 3 | 4 | "5a" | "5b") {
  return {
    kind: "finding" as const,
    id,
    ts: NOW,
    pass_id: "p_1",
    zone_id: "z_1",
    session_id: "s",
    class: cls,
    effective_class: cls,
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

describe("openAggregateFindings", () => {
  let dir: string;
  let ctx: OpenContext;
  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-agg-"));
    const store = new JsonlStore({ repoRoot: dir, logger });
    await store.appendFinding(findingRow("a", "src/x.ts", 1));
    await store.appendFinding(findingRow("b", "src/x.ts", 2));
    await store.appendFinding(findingRow("c", "src/y.ts", "5b"));
    ctx = {
      selector: "@aggregate-findings:pass_id=p_1",
      claimId: "c_a",
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

  it("counts class_1_4 remaining, captures class_5, computes dedup file groups", async () => {
    const r = await openAggregateFindings(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.class_1_4_remaining).toBe(2);
    expect(p.class_5).toHaveLength(1);
    expect(p.dedup_file_groups).toEqual([{ file: "src/x.ts", finding_ids: ["a", "b"] }]);
  });
});
