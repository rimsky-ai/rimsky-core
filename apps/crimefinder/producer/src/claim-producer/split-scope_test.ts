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
import { splitScope } from "./split-scope.js";
import { createPartitionCache, OpenContext } from "../scopes/types.js";

const logger = pino({ level: "silent" });

async function makeCtx(): Promise<OpenContext> {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-split-"));
  const store = new JsonlStore({ repoRoot: dir, logger });
  return {
    selector: "",
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
}

describe("splitScope", () => {
  it("splits source-tree-partition by cached zones", async () => {
    const ctx = await makeCtx();
    ctx.partitionCache.setZonePlan("p_1", [
      { id: "z_a", label: "src/a", files: ["src/a/x.ts"] },
      { id: "z_b", label: "src/b", files: ["src/b/y.ts"] },
    ]);
    const out = await splitScope({
      parentClaimHandleId: "c",
      parentScope: new Uint8Array(),
      partitionRequest: new TextEncoder().encode(
        JSON.stringify({ kind: "source-tree-partition", pass_id: "p_1" }),
      ),
      ctx,
    });
    expect(out).toHaveLength(2);
    expect(out[0].partitionKey).toBe("z_a");
  });

  it("splits dedup-partition by cached batches", async () => {
    const ctx = await makeCtx();
    ctx.partitionCache.setDedupBatches("p_1", [
      [{ file: "x.ts", findingIds: ["a", "b"] }],
      [{ file: "y.ts", findingIds: ["c", "d"] }],
    ]);
    const out = await splitScope({
      parentClaimHandleId: "c",
      parentScope: new Uint8Array(),
      partitionRequest: new TextEncoder().encode(
        JSON.stringify({ kind: "dedup-partition", pass_id: "p_1" }),
      ),
      ctx,
    });
    expect(out).toHaveLength(2);
  });

  it("splits fix-partition by cached affected zones", async () => {
    const ctx = await makeCtx();
    ctx.partitionCache.setAffectedZones("p_1", 2, [
      { id: "z_a", label: "src/a", files: ["src/a/x.ts"] },
    ]);
    // Seed an unresolved class-1 finding so z_a's fix-cycle bucket is
    // non-empty; splitAffected drops zones without unresolved class-1-4
    // findings from the dispatch set.
    await ctx.store.appendFinding({
      kind: "finding",
      id: "f_seed",
      ts: "2026-05-19T12:00:00.000+00:00",
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
      fingerprint: "sha256:seed",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    const out = await splitScope({
      parentClaimHandleId: "c",
      parentScope: new Uint8Array(),
      partitionRequest: new TextEncoder().encode(
        JSON.stringify({ kind: "fix-partition", pass_id: "p_1", iter_num: 2 }),
      ),
      ctx,
    });
    expect(out).toHaveLength(1);
  });

  it("emits per-child iter_num and assigned_finding_ids on fix-partition scopeData", async () => {
    const ctx = await makeCtx();
    ctx.partitionCache.setAffectedZones("p_1", 3, [
      { id: "z_a", label: "src/a", files: ["src/a/x.ts"] },
      { id: "z_b", label: "src/b", files: ["src/b/y.ts"] },
    ]);
    // Seed unresolved class-1 findings in zone a only. Zone b's bucket is
    // empty, so splitAffected drops it from the fix-cycle dispatch set —
    // there is no class-1-4 unresolved work in z_b.
    const NOW = "2026-05-19T12:00:00.000+00:00";
    await ctx.store.appendFinding({
      kind: "finding",
      id: "f_a1",
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
      fingerprint: "sha256:a1",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await ctx.store.appendFinding({
      kind: "finding",
      id: "f_a2",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_a",
      session_id: "s",
      class: 2,
      effective_class: 2,
      auto_rerouted: false,
      file: "src/a/x.ts",
      line_start: null,
      line_end: null,
      description: "y",
      fingerprint: "sha256:a2",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    const out = await splitScope({
      parentClaimHandleId: "c",
      parentScope: new Uint8Array(),
      partitionRequest: new TextEncoder().encode(
        JSON.stringify({ kind: "fix-partition", pass_id: "p_1", iter_num: 3 }),
      ),
      ctx,
    });
    expect(out).toHaveLength(1);
    const aScope = JSON.parse(new TextDecoder().decode(out[0].scopeData));
    expect(aScope.zone_id).toBe("z_a");
    expect(aScope.iter_num).toBe(3);
    expect(aScope.assigned_finding_ids.sort()).toEqual(["f_a1", "f_a2"]);
    expect(aScope.role).toBe("fix-cycle");
  });

  it("skips zones with empty fix buckets in fix-partition", async () => {
    // Two affected zones, but only z_a has unresolved class-1-4 findings.
    // z_b has a 5a finding (out of fix-cycle scope) and z_c has a fixed
    // class-1 finding (resolved). Neither should produce a child.
    const ctx = await makeCtx();
    ctx.partitionCache.setAffectedZones("p_1", 1, [
      { id: "z_a", label: "src/a", files: ["src/a/x.ts"] },
      { id: "z_b", label: "src/b", files: ["src/b/y.ts"] },
      { id: "z_c", label: "src/c", files: ["src/c/z.ts"] },
    ]);
    const NOW = "2026-05-19T12:00:00.000+00:00";
    await ctx.store.appendFinding({
      kind: "finding",
      id: "f_a",
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
      description: "a",
      fingerprint: "sha256:a",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await ctx.store.appendFinding({
      kind: "finding",
      id: "f_b",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_b",
      session_id: "s",
      class: "5a",
      effective_class: "5a",
      auto_rerouted: false,
      file: "src/b/y.ts",
      line_start: null,
      line_end: null,
      description: "b",
      fingerprint: "sha256:b",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await ctx.store.appendFinding({
      kind: "finding",
      id: "f_c",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_c",
      session_id: "s",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "src/c/z.ts",
      line_start: null,
      line_end: null,
      description: "c",
      fingerprint: "sha256:c",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await ctx.store.appendFinding({
      kind: "status_update",
      id: "su_c",
      ts: NOW,
      ref: "f_c",
      status: "fixed",
      by_pass: "p_1",
      by_session: "s",
      resolved_at_commit: "deadbeef",
    });
    const out = await splitScope({
      parentClaimHandleId: "c",
      parentScope: new Uint8Array(),
      partitionRequest: new TextEncoder().encode(
        JSON.stringify({ kind: "fix-partition", pass_id: "p_1", iter_num: 1 }),
      ),
      ctx,
    });
    expect(out).toHaveLength(1);
    const scope = JSON.parse(new TextDecoder().decode(out[0].scopeData));
    expect(scope.zone_id).toBe("z_a");
  });

  it("re-review-partition carries iter_num but no assigned_finding_ids", async () => {
    const ctx = await makeCtx();
    ctx.partitionCache.setAffectedZones("p_1", 2, [
      { id: "z_a", label: "src/a", files: ["src/a/x.ts"] },
    ]);
    const out = await splitScope({
      parentClaimHandleId: "c",
      parentScope: new Uint8Array(),
      partitionRequest: new TextEncoder().encode(
        JSON.stringify({ kind: "re-review-partition", pass_id: "p_1", iter_num: 2 }),
      ),
      ctx,
    });
    expect(out).toHaveLength(1);
    const scope = JSON.parse(new TextDecoder().decode(out[0].scopeData));
    expect(scope.iter_num).toBe(2);
    expect(scope.role).toBe("re-review");
    expect(scope.assigned_finding_ids).toBeUndefined();
  });

  it("returns zero sub-scopes when affected set is missing", async () => {
    const ctx = await makeCtx();
    const out = await splitScope({
      parentClaimHandleId: "c",
      parentScope: new Uint8Array(),
      partitionRequest: new TextEncoder().encode(
        JSON.stringify({ kind: "fix-partition", pass_id: "p_1", iter_num: 1 }),
      ),
      ctx,
    });
    expect(out).toEqual([]);
  });

  it("throws on unknown kind", async () => {
    const ctx = await makeCtx();
    await expect(
      splitScope({
        parentClaimHandleId: "c",
        parentScope: new Uint8Array(),
        partitionRequest: new TextEncoder().encode(JSON.stringify({ kind: "nope", pass_id: "p" })),
        ctx,
      }),
    ).rejects.toThrow();
  });
});
