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
import { openReport } from "./report.js";
import { createPartitionCache, OpenContext } from "./types.js";

const logger = pino({ level: "silent" });
const NOW = "2026-05-19T12:00:00.000+00:00";

describe("openReport", () => {
  it("writes a pass_finished row and returns it as payload", async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-report-"));
    const store = new JsonlStore({ repoRoot: dir, logger });
    await store.appendPassStarted({
      kind: "pass_started",
      id: "p_1",
      ts: NOW,
      mission: "m",
      trigger: "manual",
      template_hash: "x",
      fix_cycle_cap: 3,
      params_hash: "y",
    });
    const cache = createPartitionCache();
    cache.setZonePlan("p_1", [{ id: "z_a", label: "src/a", files: ["src/a/x.ts"] }]);
    await store.appendCoverage({
      ts: NOW,
      pass_id: "p_1",
      session_id: "s",
      zone_id: "z_a",
      file: "src/a/x.ts",
    });
    const ctx: OpenContext = {
      selector: "@report:pass_id=p_1",
      claimId: "c",
      repoRoot: dir,
      store,
      tokens: new SessionTokenRegistry(),
      iterCounter: new IterationCounter(store, logger),
      stateEndpointUrl: "url",
      partitionCache: cache,
      config: ConfigSchema.parse({}),
      git: createGitOps(),
      logger,
    };
    const r = await openReport(ctx);
    const summary = JSON.parse(new TextDecoder().decode(r.payload));
    expect(summary.kind).toBe("pass_finished");
    expect(summary.zones_planned).toBe(1);
    expect(summary.zones_completed).toBe(1);
    expect(summary.coverage_pct).toBe(100);
    const passes = await store.readPasses();
    expect(passes.some((p) => p.kind === "pass_finished")).toBe(true);
  });
});
