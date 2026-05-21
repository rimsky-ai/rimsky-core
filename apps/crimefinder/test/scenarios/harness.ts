/**
 * Scenario harness for crimefinder.
 *
 * Two flavors:
 *  - In-process (default): spins up the producer's gRPC server, drives gate
 *    calls via StateClient. Does NOT bring up the full rimsky stack — that
 *    requires docker-compose + a pre-built crimefinder-producer image.
 *    These tests exercise the producer surface, the gate vocabulary, the
 *    atomic commit-fix transaction, JSONL durability, recovery, dedup,
 *    class-5b routing, coverage thresholds, and tension confirmation —
 *    the bulk of the crimefinder business invariants.
 *  - Full-stack (CRIMEFINDER_INTEGRATION=1): pulls in testcontainers,
 *    builds the producer image, and brings up postgres + rimsky stack.
 *    Slow and Docker-dependent; gated.
 *
 * The plan's verification commands run the in-process scenarios; the
 * full-stack hook exists so a CI lane can opt in.
 */
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { execFile as execFileCb } from "node:child_process";
import { pino, Logger } from "pino";
import { startGrpcServer, RunningServer } from "@crimefinder/producer/dist/server.js";
import { JsonlStore } from "@crimefinder/producer/dist/jsonl-store.js";
import { SessionTokenRegistry } from "@crimefinder/producer/dist/state/session-tokens.js";
import { createPartitionCache, PartitionCache } from "@crimefinder/producer/dist/scopes/types.js";
import {
  FindingsRow,
  CoverageRow,
  PassesRow,
  encodeAddress,
} from "@crimefinder/shared";

const execFile = promisify(execFileCb);

export interface ScenarioHarness {
  repoRoot: string;
  producer: RunningServer;
  store: JsonlStore;
  tokens: SessionTokenRegistry;
  partitionCache: PartitionCache;
  logger: Logger;
  // Issues a session token via the producer's in-process SessionTokenRegistry
  // (the same one its gRPC server uses), so tests can call typed-state RPCs
  // directly without going through Open.
  issueTestToken(args: {
    passId: string;
    sessionId: string;
    zoneId?: string;
    role?: "review-zone" | "fix-cycle" | "dedup" | "re-review";
  }): string;
  readFindings(): Promise<FindingsRow[]>;
  readCoverage(): Promise<CoverageRow[]>;
  readPasses(): Promise<PassesRow[]>;
  teardown(): Promise<void>;
}

export interface SetupOptions {
  fixtureDir?: string;
  initialFiles?: Record<string, string>;
  config?: string; // YAML string for .crimefinder/config.yml
}

async function copyFixture(src: string, dst: string): Promise<void> {
  await fs.mkdir(dst, { recursive: true });
  const entries = await fs.readdir(src, { withFileTypes: true });
  for (const ent of entries) {
    const a = path.join(src, ent.name);
    const b = path.join(dst, ent.name);
    if (ent.isDirectory()) await copyFixture(a, b);
    else if (ent.isFile()) await fs.copyFile(a, b);
  }
}

async function initGitRepo(dir: string): Promise<void> {
  await execFile("git", ["init", "-q", "-b", "main"], { cwd: dir });
  await execFile("git", ["config", "user.email", "test@example.com"], { cwd: dir });
  await execFile("git", ["config", "user.name", "test"], { cwd: dir });
  await execFile("git", ["config", "commit.gpgsign", "false"], { cwd: dir });
  await fs.writeFile(path.join(dir, ".gitignore"), ".crimefinder/\n");
  await execFile("git", ["add", "."], { cwd: dir });
  await execFile("git", ["commit", "-qm", "init"], { cwd: dir });
}

// To share the producer's in-process tokens and partition cache with the
// test, we construct our own JsonlStore against the same dir.
export async function setupHarness(opts: SetupOptions = {}): Promise<ScenarioHarness> {
  const repoRoot = await fs.mkdtemp(path.join(os.tmpdir(), "cf-scenario-"));
  if (opts.fixtureDir) {
    await copyFixture(opts.fixtureDir, repoRoot);
  }
  for (const [rel, content] of Object.entries(opts.initialFiles ?? {})) {
    const full = path.join(repoRoot, rel);
    await fs.mkdir(path.dirname(full), { recursive: true });
    await fs.writeFile(full, content);
  }
  if (opts.config) {
    await fs.mkdir(path.join(repoRoot, ".crimefinder"), { recursive: true });
    await fs.writeFile(path.join(repoRoot, ".crimefinder", "config.yml"), opts.config);
  }
  await initGitRepo(repoRoot);

  const logger = pino({ level: "silent" });
  // The harness shares its tokens + partitionCache with the producer's
  // in-process gRPC server so test-issued tokens are recognized by gates
  // dispatched via gRPC, and so partition plans cached by SplitScope are
  // visible to direct handler calls.
  const tokens = new SessionTokenRegistry();
  const partitionCache = createPartitionCache();
  const producer = await startGrpcServer({
    host: "127.0.0.1",
    port: 0,
    repoRoot,
    stateEndpointUrl: "127.0.0.1:0",
    logger,
    tokens,
    partitionCache,
  });
  const store = new JsonlStore({ repoRoot, logger });

  return {
    repoRoot,
    producer,
    store,
    tokens,
    partitionCache,
    logger,
    issueTestToken: ({ passId, sessionId, zoneId, role }) =>
      tokens.issue({ passId, claimHandleId: sessionId, zoneId, role, issuedAt: Date.now() }),
    readFindings: () => store.readFindings(),
    readCoverage: () => store.readCoverage(),
    readPasses: () => store.readPasses(),
    teardown: async () => {
      await producer.shutdown();
      await fs.rm(repoRoot, { recursive: true, force: true });
    },
  };
}

export async function gitCommit(dir: string, msg: string, paths: string[] = ["."]): Promise<string> {
  await execFile("git", ["add", ...paths], { cwd: dir });
  await execFile("git", ["commit", "-qm", msg], { cwd: dir });
  const { stdout } = await execFile("git", ["rev-parse", "HEAD"], { cwd: dir });
  return stdout.trim();
}

export function encodePassStateAddress(args: {
  passId: string;
  stateEndpointUrl: string;
  sessionToken: string;
}): Uint8Array {
  return encodeAddress({
    kind: "pass-state",
    pass_id: args.passId,
    state_endpoint_url: args.stateEndpointUrl,
    session_token: args.sessionToken,
  });
}
