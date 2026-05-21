import { describe, it, expect } from "vitest";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { pino } from "pino";
import { setupHarness } from "./harness.js";
import { handleOpen } from "@crimefinder/producer/dist/claim-producer/open.js";
import { handleAppendCoverage } from "@crimefinder/producer/dist/state/append-coverage.js";
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

// In-process full-pass smoke test. Drives the producer's gate surface
// directly (no claude CLI subprocess; the supervisor stack is not part
// of this scenario). Verifies that JSONL persistence + gate semantics
// give the expected end-state when a pass walks a tiny zone with zero
// findings.
describe("scenario: full pass with stub agent", () => {
  it("walks a tiny repo with no findings and writes coverage + pass rows", async () => {
    const h = await setupHarness({ fixtureDir: path.resolve(here, "fixtures/tiny-repo") });
    try {
      // We use the harness's local-producer setup but drive scope handlers
      // directly. The "Open"s ensure pass_started, source-tree partition,
      // and report flows write the expected JSONL.
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

      // open-pass → pass_started
      const passOpen = await handleOpen(
        { selector: "@pass-state:new&mission=test&trigger=manual", claim_id: "c_pass" },
        { ...ctxBase, selector: "@pass-state:new&mission=test&trigger=manual", claimId: "c_pass" },
      );
      if (passOpen.type !== "acquired") throw new Error("pass open failed");
      const passPayload = JSON.parse(new TextDecoder().decode(passOpen.payload));
      const passId = passPayload.pass_id;

      // source-tree open populates the zone plan cache
      await handleOpen(
        { selector: `@source-tree:pass_id=${passId}`, claim_id: "c_src" },
        { ...ctxBase, selector: `@source-tree:pass_id=${passId}`, claimId: "c_src" },
      );

      // Stub a review-zone session: emit coverage rows for the fixture files.
      const zonePlan = partitionCache.getZonePlan(passId)!;
      const zone = zonePlan[0];
      const sessionToken = tokens.issue({
        passId,
        claimHandleId: "sess_1",
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
      await handleAppendCoverage(
        { session_token: sessionToken, files_read: zone.files },
        stateDeps,
      );

      // report scope writes pass_finished
      const reportOpen = await handleOpen(
        { selector: `@report:pass_id=${passId}`, claim_id: "c_rpt" },
        { ...ctxBase, selector: `@report:pass_id=${passId}`, claimId: "c_rpt" },
      );
      if (reportOpen.type !== "acquired") throw new Error("report open failed");

      // Assert pass_started + pass_finished rows
      const passes = await h.readPasses();
      const started = passes.filter((p) => p.kind === "pass_started");
      const finished = passes.filter((p) => p.kind === "pass_finished");
      expect(started).toHaveLength(1);
      expect(finished).toHaveLength(1);
      expect((await h.readFindings()).filter((r) => r.kind === "finding")).toHaveLength(0);
      const coverage = await h.readCoverage();
      expect(coverage.length).toBeGreaterThan(0);
    } finally {
      await h.teardown();
    }
  });
});

