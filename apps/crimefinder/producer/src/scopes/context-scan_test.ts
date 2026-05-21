import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import pino from "pino";
import { JsonlStore } from "../jsonl-store.js";
import { SessionTokenRegistry } from "../state/session-tokens.js";
import { IterationCounter } from "../state/iteration-counter.js";
import { createGitOps } from "../git-ops.js";
import { ConfigSchema } from "../config.js";
import { openContextScan } from "./context-scan.js";
import { createPartitionCache, OpenContext } from "./types.js";

const logger = pino({ level: "silent" });

describe("openContextScan", () => {
  let dir: string;
  let ctx: OpenContext;
  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-ctxscan-"));
    const store = new JsonlStore({ repoRoot: dir, logger });
    ctx = {
      selector: "@context-scan:pass_id=p_abc",
      claimId: "c_2",
      repoRoot: dir,
      store,
      tokens: new SessionTokenRegistry(),
      iterCounter: new IterationCounter(store, logger),
      stateEndpointUrl: "localhost:7081",
      partitionCache: createPartitionCache(),
      config: ConfigSchema.parse({}),
      git: createGitOps(),
      logger,
    };
  });

  it("reports CLAUDE.md presence and concept files", async () => {
    await fs.writeFile(path.join(dir, "CLAUDE.md"), "# project");
    await fs.mkdir(path.join(dir, ".ok-planner/design/concepts"), { recursive: true });
    await fs.writeFile(path.join(dir, ".ok-planner/design/concepts/foo.md"), "# foo");

    const r = await openContextScan(ctx);
    const manifest = JSON.parse(new TextDecoder().decode(r.payload));
    expect(manifest.claude_md_present).toBe(true);
    expect(manifest.concepts).toEqual([{ slug: "foo", path: ".ok-planner/design/concepts/foo.md" }]);
  });

  it("handles missing design dirs gracefully", async () => {
    const r = await openContextScan(ctx);
    const manifest = JSON.parse(new TextDecoder().decode(r.payload));
    expect(manifest.claude_md_present).toBe(false);
    expect(manifest.concepts).toEqual([]);
    expect(manifest.tensions).toEqual([]);
  });
});
