import fs from "node:fs/promises";
import path from "node:path";
import type { Logger } from "pino";
import {
  FindingRow,
  StatusUpdateRow,
  TensionConfirmationRow,
  HelpRequestRow,
  FindingsRow,
  FindingsRowSchema,
  CoverageRow,
  CoverageRowSchema,
  PassStartedRow,
  PassFinishedRow,
  IterMarkerRow,
  SkipZoneRow,
  ZonePlanRow,
  DedupBatchesRow,
  PassClosedEmittedRow,
  PassesRow,
  PassesRowSchema,
  generateRowId,
} from "@crimefinder/shared";
import { JsonlMutex } from "./jsonl-mutex.js";

const FINDINGS_FILE = "findings.jsonl";
const COVERAGE_FILE = "coverage.jsonl";
const PASSES_FILE = "passes.jsonl";

export interface JsonlStoreOptions {
  repoRoot: string;
  logger: Logger;
}

export class JsonlStore {
  private readonly repoRoot: string;
  private readonly logger: Logger;
  private readonly mutexes = new Map<string, JsonlMutex>();

  constructor(opts: JsonlStoreOptions) {
    this.repoRoot = opts.repoRoot;
    this.logger = opts.logger;
  }

  private mutexFor(file: string): JsonlMutex {
    let m = this.mutexes.get(file);
    if (!m) {
      m = new JsonlMutex();
      this.mutexes.set(file, m);
    }
    return m;
  }

  private filePath(name: string): string {
    return path.join(this.repoRoot, ".crimefinder", name);
  }

  async ensureDir(): Promise<void> {
    await fs.mkdir(path.join(this.repoRoot, ".crimefinder"), { recursive: true });
  }

  // The absolute path to the .crimefinder directory. Useful for other
  // persisters (e.g. SessionTokenRegistry) that want to land their JSONL
  // alongside findings/coverage/passes.
  async getStoreDir(): Promise<string> {
    await this.ensureDir();
    return path.join(this.repoRoot, ".crimefinder");
  }

  private async appendLine(file: string, line: string): Promise<void> {
    await this.ensureDir();
    await this.mutexFor(file).withLock(async () => {
      await fs.appendFile(this.filePath(file), line + "\n", "utf-8");
    });
  }

  async appendFinding(
    row: FindingRow | StatusUpdateRow | TensionConfirmationRow | HelpRequestRow,
  ): Promise<void> {
    FindingsRowSchema.parse(row);
    await this.appendLine(FINDINGS_FILE, JSON.stringify(row));
  }

  async appendCoverage(row: CoverageRow): Promise<void> {
    CoverageRowSchema.parse(row);
    await this.appendLine(COVERAGE_FILE, JSON.stringify(row));
  }

  async appendPassStarted(row: PassStartedRow): Promise<void> {
    PassesRowSchema.parse(row);
    await this.appendLine(PASSES_FILE, JSON.stringify(row));
  }

  async appendPassFinished(row: PassFinishedRow): Promise<void> {
    PassesRowSchema.parse(row);
    await this.appendLine(PASSES_FILE, JSON.stringify(row));
  }

  async appendIterMarker(row: IterMarkerRow): Promise<void> {
    PassesRowSchema.parse(row);
    await this.appendLine(PASSES_FILE, JSON.stringify(row));
  }

  async appendSkipZone(row: SkipZoneRow): Promise<void> {
    PassesRowSchema.parse(row);
    await this.appendLine(PASSES_FILE, JSON.stringify(row));
  }

  // Per-pass monotonic write counter scoped to a kind. The caller MUST
  // hold the passes-file mutex (i.e. nest this inside a
  // `withPassesLock`); the read+max walk + the eventual write together
  // form one critical section. Returns the next `seq` to assign.
  private nextSeqFor(rows: PassesRow[], kind: ZonePlanRow["kind"] | DedupBatchesRow["kind"], passId: string): number {
    let max = -1;
    for (const r of rows) {
      if (r.kind !== kind) continue;
      if (r.pass_id !== passId) continue;
      const s = typeof r.seq === "number" ? r.seq : -1;
      if (s > max) max = s;
    }
    return max + 1;
  }

