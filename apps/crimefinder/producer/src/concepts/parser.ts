import fs from "node:fs/promises";
import path from "node:path";

export interface ConceptDoc {
  slug: string;
  filePath: string;
  content: string;
  boundaries: string;
  invariants: string;
}

const BOUNDARIES_HEADING = /^##\s+Boundaries\b/im;
const INVARIANTS_HEADING = /^##\s+Invariants\b/im;
const ANY_H2 = /^##\s+/m;

function extractSection(content: string, heading: RegExp): string {
  const m = heading.exec(content);
  if (!m) return "";
  const start = m.index + m[0].length;
  const rest = content.slice(start);
  // Find the next ## heading (any kind) after our section.
  const nextH2 = ANY_H2.exec(rest);
  const end = nextH2 ? nextH2.index : rest.length;
  return rest.slice(0, end).trim();
}

function slugFromPath(filePath: string): string {
  const base = path.basename(filePath, path.extname(filePath));
  return base;
}

export async function readConcept(filePath: string): Promise<ConceptDoc> {
  const content = await fs.readFile(filePath, "utf-8");
  return {
    slug: slugFromPath(filePath),
    filePath,
    content,
    boundaries: extractSection(content, BOUNDARIES_HEADING),
    invariants: extractSection(content, INVARIANTS_HEADING),
  };
}
