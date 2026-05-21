import { describe, it, expect } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { pino } from "pino";
import { setupHarness } from "./harness.js";
import { handleOpen } from "@crimefinder/producer/dist/claim-producer/open.js";
import { handleAppendFinding } from "@crimefinder/producer/dist/state/append-finding.js";
import { handleCommitFix } from "@crimefinder/producer/dist/state/commit-fix.js";
import { JsonlStore } from "@crimefinder/producer/dist/jsonl-store.js";
import { SessionTokenRegistry } from "@crimefinder/producer/dist/state/session-tokens.js";
import { IterationCounter } from "@crimefinder/producer/dist/state/iteration-counter.js";
import { TestCache } from "@crimefinder/producer/dist/state/test-cache.js";
import { TestRunMutex } from "@crimefinder/producer/dist/state/run-tests.js";
import { CommitMutex } from "@crimefinder/producer/dist/state/commit-mutex.js";
import { createGitOps } from "@crimefinder/producer/dist/git-ops.js";
import { ConfigSchema } from "@crimefinder/producer/dist/config.js";
import { createPartitionCache } from "@crimefinder/producer/dist/scopes/types.js";

const here = path.dirname(fileURLToPath(import.meta.url));
const logger = pino({ level: "silent" });

describe("scenario: fix-cycle iteration", () => {
  it("iter-guard advances iter_num across calls and iter-aggregate reports more_work_needed", async () => {
    const h = await setupHarness({ fixtureDir: path.resolve(here, "fixtures/tiny-repo") });
    try {
      const dir = h.repoRoot;
      const store = new JsonlStore({ repoRoot: dir, logger });
      const tokens = new SessionTokenRegistry();
      const iterCounter = new IterationCounter(store, logger);
      const partitionCache = createPartitionCache();
      const ctxBase = {
        repoRoot: dir,
        store,
        tokens,
        iterCounter,
        stateEndpointUrl: "127.0.0.1:0",
        partitionCache,
        config: ConfigSchema.parse({}),
        git: createGitOps(),
        logger,
      };

      const passOpen = await handleOpen(
        { selector: "@pass-state:new&mission=test&trigger=manual", claim_id: "c_pass" },
        { ...ctxBase, selector: "@pass-state:new&mission=test&trigger=manual", claimId: "c_pass" },
      );
      if (passOpen.type !== "acquired") throw new Error("pass open");
      const passId = JSON.parse(new TextDecoder().decode(passOpen.payload)).pass_id;
      await handleOpen(
        { selector: `@source-tree:pass_id=${passId}`, claim_id: "c_src" },
        { ...ctxBase, selector: `@source-tree:pass_id=${passId}`, claimId: "c_src" },
      );
      const zones = partitionCache.getZonePlan(passId)!;
      const zone = zones[0];

      // Stub: emit a class-1 finding on src/foo.ts (in zone).
      const reviewTok = tokens.issue({
        passId,
        claimHandleId: "sess_review",
        zoneId: zone.id,
        role: "review-zone",
        issuedAt: 0,
      });
      const stateDeps = {
        store,
        tokens,
        iterCounter,
        testCache: new TestCache(),
        testRunMutex: new TestRunMutex(),
        commitMutex: new CommitMutex(),
        git: createGitOps(),
        config: ctxBase.config,
        partitionCache,
        repoRoot: dir,
        logger,
      };
      const f = await handleAppendFinding(
        {
          session_token: reviewTok,
          class: "1",
          file: "src/foo.ts",
          line_start: 0,
          line_start_present: false,
          line_end: 0,
          line_end_present: false,
          description: "missing edge case",
          confidence: "high",
        },
        stateDeps,
      );

      // iter-guard: first call returns iter_num=1, affected_zones=[zone].
      const guard1 = await handleOpen(
        { selector: `@unresolved-class-1-4:pass_id=${passId}`, claim_id: "c_guard1" },
        { ...ctxBase, selector: `@unresolved-class-1-4:pass_id=${passId}`, claimId: "c_guard1" },
      );
      if (guard1.type !== "acquired") throw new Error("guard1");
      const guard1Payload = JSON.parse(new TextDecoder().decode(guard1.payload));
      expect(guard1Payload.iter_num).toBe(1);
      expect(guard1Payload.skipped).toBe(false);

      // Apply a fix: modify src/foo.ts, commit-fix.
      const fixTok = tokens.issue({
        passId,
        claimHandleId: "sess_fix",
        zoneId: zone.id,
        role: "fix-cycle",
        issuedAt: 0,
      });
      await fs.writeFile(path.join(dir, "src/foo.ts"), "export function foo(): string { return 'fixed'; }\n");
      const commit = await handleCommitFix(
        {
          session_token: fixTok,
          finding_id: f.finding_id,
          fix_description: "x",
          commit_message: "fix: edge case",
        },
        stateDeps,
      );
      expect(commit.finding_status).toBe("fixed");

      // iter-guard again: iter_num=2, skipped:true (no more class-1-4 work).
      const guard2 = await handleOpen(
        { selector: `@unresolved-class-1-4:pass_id=${passId}`, claim_id: "c_guard2" },
        { ...ctxBase, selector: `@unresolved-class-1-4:pass_id=${passId}`, claimId: "c_guard2" },
      );
      if (guard2.type !== "acquired") throw new Error("guard2");
      const guard2Payload = JSON.parse(new TextDecoder().decode(guard2.payload));
      expect(guard2Payload.iter_num).toBe(2);
      expect(guard2Payload.skipped).toBe(true);

      // iter-aggregate at iter=1: more_work_needed:false.
      const iterAgg = await handleOpen(
        { selector: `@iter-aggregate:pass_id=${passId}&iter_num=1`, claim_id: "c_iaa" },
        { ...ctxBase, selector: `@iter-aggregate:pass_id=${passId}&iter_num=1`, claimId: "c_iaa" },
      );
      if (iterAgg.type !== "acquired") throw new Error("iter-aggregate");
      const aggPayload = JSON.parse(new TextDecoder().decode(iterAgg.payload));
      expect(aggPayload.more_work_needed).toBe(false);

      const rows = await h.readFindings();
      expect(rows.some((r) => r.kind === "status_update" && r.status === "fixed")).toBe(true);
    } finally {
      await h.teardown();
    }
  });
});
