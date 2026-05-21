import { describe, it, expect } from "vitest";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { pino } from "pino";
import { setupHarness } from "./harness.js";
import { handleOpen } from "@crimefinder/producer/dist/claim-producer/open.js";
import { handleAppendCoverage } from "@crimefinder/producer/dist/state/append-coverage.js";
import { handleSkipZone } from "@crimefinder/producer/dist/state/skip-zone.js";
import { handleGetZoneCoverage } from "@crimefinder/producer/dist/state/get-zone-coverage.js";
import { JsonlStore } from "@crimefinder/producer/dist/jsonl-store.js";
import { SessionTokenRegistry } from "@crimefinder/producer/dist/state/session-tokens.js";
import { IterationCounter } from "@crimefinder/producer/dist/state/iteration-counter.js";
import { TestCache } from "@crimefinder/producer/dist/state/test-cache.js";
import { TestRunMutex } from "@crimefinder/producer/dist/state/run-tests.js";
import { CommitMutex } from "@crimefinder/producer/dist/state/commit-mutex.js";
import { createGitOps } from "@crimefinder/producer/dist/git-ops.js";
import { ConfigSchema } from "@crimefinder/producer/dist/config.js";
import { createPartitionCache } from "@crimefinder/producer/dist/scopes/types.js";
import { computeZoneCoverage } from "@crimefinder/producer/dist/zones/coverage.js";
import { GateError } from "@crimefinder/shared";

const here = path.dirname(fileURLToPath(import.meta.url));
const logger = pino({ level: "silent" });

