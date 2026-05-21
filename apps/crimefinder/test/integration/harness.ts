/**
 * Real-rimsky integration harness for crimefinder.
 *
 * Brings up the full rimsky stack as host subprocesses against a tmp
 * sqlite file, plus the crimefinder-producer + crimefinder-executor as
 * node subprocesses (executor in stub mode), and exposes a small driver
 * API for registering templates, creating instances, polling terminal
 * state, and reading the producer's JSONL store.
 *
 * Unlike `test/scenarios/harness.ts` (in-process, drives gates directly),
 * this harness exercises the actual wire surface: rimsky's gRPC
 * Capabilities handshake against the producer + executor, the YAML
 * template parser, the control-api HTTP/JSON, the scheduler+supervisor
 * dispatch loop, and address-bytes round-tripping.
 *
 * Prerequisites (the harness validates these and fails fast):
 *   - rimsky Go binaries built at `bin/<name>` (rimsky-migrate,
 *     rimsky-control-api, rimsky-scheduler, rimsky-supervisor, rimsky).
 *     Run `make build-all` then `go build -o bin/<name> ./cmd/<name>`,
 *     or use the `buildRimskyBinaries` helper below.
 *   - crimefinder workspace built (`npm run build` from
 *     `apps/crimefinder/`) so the producer + executor `dist/main.js`
 *     entry points exist.
 *
 * Stub-mode + no Claude CLI: the executor binary runs with
 * `CRIMEFINDER_EXECUTOR_STUB_MODE=1` and consults `userdata.stub_outcome`
 * for canned outcomes. The harness threads stub outcomes through the
 * `params.*` (which the template substitutes into userdata) when the
 * template wiring permits; otherwise each fan-out child receives the
 * default-success stub outcome.
 *
 * @source: test/scenarios/harness.ts (in-process; this is the
 *          subprocess-orchestrating cousin).
 * @diverged: true
 * @reason: integration coverage requires the real wire surface — the
 *          in-process harness intentionally short-circuits past it.
 */
import { spawn, ChildProcess, execFile as execFileCb } from "node:child_process";
import { promisify } from "node:util";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { JsonlStore } from "@crimefinder/producer/dist/jsonl-store.js";
import { pino } from "pino";
import type { FindingsRow, CoverageRow, PassesRow } from "@crimefinder/shared";

const execFile = promisify(execFileCb);
const here = path.dirname(fileURLToPath(import.meta.url));
const repoRootRimsky = path.resolve(here, "../../../..");

export interface IntegrationHarness {
  /** Tmp git repo the producer was pointed at. Fixture content was
   *  copied here and an initial commit was made. */
  repoRoot: string;
  /** http://host:port of the rimsky control-api subprocess. */
  controlApiUrl: string;
  /** gRPC endpoint of the crimefinder-producer subprocess. */
  producerEndpoint: string;
  /** gRPC endpoint of the crimefinder-executor subprocess (stub mode). */
  executorEndpoint: string;

  registerTemplate(yamlPath: string): Promise<string>;
  deployTemplate(templateHash: string): Promise<void>;
  createInstance(templateHash: string, params: Record<string, unknown>): Promise<string>;
  waitForInstanceTerminal(
    instanceId: string,
    timeoutMs?: number,
  ): Promise<{ id: string; terminated_at: string | null; raw: unknown }>;
  getInstance(instanceId: string): Promise<{ id: string; terminated_at: string | null; raw: unknown }>;

  readFindings(): Promise<FindingsRow[]>;
  readPasses(): Promise<PassesRow[]>;
  readCoverage(): Promise<CoverageRow[]>;

  /** Resolves to the array of git commit messages on `main` since the
   *  initial commit (most-recent first). Useful for asserting that the
   *  stub executor's `review_commit_fix` gate produced a fix commit. */
  gitLogMessages(): Promise<string[]>;

  teardown(): Promise<void>;
}

