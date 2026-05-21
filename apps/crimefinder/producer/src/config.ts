import fs from "node:fs/promises";
import path from "node:path";
import yaml from "yaml";
import { z } from "zod";

export const ConfigSchema = z
  .object({
    tests: z
      .object({
        command: z.string(),
        timeout_seconds: z.number().int().positive().default(600),
        cwd: z.string().default("."),
      })
      .optional(),
    require_tests_before_commit: z.boolean().default(false),
    coverage: z
      .object({
        threshold_pct: z.number().min(0).max(100).default(80),
        on_below_threshold: z.enum(["require_skip", "warn", "allow"]).default("require_skip"),
      })
      .default({}),
    partitioning: z
      .object({
        max_files_per_zone: z.number().int().positive().default(50),
        small_group_threshold: z.number().int().positive().default(10),
        additional_ignore_patterns: z.array(z.string()).default([]),
      })
      .default({}),
    allowed_tools: z
      .array(z.string())
      .default(["Read", "Glob", "Grep", "Edit", "Write", "mcp__crimefinder__review_*"]),
    design_docs: z
      .object({
        concepts_dir: z.string().default(".ok-planner/design/concepts"),
        tensions_dir: z.string().default(".ok-planner/design/tensions"),
        annotation_marker: z.string().default("@concept:"),
      })
      .optional(),
  })
  .default({});

export type CrimefinderConfig = z.infer<typeof ConfigSchema>;

export async function readConfig(repoRoot: string): Promise<CrimefinderConfig> {
  const filePath = path.join(repoRoot, ".crimefinder", "config.yml");
  let raw: string;
  try {
    raw = await fs.readFile(filePath, "utf-8");
  } catch (e) {
    if ((e as NodeJS.ErrnoException).code === "ENOENT") {
      return ConfigSchema.parse({});
    }
    throw e;
  }
  const parsed = yaml.parse(raw);
  return ConfigSchema.parse(parsed ?? {});
}