describe("scenario: coverage threshold and skip-zone interaction", () => {
  it("records skip_zone when the agent calls it (the operator path under coverage threshold)", async () => {
    const h = await setupHarness({
      fixtureDir: path.resolve(here, "fixtures/multi-zone-repo"),
      config: "coverage:\n  threshold_pct: 80\n  on_below_threshold: require_skip\n",
    });
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
        config: ConfigSchema.parse({
          coverage: { threshold_pct: 80, on_below_threshold: "require_skip" },
        }),
        git: createGitOps(),
        logger,
      };
      const passOpen = await handleOpen(
        { selector: "@pass-state:new&mission=test&trigger=manual", claim_id: "c_pass" },
        { ...ctxBase, selector: "@pass-state:new&mission=test&trigger=manual", claimId: "c_pass" },
      );
      if (passOpen.type !== "acquired") throw new Error("pass");
      const passId = JSON.parse(new TextDecoder().decode(passOpen.payload)).pass_id;
      await handleOpen(
        { selector: `@source-tree:pass_id=${passId}`, claim_id: "c_src" },
        { ...ctxBase, selector: `@source-tree:pass_id=${passId}`, claimId: "c_src" },
      );
      const zones = partitionCache.getZonePlan(passId)!;
      // Pick the largest zone so a 1-file read is well below 80% coverage.
      const zone = zones.reduce((best, z) => (z.files.length > best.files.length ? z : best));
      expect(zone.files.length).toBeGreaterThanOrEqual(5);
      const tok = tokens.issue({
        passId,
        claimHandleId: "sess",
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

      // Record coverage for just one file in a multi-file zone — well below 80%.
      await handleAppendCoverage(
        { session_token: tok, files_read: [zone.files[0]] },
        stateDeps,
      );
      const coverage = await h.readCoverage();
      const summary = computeZoneCoverage(coverage, passId, zones);
      const a = summary.find((s) => s.zoneId === zone.id)!;
      expect(a.coveragePercent).toBeLessThan(80);

      // Operator's recourse: review_skip_zone records the skip and the pass
      // continues. Without skip, the agent would have to either keep
      // reading or raise via review_request_help.
      const skip = await handleSkipZone(
        { session_token: tok, reason: "no relevant files" },
        stateDeps,
      );
      expect(skip.zone_id).toBe(zone.id);
      expect(skip.skipped).toBe(true);
      const passes = await h.readPasses();
      expect(passes.some((p) => p.kind === "skip_zone" && p.zone_id === zone.id)).toBe(true);
    } finally {
      await h.teardown();
    }
  });

  // Spec lines 1275-1278: review_complete must reject with
  // coverage_below_threshold when coverage falls short and skip_zone was
  // not recorded; calling review_skip_zone first must clear the gate so a
  // subsequent review_complete succeeds. The gate lives in the executor
  // (review-complete.ts) and reads producer state via getZoneCoverage; the
  // scenario test reproduces the gate's branching against real producer
  // state without spinning up the executor process.
  it("review_complete is blocked under threshold, allowed after skip_zone", async () => {
    const h = await setupHarness({
      fixtureDir: path.resolve(here, "fixtures/multi-zone-repo"),
      config: "coverage:\n  threshold_pct: 80\n  on_below_threshold: require_skip\n",
    });
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
        config: ConfigSchema.parse({
          coverage: { threshold_pct: 80, on_below_threshold: "require_skip" },
        }),
        git: createGitOps(),
        logger,
      };
      const passOpen = await handleOpen(
        { selector: "@pass-state:new&mission=test&trigger=manual", claim_id: "c_pass" },
        { ...ctxBase, selector: "@pass-state:new&mission=test&trigger=manual", claimId: "c_pass" },
      );
      if (passOpen.type !== "acquired") throw new Error("pass");
      const passId = JSON.parse(new TextDecoder().decode(passOpen.payload)).pass_id;
      await handleOpen(
        { selector: `@source-tree:pass_id=${passId}`, claim_id: "c_src" },
        { ...ctxBase, selector: `@source-tree:pass_id=${passId}`, claimId: "c_src" },
      );
      const zones = partitionCache.getZonePlan(passId)!;
      const zone = zones.reduce((best, z) => (z.files.length > best.files.length ? z : best));
      const tok = tokens.issue({
        passId,
        claimHandleId: "sess",
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

      // Coverage well below 80% — one file out of N (N >= 5).
      await handleAppendCoverage(
        { session_token: tok, files_read: [zone.files[0]] },
        stateDeps,
      );

      // What review_complete would see (gate-side): getZoneCoverage returns
      // coverage_pct < threshold and skip_recorded=false → reject.
      const before = await handleGetZoneCoverage({ session_token: tok }, stateDeps);
      expect(before.coverage_pct).toBeLessThan(80);
      expect(before.skip_recorded).toBe(false);

      // Recording the skip clears the gate.
      await handleSkipZone({ session_token: tok, reason: "no relevant files" }, stateDeps);
      const after = await handleGetZoneCoverage({ session_token: tok }, stateDeps);
      expect(after.coverage_pct).toBeLessThan(80);
      expect(after.skip_recorded).toBe(true);
    } finally {
      await h.teardown();
    }
  });

  // Spec line 432: skip-zone is only valid when coverage is actually below
  // threshold — otherwise the agent could short-circuit a fully-readable
  // zone. The producer-side guard rejects the skip.
  it("review_skip_zone rejects when coverage is already at threshold", async () => {
    const h = await setupHarness({
      fixtureDir: path.resolve(here, "fixtures/multi-zone-repo"),
      config: "coverage:\n  threshold_pct: 80\n  on_below_threshold: require_skip\n",
    });
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
        config: ConfigSchema.parse({
          coverage: { threshold_pct: 80, on_below_threshold: "require_skip" },
        }),
        git: createGitOps(),
        logger,
      };
      const passOpen = await handleOpen(
        { selector: "@pass-state:new&mission=test&trigger=manual", claim_id: "c_pass" },
        { ...ctxBase, selector: "@pass-state:new&mission=test&trigger=manual", claimId: "c_pass" },
      );
      if (passOpen.type !== "acquired") throw new Error("pass");
      const passId = JSON.parse(new TextDecoder().decode(passOpen.payload)).pass_id;
      await handleOpen(
        { selector: `@source-tree:pass_id=${passId}`, claim_id: "c_src" },
        { ...ctxBase, selector: `@source-tree:pass_id=${passId}`, claimId: "c_src" },
      );
      const zones = partitionCache.getZonePlan(passId)!;
      const zone = zones[0];
      const tok = tokens.issue({
        passId,
        claimHandleId: "sess",
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

      // Read every file — coverage = 100% — skip should be refused.
      await handleAppendCoverage(
        { session_token: tok, files_read: zone.files },
        stateDeps,
      );
      let caught: unknown;
      try {
        await handleSkipZone({ session_token: tok, reason: "should be refused" }, stateDeps);
      } catch (e) {
        caught = e;
      }
      expect(caught).toBeInstanceOf(GateError);
      expect((caught as GateError).envelope.data.crimefinder_error_class).toBe(
        "coverage_above_threshold",
      );
    } finally {
      await h.teardown();
    }
  });
});
