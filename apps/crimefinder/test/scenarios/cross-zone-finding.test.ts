import { describe, it, expect } from "vitest";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { pino } from "pino";
import { setupHarness } from "./harness.js";
import { handleOpen } from "@crimefinder/producer/dist/claim-producer/open.js";
import { handleAppendFinding } from "@crimefinder/producer/dist/state/append-finding.js";
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

describe("scenario: cross-zone finding", () => {
  it("attributes a finding to the zone containing the file with originating_zone_id set", async () => {
    const h = await setupHarness({ fixtureDir: path.resolve(here, "fixtures/multi-zone-repo") });
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
      const myZone = zones[0];
      const otherZone = zones[1];
      const otherFile = otherZone.files[0];

      const tok = tokens.issue({
        passId,
        claimHandleId: "sess",
        zoneId: myZone.id,
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
      await handleAppendFinding(
        {
          session_token: tok,
          class: "1",
          file: otherFile,
          line_start: 0,
          line_start_present: false,
          line_end: 0,
          line_end_present: false,
          description: "cross-zone issue",
          confidence: "high",
        },
        stateDeps,
      );
      const rows = await h.readFindings();
      const f = rows.find((r) => r.kind === "finding") as { zone_id: string; originating_zone_id: string | null; file: string } | undefined;
      expect(f).toBeTruthy();
      expect(f!.file).toBe(otherFile);
      expect(f!.zone_id).toBe(otherZone.id);
      expect(f!.originating_zone_id).toBe(myZone.id);
    } finally {
      await h.teardown();
    }
  });
});
