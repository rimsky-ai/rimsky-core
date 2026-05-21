import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { execFile as execFileCb } from "node:child_process";
import pino from "pino";
import { generateRowId } from "@crimefinder/shared";
import { JsonlStore } from "../jsonl-store.js";
import { IterationCounter } from "../state/iteration-counter.js";
import { createGitOps } from "../git-ops.js";
import { createPartitionCache } from "../scopes/types.js";
import { runStartupRecovery } from "./startup-scan.js";

const execFile = promisify(execFileCb);
const logger = pino({ level: "silent" });
const NOW = "2026-05-19T12:00:00.000+00:00";

async function initRepo(dir: string): Promise<void> {
  await execFile("git", ["init", "-q", "-b", "main"], { cwd: dir });
  await execFile("git", ["config", "user.email", "x@y"], { cwd: dir });
  await execFile("git", ["config", "user.name", "t"], { cwd: dir });
  await execFile("git", ["config", "commit.gpgsign", "false"], { cwd: dir });
  await fs.writeFile(path.join(dir, ".gitignore"), ".crimefinder/\n");
  await fs.writeFile(path.join(dir, "src.ts"), "x");
  await execFile("git", ["add", "."], { cwd: dir });
  await execFile("git", ["commit", "-qm", "init"], { cwd: dir });
}

