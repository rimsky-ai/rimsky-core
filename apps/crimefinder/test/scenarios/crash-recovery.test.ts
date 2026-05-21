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
import { runStartupRecovery } from "@crimefinder/producer/dist/recovery/startup-scan.js";

const here = path.dirname(fileURLToPath(import.meta.url));
const logger = pino({ level: "silent" });

describe("scenario: crash recovery", () => {
  it("reconstructs status:fixed from a Resolves: footer when JSONL is missing the row", async () => {
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

      // Set up pass, source-tree, finding, commit-fix as in T51.
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

      const reviewTok = tokens.issue({
        passId,
        claimHandleId: "sess_r",
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
          description: "crash-test bug",
          confidence: "high",
        },
        stateDeps,
      );

      const fixTok = tokens.issue({
        passId,
        claimHandleId: "sess_f",
        zoneId: zone.id,
        role: "fix-cycle",
        issuedAt: 0,
      });
      await fs.writeFile(path.join(dir, "src/foo.ts"), "// fixed\n");
      const fixed = await handleCommitFix(
        {
          session_token: fixTok,
          finding_id: f.finding_id,
          fix_description: "x",
          commit_message: "fix: crash-test",
        },
        stateDeps,
      );

      // Simulate crash mid-transaction: strip the status_update line.
      const findingsPath = path.join(dir, ".crimefinder", "findings.jsonl");
      const raw = await fs.readFile(findingsPath, "utf-8");
      const lines = raw.split("\n").filter(Boolean);
      const filtered = lines.filter((l) => {
        try {
          const o = JSON.parse(l);
          return !(o.kind === "status_update" && o.ref === f.finding_id);
        } catch {
          return true;
        }
      });
      await fs.writeFile(findingsPath, filtered.join("\n") + "\n");

      // Confirm the row was actually stripped.
      const beforeRows = await h.readFindings();
      expect(beforeRows.some((r) => r.kind === "status_update" && r.ref === f.finding_id)).toBe(false);

      // Run recovery: walks git log, sees Resolves: footer, re-appends.
      const r = await runStartupRecovery({
        store,
        git: createGitOps(),
        iterCounter,
        repoRoot: dir,
        logger,
      });
      expect(r.reconstructedRowsAppended).toBe(1);
      const afterRows = await h.readFindings();
      const reconstructed = afterRows.find(
        (row) => row.kind === "status_update" && row.ref === f.finding_id,
      );
      expect(reconstructed).toBeTruthy();
      expect(reconstructed?.kind === "status_update" && reconstructed.resolved_at_commit).toBe(fixed.commit_sha);
    } finally {
      await h.teardown();
    }
  });
});
