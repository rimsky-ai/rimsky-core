import fs from "node:fs/promises";
import path from "node:path";

export interface ConceptAnnotation {
  file: string;
  line: number;
  slug: string;
}

export interface ScanOptions {
  repoRoot: string;
  marker?: string;
  ignorePatterns?: string[];
}

const DEFAULT_IGNORE = ["node_modules", ".git", "dist", "build", ".crimefinder"];

// Cap per-file read at 1 MB — concept annotations live in source files,
// not in generated bundles or vendored blobs.
const MAX_FILE_BYTES = 1 * 1024 * 1024;

// Whitelist of extensions where @concept: annotations realistically live.
// Avoids reading every file in a tree (lock files, binaries, generated
// artifacts, vendored sources, etc.).
const SCANNABLE_EXTENSIONS = new Set([
  ".ts",
  ".tsx",
  ".js",
  ".jsx",
  ".mjs",
  ".cjs",
  ".go",
  ".py",
  ".rs",
  ".rb",
  ".java",
  ".kt",
  ".swift",
  ".c",
  ".cc",
  ".cpp",
  ".h",
  ".hh",
  ".hpp",
  ".cs",
  ".md",
  ".sh",
  ".sql",
  ".proto",
  ".yml",
  ".yaml",
  ".toml",
]);

function normalizePatterns(patterns: string[]): string[] {
  return patterns.map((p) => (p.endsWith("/") ? p.slice(0, -1) : p));
}

export async function scanConceptAnnotations(opts: ScanOptions): Promise<ConceptAnnotation[]> {
  const marker = opts.marker ?? "@concept:";
  const ignoreSet = new Set(normalizePatterns(opts.ignorePatterns ?? DEFAULT_IGNORE));
  const out: ConceptAnnotation[] = [];

  async function walk(dir: string): Promise<void> {
    let entries: import("node:fs").Dirent[];
    try {
      entries = await fs.readdir(dir, { withFileTypes: true });
    } catch {
      return;
    }
    for (const ent of entries) {
      if (ignoreSet.has(ent.name)) continue;
      const full = path.join(dir, ent.name);
      if (ent.isDirectory()) {
        await walk(full);
      } else if (ent.isFile()) {
        const ext = path.extname(ent.name).toLowerCase();
        if (!SCANNABLE_EXTENSIONS.has(ext)) continue;
        await scanFile(full);
      }
    }
  }

  async function scanFile(full: string): Promise<void> {
    try {
      const st = await fs.stat(full);
      if (st.size > MAX_FILE_BYTES) return;
    } catch {
      return;
    }
    let raw: string;
    try {
      raw = await fs.readFile(full, "utf-8");
    } catch {
      return;
    }
    const lines = raw.split("\n");
    for (let i = 0; i < lines.length; i++) {
      const idx = lines[i].indexOf(marker);
      if (idx === -1) continue;
      const after = lines[i].slice(idx + marker.length).trimStart();
      const slugMatch = /^[a-zA-Z0-9_\-/.]+/.exec(after);
      if (!slugMatch) continue;
      out.push({
        file: path.relative(opts.repoRoot, full),
        line: i + 1,
        slug: slugMatch[0],
      });
    }
  }

  await walk(opts.repoRoot);
  return out;
}