  async appendZonePlan(row: ZonePlanRow): Promise<void> {
    // Atomically assign a per-pass monotonic `seq` under the passes-file
    // mutex so concurrent writers can't tie on `ts` (ISO-8601 ms
    // granularity is too coarse under high throughput). Recovery uses
    // `seq` for last-wins ordering.
    await this.mutexFor(PASSES_FILE).withLock(async () => {
      if (typeof row.seq !== "number") {
        const existing = await this.readPassesNoLock();
        row = { ...row, seq: this.nextSeqFor(existing, "zone_plan", row.pass_id) };
      }
      PassesRowSchema.parse(row);
      await this.ensureDir();
      await fs.appendFile(this.filePath(PASSES_FILE), JSON.stringify(row) + "\n", "utf-8");
    });
  }

  async appendDedupBatches(row: DedupBatchesRow): Promise<void> {
    await this.mutexFor(PASSES_FILE).withLock(async () => {
      if (typeof row.seq !== "number") {
        const existing = await this.readPassesNoLock();
        row = { ...row, seq: this.nextSeqFor(existing, "dedup_batches", row.pass_id) };
      }
      PassesRowSchema.parse(row);
      await this.ensureDir();
      await fs.appendFile(this.filePath(PASSES_FILE), JSON.stringify(row) + "\n", "utf-8");
    });
  }

  // First-writer-wins atomic check-then-append for the `pass_closed`
  // de-dup marker. Returns `true` iff this call inserted the row (the
  // caller is now responsible for emitting `pass_closed`); returns
  // `false` if a row already exists (someone else emits). Held under
  // the passes-file mutex so concurrent zone-completions serialize
  // through one critical section.
  async tryClaimPassClosedEmission(passId: string): Promise<boolean> {
    return this.mutexFor(PASSES_FILE).withLock(async () => {
      const existing = await this.readPassesNoLock();
      for (const r of existing) {
        if (r.kind === "pass_closed_emitted" && r.pass_id === passId) {
          return false;
        }
      }
      const row: PassClosedEmittedRow = {
        kind: "pass_closed_emitted",
        id: generateRowId(),
        ts: new Date().toISOString(),
        pass_id: passId,
      };
      PassesRowSchema.parse(row);
      await this.ensureDir();
      await fs.appendFile(this.filePath(PASSES_FILE), JSON.stringify(row) + "\n", "utf-8");
      return true;
    });
  }

  // Lock-free read for use inside an outer `mutexFor(PASSES_FILE)`
  // critical section. Public callers should use `readPasses()`.
  private async readPassesNoLock(): Promise<PassesRow[]> {
    let raw: string;
    try {
      raw = await fs.readFile(this.filePath(PASSES_FILE), "utf-8");
    } catch (e) {
      if ((e as NodeJS.ErrnoException).code === "ENOENT") return [];
      throw e;
    }
    const out: PassesRow[] = [];
    for (const line of raw.split("\n")) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      try {
        out.push(PassesRowSchema.parse(JSON.parse(trimmed)));
      } catch {
        // ignored — matches the lenient readJsonl path
      }
    }
    return out;
  }

  private async readJsonl<T>(file: string, parse: (obj: unknown) => T): Promise<T[]> {
    let raw: string;
    try {
      raw = await fs.readFile(this.filePath(file), "utf-8");
    } catch (e) {
      if ((e as NodeJS.ErrnoException).code === "ENOENT") return [];
      throw e;
    }
    const out: T[] = [];
    for (const line of raw.split("\n")) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      let obj: unknown;
      try {
        obj = JSON.parse(trimmed);
      } catch (e) {
        this.logger.warn({ file, err: String(e), line: trimmed.slice(0, 200) }, "jsonl_parse_error");
        continue;
      }
      try {
        out.push(parse(obj));
      } catch (e) {
        this.logger.warn({ file, err: String(e) }, "jsonl_schema_error");
      }
    }
    return out;
  }

  async readFindings(): Promise<FindingsRow[]> {
    return this.readJsonl(FINDINGS_FILE, (o) => FindingsRowSchema.parse(o));
  }

  async readCoverage(): Promise<CoverageRow[]> {
    return this.readJsonl(COVERAGE_FILE, (o) => CoverageRowSchema.parse(o));
  }

  async readPasses(): Promise<PassesRow[]> {
    return this.readJsonl(PASSES_FILE, (o) => PassesRowSchema.parse(o));
  }
}
