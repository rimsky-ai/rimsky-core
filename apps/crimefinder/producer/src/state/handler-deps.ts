import type { Logger } from "pino";
import type { JsonlStore } from "../jsonl-store.js";
import type { SessionTokenRegistry } from "./session-tokens.js";
import type { IterationCounter } from "./iteration-counter.js";
import type { TestCache } from "./test-cache.js";
import type { GitOps } from "../git-ops.js";
import type { CrimefinderConfig } from "../config.js";
import type { TestRunMutex } from "./run-tests.js";
import type { CommitMutex } from "./commit-mutex.js";
import type { PartitionCache } from "../scopes/types.js";

export interface StateHandlerDeps {
  store: JsonlStore;
  tokens: SessionTokenRegistry;
  iterCounter: IterationCounter;
  testCache: TestCache;
  testRunMutex: TestRunMutex;
  commitMutex: CommitMutex;
  git: GitOps;
  config: CrimefinderConfig;
  partitionCache: PartitionCache;
  repoRoot: string;
  logger: Logger;
}

export class UnauthenticatedError extends Error {
  constructor() {
    super("invalid session_token");
    this.name = "UnauthenticatedError";
  }
}
