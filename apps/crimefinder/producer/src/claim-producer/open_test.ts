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
import { createPartitionCache, OpenContext } from "../scopes/types.js";
import { handleOpen } from "./open.js";

const logger = pino({ level: "silent" });

async function makeCtx(selector: string): Promise<OpenContext> {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-open-"));
  const store = new JsonlStore({ repoRoot: dir, logger });
  return {
    selector,
    claimId: "c_1",
    repoRoot: dir,
    store,
    tokens: new SessionTokenRegistry(),
    iterCounter: new IterationCounter(store, logger),
    stateEndpointUrl: "url",
    partitionCache: createPartitionCache(),
    config: ConfigSchema.parse({}),
    git: createGitOps(),
    logger,
  };
}

describe("handleOpen", () => {
  it("dispatches @pass-state to openPassState", async () => {
    const ctx = await makeCtx("@pass-state:new&mission=x&trigger=manual");
    const r = await handleOpen(
      { selector: "@pass-state:new&mission=x&trigger=manual", claim_id: "c_1" },
      ctx,
    );
    expect(r.type).toBe("acquired");
  });

  it("returns unavailable on unknown selectors", async () => {
    const ctx = await makeCtx("@nope:");
    const r = await handleOpen({ selector: "@nope:", claim_id: "c_1" }, ctx);
    expect(r.type).toBe("unavailable");
  });

  it("dispatches fan-out child via scope_data", async () => {
    const ctx = await makeCtx("");
    const scope = new TextEncoder().encode(
      JSON.stringify({
        kind: "source-tree-zone",
        pass_id: "p_1",
        zone_id: "z_a",
        zone_label: "src/a",
        zone_files: ["src/a/x.ts"],
      }),
    );
    const r = await handleOpen({ selector: "", claim_id: "c_1", scope_data: scope }, ctx);
    expect(r.type).toBe("acquired");
  });
});
