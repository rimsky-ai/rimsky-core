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

describe("scenario: multi-zone concurrency", () => {
  it("serializes JSONL appends from concurrent zone sessions", async () => {
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
      if (passOpen.type !== "acquired") throw new Error("pass open failed");
      const passPayload = JSON.parse(new TextDecoder().decode(passOpen.payload));
      const passId = passPayload.pass_id;
      await handleOpen(
        { selector: `@source-tree:pass_id=${passId}`, claim_id: "c_src" },
        { ...ctxBase, selector: `@source-tree:pass_id=${passId}`, claimId: "c_src" },
      );
      const zones = partitionCache.getZonePlan(passId)!;
      expect(zones.length).toBeGreaterThanOrEqual(3);

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

      // For each zone, emit 5 findings concurrently. Across N zones we
      // exercise the per-file JSONL mutex under contention.
      const ops: Promise<unknown>[] = [];
      let expectedCount = 0;
      for (const zone of zones.slice(0, 3)) {
        const tok = tokens.issue({
          passId,
          claimHandleId: `sess_${zone.id}`,
          zoneId: zone.id,
          role: "review-zone",
          issuedAt: 0,
        });
        for (let i = 0; i < 5; i++) {
          expectedCount += 1;
          ops.push(
            handleAppendFinding(
              {
                session_token: tok,
                class: "1",
                file: zone.files[0],
                line_start: 0,
                line_start_present: false,
                line_end: 0,
                line_end_present: false,
                description: `${zone.id}-bug-${i}`,
                confidence: "high",
              },
              stateDeps,
            ),
          );
        }
      }
      await Promise.all(ops);
      const rows = await h.readFindings();
      const findings = rows.filter((r) => r.kind === "finding");
      // Each (zone, i) pair generates a unique description, so the
      // fingerprints don't collide and no rediscovery-dedup should fire.
      // We expect exactly `expectedCount` rows — a smaller count would
      // mean either a JSONL mutex bug dropped writes or a dedup
      // misfire silently merged distinct findings.
      expect(findings.length).toBe(expectedCount);
      // Each finding must be a clean JSONL row (schema-validated by reader).
      for (const f of findings) {
        expect(f.fingerprint).toMatch(/^sha256:/);
      }
    } finally {
      await h.teardown();
    }
  });
});