export interface SetupOptions {
  /** Absolute path to a fixture directory whose contents will be copied
   *  into a fresh tmp dir and `git init`ed. */
  fixtureDir: string;
  /** Override default rimsky-binary paths (resolved relative to the
   *  rimsky repo root). All optional; defaults look in `bin/`. */
  binaries?: {
    migrate?: string;
    controlApi?: string;
    scheduler?: string;
    supervisor?: string;
    rimskyCli?: string;
  };
  /** Override producer / executor JS entrypoints. */
  producerEntrypoint?: string;
  executorEntrypoint?: string;
}

export async function setupHarness(opts: SetupOptions): Promise<IntegrationHarness> {
  // ── 1. Resolve + verify binaries / dist files exist. ─────────────────
  const binDir = path.resolve(repoRootRimsky, "bin");
  const migrateBin = opts.binaries?.migrate ?? path.join(binDir, "rimsky-migrate");
  const controlBin = opts.binaries?.controlApi ?? path.join(binDir, "rimsky-control-api");
  const schedulerBin = opts.binaries?.scheduler ?? path.join(binDir, "rimsky-scheduler");
  const supervisorBin = opts.binaries?.supervisor ?? path.join(binDir, "rimsky-supervisor");
  const rimskyCliBin = opts.binaries?.rimskyCli ?? path.join(binDir, "rimsky");
  for (const [name, p] of Object.entries({
    migrateBin, controlBin, schedulerBin, supervisorBin, rimskyCliBin,
  })) {
    try { await fs.access(p, fs.constants.X_OK); } catch {
      throw new Error(
        `integration harness: required binary missing or not executable: ${name}=${p}\n` +
        `  Build with: cd ${repoRootRimsky} && go build -o bin/<name> ./cmd/<name>\n` +
        `  (run for each of: rimsky-migrate, rimsky-control-api, rimsky-scheduler, rimsky-supervisor, rimsky)`,
      );
    }
  }
  const producerJs = opts.producerEntrypoint ?? path.resolve(
    repoRootRimsky, "apps/crimefinder/producer/dist/main.js",
  );
  const executorJs = opts.executorEntrypoint ?? path.resolve(
    repoRootRimsky, "apps/crimefinder/executor/dist/main.js",
  );
  for (const p of [producerJs, executorJs]) {
    try { await fs.access(p); } catch {
      throw new Error(
        `integration harness: required workspace dist missing: ${p}\n` +
        `  Build with: cd ${repoRootRimsky}/apps/crimefinder && npm run build`,
      );
    }
  }

  // ── 2. Stage repo (copy fixture, git init). ──────────────────────────
  const repoRoot = await fs.mkdtemp(path.join(os.tmpdir(), "cf-int-repo-"));
  await copyDir(opts.fixtureDir, repoRoot);
  await execFile("git", ["init", "-q", "-b", "main"], { cwd: repoRoot });
  await execFile("git", ["config", "user.email", "test@example.com"], { cwd: repoRoot });
  await execFile("git", ["config", "user.name", "test"], { cwd: repoRoot });
  await execFile("git", ["config", "commit.gpgsign", "false"], { cwd: repoRoot });
  await fs.writeFile(path.join(repoRoot, ".gitignore"), ".crimefinder/\n");
  await execFile("git", ["add", "."], { cwd: repoRoot });
  await execFile("git", ["commit", "-qm", "init"], { cwd: repoRoot });

  // ── 3. Pick free host ports. ─────────────────────────────────────────
  const controlApiPort = await pickFreePort();
  const producerGrpcPort = await pickFreePort();
  const producerHttpPort = await pickFreePort();
  const executorGrpcPort = await pickFreePort();
  const supervisorCallbackPort = await pickFreePort();

  // ── 4. Write rimsky.yml + supervisor-config.yml. ─────────────────────
  const configDir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-int-cfg-"));
  const sqlitePath = path.join(configDir, "state.db");
  const rimskyYmlPath = path.join(configDir, "rimsky.yml");
  await fs.writeFile(
    rimskyYmlPath,
    [
      "persistence:",
      "  driver: sqlite",
      "  sqlite:",
      `    path: ${sqlitePath}`,
      "",
      "claim_producers:",
      "  crimefinder:",
      `    endpoint: "grpc://127.0.0.1:${producerGrpcPort}"`,
      "    protocols: [claim_producer]",
      "    write_semantics_allowed: [sync]",
      "",
      "executors:",
      "  crimefinder:",
      "    transport: grpc",
      `    endpoint: "127.0.0.1:${executorGrpcPort}"`,
      "    tls: off",
      "    protocols: [executor]",
      "",
    ].join("\n"),
  );
  const supervisorYmlPath = path.join(configDir, "supervisor.yml");
  await fs.writeFile(
    supervisorYmlPath,
    [
      "concurrency: 4",
      "heartbeat_interval_ms: 1000",
      "claim_poll_interval_ms: 250",
      "callback:",
      "  host: 127.0.0.1",
      `  port: ${supervisorCallbackPort}`,
      "  advertise_host: 127.0.0.1",
      `  advertise_port: ${supervisorCallbackPort}`,
      "",
    ].join("\n"),
  );

  // ── 5. Run migrations. ───────────────────────────────────────────────
  await runOnce(migrateBin, [], { RIMSKY_CONFIG: rimskyYmlPath });

  // ── 6. Start producer + executor subprocesses (need them up before
  //    control-api dials Capabilities). ─────────────────────────────────
  const procs: ChildProcess[] = [];
  const logs: Array<{ name: string; chunks: string[] }> = [];

  const startNode = (name: string, script: string, env: Record<string, string>): ChildProcess => {
    const child = spawn(process.execPath, [script], {
      env: { ...process.env, ...env, NODE_NO_WARNINGS: "1" },
      stdio: ["ignore", "pipe", "pipe"],
    });
    const entry = { name, chunks: [] as string[] };
    logs.push(entry);
    child.stdout?.setEncoding("utf-8");
    child.stderr?.setEncoding("utf-8");
    child.stdout?.on("data", (chunk: string) => { entry.chunks.push(chunk); });
    child.stderr?.on("data", (chunk: string) => { entry.chunks.push(chunk); });
    procs.push(child);
    return child;
  };
  const startBin = (name: string, bin: string, env: Record<string, string>): ChildProcess => {
    const child = spawn(bin, [], {
      env: { ...process.env, ...env },
      stdio: ["ignore", "pipe", "pipe"],
    });
    const entry = { name, chunks: [] as string[] };
    logs.push(entry);
    child.stdout?.setEncoding("utf-8");
    child.stderr?.setEncoding("utf-8");
    child.stdout?.on("data", (chunk: string) => { entry.chunks.push(chunk); });
    child.stderr?.on("data", (chunk: string) => { entry.chunks.push(chunk); });
    procs.push(child);
    return child;
  };

  startNode("producer", producerJs, {
    CRIMEFINDER_PRODUCER_HOST: "127.0.0.1",
    CRIMEFINDER_PRODUCER_PORT_GRPC: String(producerGrpcPort),
    CRIMEFINDER_PRODUCER_PORT_HTTP: String(producerHttpPort),
    CRIMEFINDER_PRODUCER_REPO_ROOT: repoRoot,
    CRIMEFINDER_PRODUCER_STATE_ENDPOINT_URL: `127.0.0.1:${producerGrpcPort}`,
    LOG_LEVEL: "warn",
  });
  startNode("executor", executorJs, {
    CRIMEFINDER_EXECUTOR_HOST: "127.0.0.1",
    CRIMEFINDER_EXECUTOR_PORT_GRPC: String(executorGrpcPort),
    CRIMEFINDER_EXECUTOR_STUB_MODE: "1",
    LOG_LEVEL: "warn",
  });

  // Producer exposes /health on producerHttpPort. Executor doesn't ship
  // an HTTP health endpoint — wait for its grpc listener to bind by
  // polling a TCP connect.
  await waitForHttp(`http://127.0.0.1:${producerHttpPort}/health`, 10_000);
  await waitForTcp("127.0.0.1", executorGrpcPort, 10_000);

  // ── 7. Start control-api + scheduler + supervisor. ───────────────────
  startBin("control-api", controlBin, {
    RIMSKY_CONFIG: rimskyYmlPath,
    RIMSKY_CONTROL_API_HOST: "127.0.0.1",
    RIMSKY_CONTROL_API_PORT: String(controlApiPort),
    RIMSKY_LOG_LEVEL: "warn",
  });
  startBin("scheduler", schedulerBin, {
    RIMSKY_CONFIG: rimskyYmlPath,
    RIMSKY_SCHEDULER_TICK_MS: "250",
    RIMSKY_LOG_LEVEL: "warn",
  });
  startBin("supervisor", supervisorBin, {
    RIMSKY_CONFIG: rimskyYmlPath,
    RIMSKY_SUPERVISOR_CONFIG: supervisorYmlPath,
    RIMSKY_LOG_LEVEL: "warn",
  });

  const controlApiUrl = `http://127.0.0.1:${controlApiPort}`;
  try {
    await waitForHttp(`${controlApiUrl}/health`, 15_000);
  } catch (err) {
    // Surface subprocess logs to help diagnose startup failures.
    const dump = logs.map((l) => `--- ${l.name} ---\n${l.chunks.join("")}`).join("\n");
    await teardownProcs(procs);
    await fs.rm(repoRoot, { recursive: true, force: true });
    await fs.rm(configDir, { recursive: true, force: true });
    throw new Error(`integration harness: control-api did not become healthy: ${err}\n${dump}`);
  }

  // ── 8. Build the JSONL store reader (shares the repo dir). ──────────
  const storeLogger = pino({ level: "silent" });
  const store = new JsonlStore({ repoRoot, logger: storeLogger });

  const teardown = async (): Promise<void> => {
    await teardownProcs(procs);
    await fs.rm(repoRoot, { recursive: true, force: true }).catch(() => {});
    await fs.rm(configDir, { recursive: true, force: true }).catch(() => {});
  };

  const getInstanceImpl = async (
    instanceId: string,
  ): Promise<{ id: string; terminated_at: string | null; raw: unknown }> => {
    const res = await fetch(`${controlApiUrl}/instances/${encodeURIComponent(instanceId)}`);
    if (!res.ok) {
      const text = await res.text();
      throw new Error(`getInstance ${res.status}: ${text}`);
    }
    const raw = (await res.json()) as { id: string; terminated_at?: string | null };
    return { id: raw.id, terminated_at: raw.terminated_at ?? null, raw };
  };

  return {
    repoRoot,
    controlApiUrl,
    producerEndpoint: `grpc://127.0.0.1:${producerGrpcPort}`,
    executorEndpoint: `127.0.0.1:${executorGrpcPort}`,

    async registerTemplate(yamlPath: string): Promise<string> {
      const { stdout } = await execFile(
        rimskyCliBin,
        ["template", "register", "-endpoint", controlApiUrl, "-o", "json", yamlPath],
        { env: { ...process.env, RIMSKY_CONTROL_API: controlApiUrl } },
      );
      const parsed = JSON.parse(stdout) as { template_id?: string; id?: string };
      const hash = parsed.template_id ?? parsed.id;
      if (!hash) throw new Error(`registerTemplate: missing template_id in response: ${stdout}`);
      return hash;
    },

    async deployTemplate(templateHash: string): Promise<void> {
      await execFile(
        rimskyCliBin,
        ["template", "deploy", "-endpoint", controlApiUrl, templateHash],
        { env: { ...process.env, RIMSKY_CONTROL_API: controlApiUrl } },
      );
    },

    async createInstance(templateHash: string, params: Record<string, unknown>): Promise<string> {
      const body = JSON.stringify({ template: templateHash, params });
      const res = await fetch(`${controlApiUrl}/instances`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body,
      });
      if (!res.ok) {
        const text = await res.text();
        throw new Error(`createInstance ${res.status}: ${text}`);
      }
      const parsed = (await res.json()) as { instance_id?: string; id?: string };
      const id = parsed.instance_id ?? parsed.id;
      if (!id) throw new Error(`createInstance: missing instance_id in response: ${JSON.stringify(parsed)}`);
      return id;
    },

    getInstance: getInstanceImpl,

    async waitForInstanceTerminal(instanceId, timeoutMs = 60_000) {
      const start = Date.now();
      let last: { id: string; terminated_at: string | null; raw: unknown } = {
        id: instanceId, terminated_at: null, raw: null,
      };
      while (Date.now() - start < timeoutMs) {
        last = await getInstanceImpl(instanceId);
        if (last.terminated_at) return last;
        await new Promise((r) => setTimeout(r, 500));
      }
      throw new Error(
        `waitForInstanceTerminal: instance ${instanceId} did not reach terminal in ${timeoutMs}ms; ` +
        `last state: ${JSON.stringify(last.raw)}`,
      );
    },

    readFindings: () => store.readFindings(),
    readPasses: () => store.readPasses(),
    readCoverage: () => store.readCoverage(),

    async gitLogMessages(): Promise<string[]> {
      const { stdout } = await execFile("git", ["log", "--format=%B"], { cwd: repoRoot });
      // Split on double-newline; commit messages are separated by blank lines.
      return stdout.split(/\n\n+/).map((s) => s.trim()).filter(Boolean);
    },

    teardown,
  };
}

