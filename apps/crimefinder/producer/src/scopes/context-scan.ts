import fs from "node:fs/promises";
import path from "node:path";
import type { OpenContext, OpenResult } from "./types.js";
import { parseSelectorQuery } from "./types.js";
import { scanConceptAnnotations } from "../concepts/scanner.js";

export async function openContextScan(ctx: OpenContext): Promise<OpenResult> {
  const q = parseSelectorQuery(ctx.selector);
  const passId = q.pass_id;
  if (!passId) throw new Error("context-scan selector missing pass_id");

  const claudeMdPath = path.join(ctx.repoRoot, "CLAUDE.md");
  let claudeMdPresent = false;
  try {
    await fs.access(claudeMdPath);
    claudeMdPresent = true;
  } catch {
    claudeMdPresent = false;
  }

  const rulesDir = path.join(ctx.repoRoot, ".claude", "rules");
  let rulesFiles: string[] = [];
  try {
    const entries = await fs.readdir(rulesDir);
    rulesFiles = entries.map((f) => path.join(".claude/rules", f));
  } catch {
    rulesFiles = [];
  }

  const designDocs = ctx.config.design_docs;
  const conceptsDir = designDocs?.concepts_dir ?? ".ok-planner/design/concepts";
  const tensionsDir = designDocs?.tensions_dir ?? ".ok-planner/design/tensions";
  const marker = designDocs?.annotation_marker ?? "@concept:";

  async function listMarkdownInDir(dir: string): Promise<Array<{ slug: string; path: string }>> {
    const full = path.join(ctx.repoRoot, dir);
    try {
      const entries = await fs.readdir(full);
      return entries
        .filter((f) => f.endsWith(".md"))
        .map((f) => ({ slug: f.replace(/\.md$/, ""), path: path.join(dir, f) }));
    } catch {
      return [];
    }
  }

  const concepts = await listMarkdownInDir(conceptsDir);
  const tensions = await listMarkdownInDir(tensionsDir);
  const conceptAnnotations = await scanConceptAnnotations({
    repoRoot: ctx.repoRoot,
    marker,
  });

  const manifest = {
    claude_md_present: claudeMdPresent,
    rules_files: rulesFiles,
    concepts,
    tensions,
    concept_annotations: conceptAnnotations,
  };

  const payloadBytes = new TextEncoder().encode(JSON.stringify(manifest));
  const scopeBytes = new TextEncoder().encode(JSON.stringify({ kind: "context-scan", pass_id: passId }));
  return { address: new Uint8Array(), payload: payloadBytes, scope: scopeBytes };
}