describe("runStartupRecovery", () => {
  let dir: string;
  let store: JsonlStore;
  let git: ReturnType<typeof createGitOps>;
  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-recovery-"));
    await initRepo(dir);
    store = new JsonlStore({ repoRoot: dir, logger });
    git = createGitOps();
  });

  it("reconstructs a missing status:fixed row from a Resolves: footer", async () => {
    // Add a finding row but skip the status update.
    await store.appendFinding({
      kind: "finding",
      id: "f_xyz",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z",
      session_id: "s",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "src.ts",
      line_start: null,
      line_end: null,
      description: "x",
      fingerprint: "sha256:xyz",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await fs.writeFile(path.join(dir, "src.ts"), "fixed");
    await git.add(dir, ["src.ts"]);
    await git.commit(dir, "fix: stuff\n\nResolves: f_xyz");
    const iterCounter = new IterationCounter(store, logger);
    const r = await runStartupRecovery({ store, git, iterCounter, repoRoot: dir, logger });
    expect(r.reconstructedRowsAppended).toBe(1);
    const rows = await store.readFindings();
    expect(
      rows.some(
        (row) => row.kind === "status_update" && row.ref === "f_xyz" && row.status === "fixed",
      ),
    ).toBe(true);
  });

  it("is a no-op when status:fixed already exists for the commit", async () => {
    await store.appendFinding({
      kind: "finding",
      id: "f_abc",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z",
      session_id: "s",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "src.ts",
      line_start: null,
      line_end: null,
      description: "x",
      fingerprint: "sha256:abc",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await fs.writeFile(path.join(dir, "src.ts"), "fixed");
    await git.add(dir, ["src.ts"]);
    const sha = await git.commit(dir, "fix\n\nResolves: f_abc");
    await store.appendFinding({
      kind: "status_update",
      id: "su_1",
      ts: NOW,
      ref: "f_abc",
      status: "fixed",
      by_pass: "p_1",
      by_session: "s",
      resolved_at_commit: sha,
    });
    const r = await runStartupRecovery({
      store,
      git,
      iterCounter: new IterationCounter(store, logger),
      repoRoot: dir,
      logger,
    });
    expect(r.reconstructedRowsAppended).toBe(0);
  });

  it("skips commits without a Resolves: footer", async () => {
    await fs.writeFile(path.join(dir, "src.ts"), "y");
    await git.add(dir, ["src.ts"]);
    await git.commit(dir, "chore: tidy");
    const r = await runStartupRecovery({
      store,
      git,
      iterCounter: new IterationCounter(store, logger),
      repoRoot: dir,
      logger,
    });
    expect(r.reconstructedRowsAppended).toBe(0);
  });

  it("rehydrates the latest zone_plan per pass into the partitionCache", async () => {
    // Two zone_plan rows for the same pass simulate a partition computed
    // pre-crash followed by a successor write at restart — last-wins per
    // openSourceTree's "write only when cache is empty" rule.
    await store.appendZonePlan({
      kind: "zone_plan",
      id: generateRowId(),
      ts: "2026-05-19T11:00:00.000+00:00",
      pass_id: "p_1",
      zones: [
        { id: "z_old", label: "src/old", files: ["src/old/a.ts"] },
      ],
    });
    await store.appendZonePlan({
      kind: "zone_plan",
      id: generateRowId(),
      ts: "2026-05-19T12:00:00.000+00:00",
      pass_id: "p_1",
      zones: [
        { id: "z_a", label: "src/a", files: ["src/a/x.ts", "src/a/y.ts"] },
        { id: "z_b", label: "src/b", files: ["src/b/z.ts"] },
      ],
    });
    // A second pass to confirm per-pass scoping.
    await store.appendZonePlan({
      kind: "zone_plan",
      id: generateRowId(),
      ts: "2026-05-19T12:30:00.000+00:00",
      pass_id: "p_2",
      zones: [{ id: "z_c", label: "src/c", files: ["src/c/q.ts"] }],
    });
    const partitionCache = createPartitionCache();
    const r = await runStartupRecovery({
      store,
      git,
      iterCounter: new IterationCounter(store, logger),
      partitionCache,
      repoRoot: dir,
      logger,
    });
    expect(r.zonePlansRestored).toBe(2);
    const p1 = partitionCache.getZonePlan("p_1");
    expect(p1).toBeDefined();
    expect(p1!.map((z) => z.id).sort()).toEqual(["z_a", "z_b"]);
    const p2 = partitionCache.getZonePlan("p_2");
    expect(p2).toBeDefined();
    expect(p2![0].id).toBe("z_c");
  });

  it("uses monotonic seq (not ts) for last-wins ordering on zone_plan rows", async () => {
    // Two rows with the SAME ts but different seq; the higher-seq row
    // must win, regardless of map-iteration order.
    const SAME_TS = "2026-05-19T12:00:00.000+00:00";
    await store.appendZonePlan({
      kind: "zone_plan",
      id: generateRowId(),
      ts: SAME_TS,
      pass_id: "p_seq",
      seq: 0,
      zones: [{ id: "z_old", label: "old", files: ["old.ts"] }],
    });
    await store.appendZonePlan({
      kind: "zone_plan",
      id: generateRowId(),
      ts: SAME_TS,
      pass_id: "p_seq",
      seq: 1,
      zones: [{ id: "z_new", label: "new", files: ["new.ts"] }],
    });
    const partitionCache = createPartitionCache();
    await runStartupRecovery({
      store,
      git,
      iterCounter: new IterationCounter(store, logger),
      partitionCache,
      repoRoot: dir,
      logger,
    });
    const restored = partitionCache.getZonePlan("p_seq");
    expect(restored).toBeDefined();
    expect(restored![0].id).toBe("z_new");
  });

  it("breaks ties by row position when two legacy zone_plan rows share ts and lack seq", async () => {
    // Both rows omit `seq` (legacy shape) and share an identical ts. The
    // later-appended row must win via the position-in-file tiebreaker;
    // otherwise the first-inserted map entry would win by accident.
    //
    // We bypass `JsonlStore.appendZonePlan` here because that path always
    // auto-assigns a `seq` when none is provided — which would resolve the
    // winner via the seq branch of `cmp` and skip the position tiebreaker
    // entirely. Write raw JSONL directly to exercise the legacy shape.
    const SAME_TS = "2026-05-19T12:00:00.000+00:00";
    await store.ensureDir();
    const passesFile = path.join(dir, ".crimefinder", "passes.jsonl");
    const oldRow = {
      kind: "zone_plan",
      id: generateRowId(),
      ts: SAME_TS,
      pass_id: "p_legacy",
      zones: [{ id: "z_old", label: "old", files: ["old.ts"] }],
    };
    const newRow = {
      kind: "zone_plan",
      id: generateRowId(),
      ts: SAME_TS,
      pass_id: "p_legacy",
      zones: [{ id: "z_new", label: "new", files: ["new.ts"] }],
    };
    await fs.appendFile(
      passesFile,
      JSON.stringify(oldRow) + "\n" + JSON.stringify(newRow) + "\n",
      "utf-8",
    );
    const partitionCache = createPartitionCache();
    await runStartupRecovery({
      store,
      git,
      iterCounter: new IterationCounter(store, logger),
      partitionCache,
      repoRoot: dir,
      logger,
    });
    const restored = partitionCache.getZonePlan("p_legacy");
    expect(restored).toBeDefined();
    expect(restored![0].id).toBe("z_new");
  });

  it("breaks ties by row position when two legacy dedup_batches rows share ts and lack seq", async () => {
    // Mirror of the zone_plan position-tiebreaker test for dedup_batches.
    // Same rationale: routing through `JsonlStore.appendDedupBatches` would
    // auto-assign a `seq` and bypass the position branch of `cmp`.
    const SAME_TS = "2026-05-19T12:00:00.000+00:00";
    await store.ensureDir();
    const passesFile = path.join(dir, ".crimefinder", "passes.jsonl");
    const oldRow = {
      kind: "dedup_batches",
      id: generateRowId(),
      ts: SAME_TS,
      pass_id: "p_legacy",
      batches: [[{ file: "old.ts", finding_ids: ["f_old"] }]],
    };
    const newRow = {
      kind: "dedup_batches",
      id: generateRowId(),
      ts: SAME_TS,
      pass_id: "p_legacy",
      batches: [[{ file: "new.ts", finding_ids: ["f_new"] }]],
    };
    await fs.appendFile(
      passesFile,
      JSON.stringify(oldRow) + "\n" + JSON.stringify(newRow) + "\n",
      "utf-8",
    );
    const partitionCache = createPartitionCache();
    await runStartupRecovery({
      store,
      git,
      iterCounter: new IterationCounter(store, logger),
      partitionCache,
      repoRoot: dir,
      logger,
    });
    const batches = partitionCache.getDedupBatches("p_legacy");
    expect(batches).toBeDefined();
    expect(batches![0][0].file).toBe("new.ts");
  });

  it("uses monotonic seq (not ts) for last-wins ordering on dedup_batches rows", async () => {
    const SAME_TS = "2026-05-19T12:00:00.000+00:00";
    await store.appendDedupBatches({
      kind: "dedup_batches",
      id: generateRowId(),
      ts: SAME_TS,
      pass_id: "p_seq",
      seq: 0,
      batches: [[{ file: "old.ts", finding_ids: ["f_old"] }]],
    });
    await store.appendDedupBatches({
      kind: "dedup_batches",
      id: generateRowId(),
      ts: SAME_TS,
      pass_id: "p_seq",
      seq: 1,
      batches: [[{ file: "new.ts", finding_ids: ["f_new"] }]],
    });
    const partitionCache = createPartitionCache();
    await runStartupRecovery({
      store,
      git,
      iterCounter: new IterationCounter(store, logger),
      partitionCache,
      repoRoot: dir,
      logger,
    });
    const batches = partitionCache.getDedupBatches("p_seq");
    expect(batches).toBeDefined();
    expect(batches![0][0].file).toBe("new.ts");
  });

  it("rehydrates dedup_batches into the partitionCache (last-wins per pass)", async () => {
    await store.appendDedupBatches({
      kind: "dedup_batches",
      id: generateRowId(),
      ts: "2026-05-19T11:00:00.000+00:00",
      pass_id: "p_1",
      batches: [[{ file: "src/a.ts", finding_ids: ["f_old"] }]],
    });
    await store.appendDedupBatches({
      kind: "dedup_batches",
      id: generateRowId(),
      ts: "2026-05-19T12:00:00.000+00:00",
      pass_id: "p_1",
      batches: [
        [
          { file: "src/x.ts", finding_ids: ["f_1", "f_2"] },
          { file: "src/y.ts", finding_ids: ["f_3"] },
        ],
        [{ file: "src/z.ts", finding_ids: ["f_4", "f_5"] }],
      ],
    });
    const partitionCache = createPartitionCache();
    const r = await runStartupRecovery({
      store,
      git,
      iterCounter: new IterationCounter(store, logger),
      partitionCache,
      repoRoot: dir,
      logger,
    });
    expect(r.dedupBatchesRestored).toBe(1);
    const batches = partitionCache.getDedupBatches("p_1");
    expect(batches).toBeDefined();
    expect(batches!.length).toBe(2);
    expect(batches![0]).toEqual([
      { file: "src/x.ts", findingIds: ["f_1", "f_2"] },
      { file: "src/y.ts", findingIds: ["f_3"] },
    ]);
    expect(batches![1]).toEqual([
      { file: "src/z.ts", findingIds: ["f_4", "f_5"] },
    ]);
  });
});
