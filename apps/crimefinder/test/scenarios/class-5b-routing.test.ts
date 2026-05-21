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

describe("scenario: class-5b auto-routing", () => {
  async function setup() {
    const h = await setupHarness({
      fixtureDir: path.resolve(here, "fixtures/tiny-repo"),
      config:
        "design_docs:\n  concepts_dir: .ok-planner/design/concepts\n  tensions_dir: .ok-planner/design/tensions\n  annotation_marker: '@concept:'\n",
    });
    const store = new JsonlStore({ repoRoot: h.repoRoot, logger });
    const tokens = new SessionTokenRegistry();
    const iterCounter = new IterationCounter(store, logger);
    const partitionCache = createPartitionCache();
    const ctxBase = {
      repoRoot: h.repoRoot,
      store,
      tokens,
      iterCounter,
      stateEndpointUrl: "127.0.0.1:0",
      partitionCache,
      config: ConfigSchema.parse({
        design_docs: {
          concepts_dir: ".ok-planner/design/concepts",
          tensions_dir: ".ok-planner/design/tensions",
          annotation_marker: "@concept:",
        },
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
    const zone = partitionCache.getZonePlan(passId)![0];
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
      repoRoot: h.repoRoot,
      logger,
    };
    return { h, stateDeps, tok };
  }

  it("auto-routes a class-1 to class-5b when boundaries text is NOT quoted", async () => {
    const { h, stateDeps, tok } = await setup();
    try {
      const r = await handleAppendFinding(
        {
          session_token: tok,
          class: "1",
          file: "src/foo.ts",
          line_start: 0,
          line_start_present: false,
          line_end: 0,
          line_end_present: false,
          description: "the example concept is wrong here in some general way",
          concept_slug: "example-concept",
          confidence: "high",
        },
        stateDeps,
      );
      expect(r.effective_class).toBe("5b");
      expect(r.auto_rerouted).toBe(true);
    } finally {
      await h.teardown();
    }
  });

  it("keeps class-1 when description quotes ≥ 8 contiguous boundary tokens", async () => {
    const { h, stateDeps, tok } = await setup();
    try {
      const r = await handleAppendFinding(
        {
          session_token: tok,
          class: "1",
          file: "src/foo.ts",
          line_start: 0,
          line_start_present: false,
          line_end: 0,
          line_end_present: false,
          description:
            "the example concept does not perform any side effecting filesystem operations during commit transactions",
          concept_slug: "example-concept",
          confidence: "high",
        },
        stateDeps,
      );
      expect(r.effective_class).toBe("1");
      expect(r.auto_rerouted).toBe(false);
    } finally {
      await h.teardown();
    }
  });
});