// ──────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────

async function copyDir(src: string, dst: string): Promise<void> {
  await fs.mkdir(dst, { recursive: true });
  const entries = await fs.readdir(src, { withFileTypes: true });
  for (const ent of entries) {
    const a = path.join(src, ent.name);
    const b = path.join(dst, ent.name);
    if (ent.isDirectory()) await copyDir(a, b);
    else if (ent.isFile()) await fs.copyFile(a, b);
  }
}

async function pickFreePort(): Promise<number> {
  const net = await import("node:net");
  return await new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      if (addr && typeof addr === "object") {
        const port = addr.port;
        srv.close(() => resolve(port));
      } else {
        reject(new Error("pickFreePort: unexpected address shape"));
      }
    });
  });
}

async function waitForHttp(url: string, timeoutMs: number): Promise<void> {
  const start = Date.now();
  let lastErr: unknown;
  while (Date.now() - start < timeoutMs) {
    try {
      const res = await fetch(url, { signal: AbortSignal.timeout(1000) });
      if (res.status === 200) return;
    } catch (err) {
      lastErr = err;
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`waitForHttp(${url}) timed out after ${timeoutMs}ms; last err: ${String(lastErr)}`);
}

async function waitForTcp(host: string, port: number, timeoutMs: number): Promise<void> {
  const net = await import("node:net");
  const start = Date.now();
  let lastErr: unknown;
  while (Date.now() - start < timeoutMs) {
    try {
      await new Promise<void>((resolve, reject) => {
        const sock = net.createConnection({ host, port });
        sock.once("connect", () => { sock.end(); resolve(); });
        sock.once("error", reject);
        sock.setTimeout(800, () => { sock.destroy(new Error("timeout")); });
      });
      return;
    } catch (err) {
      lastErr = err;
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`waitForTcp(${host}:${port}) timed out after ${timeoutMs}ms; last err: ${String(lastErr)}`);
}

async function runOnce(bin: string, args: string[], env: Record<string, string>): Promise<void> {
  await execFile(bin, args, { env: { ...process.env, ...env } });
}

async function teardownProcs(procs: ChildProcess[]): Promise<void> {
  for (const p of procs) {
    if (!p.killed) {
      try { p.kill("SIGTERM"); } catch { /* ignore */ }
    }
  }
  // Give them a moment to flush, then SIGKILL stragglers.
  await new Promise((r) => setTimeout(r, 500));
  for (const p of procs) {
    if (!p.killed && p.exitCode === null) {
      try { p.kill("SIGKILL"); } catch { /* ignore */ }
    }
  }
}
