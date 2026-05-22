import fs from "node:fs/promises";
import path from "node:path";
import yaml from "yaml";
import { z } from "zod";

// Minimal mirror of the producer's CrimefinderConfig for the executor —
// the executor reads `.crimefinder/config.yml` directly at dispatch time
// because the rimsky template DSL has no clean way to forward nested cfg
// substructures into per-node attribute defaults. The executor only
// needs the coverage knobs; everything else is producer-side
// enforcement.
//
// Keep this in sync with `producer/src/config.ts::ConfigSchema` for the
// fields we read. Drift is OK for other knobs (the executor doesn't
// consume them), but coverage_threshold_pct / coverage_on_below_threshold
// MUST match producer defaults or skip-zone semantics will diverge.
const RepoConfigSchema = z
  .object({
    coverage: z
      .object({
        threshold_pct: z.number().min(0).max(100).default(80),
        on_below_threshold: z
          .enum(["require_skip", "warn", "allow"])
          .default("require_skip"),
      })
      .default({}),
  })
  .default({});

export interface RepoCoverageKnobs {
  thresholdPct: number;
  onBelow: "require_skip" | "warn" | "allow";
}

// Read coverage knobs from `<repoRoot>/.crimefinder/config.yml`. Returns
// the defaults when the file is absent or unparseable (logged at warn).
export async function readRepoCoverage(repoRoot: string): Promise<RepoCoverageKnobs> {
  const filePath = path.join(repoRoot, ".crimefinder", "config.yml");
  let raw: string;
  try {
    raw = await fs.readFile(filePath, "utf-8");
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code === "ENOENT") {
      return { thresholdPct: 80, onBelow: "require_skip" };
    }
    throw e;
  }
  let parsed: unknown;
  try {
    parsed = yaml.parse(raw);
  } catch {
    return { thresholdPct: 80, onBelow: "require_skip" };
  }
  const cfg = RepoConfigSchema.parse(parsed ?? {});
  return {
    thresholdPct: cfg.coverage.threshold_pct,
    onBelow: cfg.coverage.on_below_threshold,
  };
}
