import fs from "node:fs/promises";
import path from "node:path";
import {
  parseFindingsLine,
  parsePassesLine,
  FindingsRow,
  PassesRow,
} from "@crimefinder/shared";
import { RimskyCli } from "../rimsky-cli.js";

interface StatusFlags {
  repo: string;
  template?: string;
  cli?: RimskyCli;
}

function parseFlags(argv: string[]): StatusFlags {
  let repo = process.cwd();
  let template: string | undefined;
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === "--repo") repo = argv[++i];
    else if (argv[i] === "--template") template = argv[++i];
  }
  return { repo, template };
}

async function readJsonl<T>(file: string, parse: (s: string) => T): Promise<T[]> {
  try {
    const raw = await fs.readFile(file, "utf-8");
    const out: T[] = [];
    for (const line of raw.split("\n")) {
      const t = line.trim();
      if (!t) continue;
      try {
        out.push(parse(t));
      } catch {
        // skip malformed
      }
    }
    return out;
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code === "ENOENT") return [];
    throw e;
  }
}

export interface RunStatusOptions {
  cli?: RimskyCli;
}

export async function runStatus(argv: string[], opts: RunStatusOptions = {}): Promise<number> {
  const { repo, template } = parseFlags(argv);
  const findingsPath = path.join(repo, ".crimefinder", "findings.jsonl");
  const passesPath = path.join(repo, ".crimefinder", "passes.jsonl");
  const findings: FindingsRow[] = await readJsonl(findingsPath, parseFindingsLine);
  const passes: PassesRow[] = await readJsonl(passesPath, parsePassesLine);

  const byClass = new Map<string, number>();
  for (const f of findings) {
    if (f.kind !== "finding") continue;
    const k = String(f.effective_class);
    byClass.set(k, (byClass.get(k) ?? 0) + 1);
  }
  const lastPass = [...passes].reverse().find((p) => p.kind === "pass_started");
  const lastFinished = [...passes].reverse().find((p) => p.kind === "pass_finished");

  // Query rimsky control-api for live (non-archived) passes. Failure is
  // non-fatal — we still display the JSONL history.
  const cli = opts.cli ?? new RimskyCli();
  const liveInstances = await cli.instanceList({ template });

  console.log("crimefinder status");
  console.log(`  repo:           ${repo}`);
  console.log(`  passes:         ${passes.filter((p) => p.kind === "pass_started").length}`);
  if (lastPass && lastPass.kind === "pass_started") {
    console.log(`  latest pass:    ${lastPass.id}`);
    console.log(`    mission:      ${lastPass.mission}`);
    console.log(`    started:      ${lastPass.ts}`);
  }
  if (lastFinished && lastFinished.kind === "pass_finished") {
    console.log(`    finished:     ${lastFinished.ts}`);
    console.log(`    coverage:     ${lastFinished.coverage_pct}%`);
  }
  console.log("  findings by class:");
  for (const [cls, n] of [...byClass.entries()].sort()) {
    console.log(`    class ${cls}: ${n}`);
  }
  console.log(`  live instances: ${liveInstances.length}`);
  for (const inst of liveInstances) {
    const id =
      typeof inst.instance_id === "string"
        ? inst.instance_id
        : typeof inst.id === "string"
          ? inst.id
          : "?";
    const status = typeof inst.status === "string" ? inst.status : "?";
    console.log(`    ${id} (${status})`);
  }
  return 0;
}
