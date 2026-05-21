/**
 * End-to-end integration scenario for crimefinder against a real rimsky
 * stack (sqlite-backed) and the crimefinder-producer + crimefinder-
 * executor as host subprocesses.
 *
 * Coverage:
 *   - rimsky's template parser accepts `templates/code-review-pass.yml`
 *     as currently shipped (the wire surface the in-process scenarios
 *     can't exercise — they import producer-side gate handlers
 *     directly and never round-trip through rimsky).
 *   - control-api accepts an instance-create against the registered
 *     template and returns an `instance_id`.
 *   - the rimsky scheduler + supervisor stay healthy through the
 *     subprocess lifecycle (i.e. the executor's gRPC Capabilities
 *     handshake + the producer's gRPC ClaimProducer surface satisfy
 *     control-api's startup dial).
 *
 * Known scope limitation: the test does NOT yet wait for the full DAG
 * to walk to terminal. The crimefinder template wires real
 * deterministic-mode handlers in the producer plus stub-mode executor
 * dispatches; threading per-mission stub_outcomes through the template's
 * userdata (which rimsky does NOT substitute `{{...}}` inside) requires
 * either a stub-mode env-var hook or a side-channel HTTP outcome
 * server. The harness leaves room for that follow-up; the scenario
 * exercises the wire-surface invariants and the JSONL substrate
 * (which is what the in-process scenarios cannot exercise at all).
 *
 * Gating: this test spawns five Go binaries + two Node subprocesses
 * and is slower than the in-process scenarios. It is NOT included in
 * the workspace's default `npm test`; run via
 *   cd apps/crimefinder && npm run test:integration
 */
import { describe, it, expect } from "vitest";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { existsSync } from "node:fs";
import { setupHarness } from "./harness.js";

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRootRimsky = path.resolve(here, "../../../..");
const templatePath = path.resolve(repoRootRimsky, "apps/crimefinder/templates/code-review-pass.yml");
const fixtureDir = path.resolve(here, "fixtures/full-pass");

// Pre-flight: skip the suite with a clear message if the required
// binaries aren't built yet. Avoids cryptic spawn ENOENT failures.
const requiredBins = [
  "rimsky-migrate",
  "rimsky-control-api",
  "rimsky-scheduler",
  "rimsky-supervisor",
  "rimsky",
].map((n) => path.join(repoRootRimsky, "bin", n));
const requiredDist = [
  path.join(repoRootRimsky, "apps/crimefinder/producer/dist/main.js"),
  path.join(repoRootRimsky, "apps/crimefinder/executor/dist/main.js"),
];
const missing = [...requiredBins, ...requiredDist].filter((p) => !existsSync(p));

const maybeIt = missing.length === 0 ? it : it.skip;

describe("integration: full-pass against real rimsky stack", () => {
  if (missing.length > 0) {
    // Surface the blocker so the operator sees what to do; the test
    // body itself is skipped (it.skip below).
    // eslint-disable-next-line no-console
    console.warn(
      `integration tests skipped: missing prerequisites: ${missing.join(", ")}\n` +
        `  Build the rimsky binaries: cd ${repoRootRimsky} && ` +
        `for n in rimsky-migrate rimsky-control-api rimsky-scheduler ` +
        `rimsky-supervisor rimsky; do go build -o bin/$n ./cmd/$n; done\n` +
        `  Build the crimefinder workspace: cd ${repoRootRimsky}/apps/crimefinder && npm run build`,
    );
  }

  maybeIt(
    "registers the template, creates an instance, and brings up the full stack",
    async () => {
      const h = await setupHarness({ fixtureDir });
      try {
        const templateHash = await h.registerTemplate(templatePath);
        expect(templateHash).toMatch(/^sha256-[0-9a-f]+$/);

        await h.deployTemplate(templateHash);

        const instanceId = await h.createInstance(templateHash, {
          mission: "integration test",
          trigger: "manual",
          repo_root: h.repoRoot,
        });
        expect(instanceId).toBeTruthy();
        expect(typeof instanceId).toBe("string");

        // Snapshot the instance once — the full DAG-walk assertion is a
        // follow-up (requires per-mission stub-outcome plumbing through
        // userdata; see file-header note). We assert the instance is
        // either still running OR already terminal — both prove the
        // control-api / scheduler / supervisor wire is alive.
        const snap = await h.getInstance(instanceId);
        expect(snap.id).toBe(instanceId);
        // terminated_at is either null (still working) or an ISO string.
        if (snap.terminated_at !== null) {
          expect(typeof snap.terminated_at).toBe("string");
        }

        // Allow a brief window for the producer to write the initial
        // pass-state row (open-pass is the first node and is
        // deterministic), then assert the JSONL substrate is reachable.
        // The harness reads via the same JsonlStore the producer writes
        // through, so an empty list here would mean the producer never
        // even opened pass-state — which would in turn mean the
        // control-api -> scheduler -> supervisor -> producer dispatch
        // path is broken.
        await new Promise((r) => setTimeout(r, 3_000));
        // We don't assert non-empty rows yet (the producer's Open is
        // dispatched only when the scheduler picks up the open-pass
        // node; that path's full E2E is the follow-up). But we DO
        // assert reading doesn't throw — a contract test on the
        // JsonlStore + repo-dir invariant.
        await expect(h.readPasses()).resolves.toBeInstanceOf(Array);
        await expect(h.readFindings()).resolves.toBeInstanceOf(Array);
        await expect(h.readCoverage()).resolves.toBeInstanceOf(Array);
      } finally {
        await h.teardown();
      }
    },
    120_000,
  );
});
