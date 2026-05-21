// Test-only helpers for building StateHandlerDeps fixtures.
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { pino } from "pino";
import { JsonlStore } from "../jsonl-store.js";
import { SessionTokenRegistry } from "./session-tokens.js";
import { IterationCounter } from "./iteration-counter.js";
import { TestCache } from "./test-cache.js";
import { TestRunMutex } from "./run-tests.js";
import { CommitMutex } from "./commit-mutex.js";
import { createGitOps } from "../git-ops.js";
import { ConfigSchema, CrimefinderConfig } from "../config.js";
import { createPartitionCache, PartitionCache } from "../scopes/types.js";
import type { StateHandlerDeps } from "./handler-deps.js";

export async function makeStateDeps(
  overrides: { config?: Partial<CrimefinderConfig>; partitionCache?: PartitionCache } = {},
): Promise<{ dir: string; deps: StateHandlerDeps }> {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-state-"));
  const logger = pino({ level: "silent" });
  const store = new JsonlStore({ repoRoot: dir, logger });
  const deps: StateHandlerDeps = {
    store,
    tokens: new SessionTokenRegistry(),
    iterCounter: new IterationCounter(store, logger),
    testCache: new TestCache(),
    testRunMutex: new TestRunMutex(),
    commitMutex: new CommitMutex(),
    git: createGitOps(),
    config: overrides.config
      ? ConfigSchema.parse(overrides.config)
      : ConfigSchema.parse({}),
    partitionCache: overrides.partitionCache ?? createPartitionCache(),
    repoRoot: dir,
    logger,
  };
  return { dir, deps };
}
