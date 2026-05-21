import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { scanConceptAnnotations } from "./scanner.js";

describe("scanConceptAnnotations", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-scan-"));
  });

  it("finds @concept: markers across files", async () => {
    await fs.mkdir(path.join(dir, "src"), { recursive: true });
    await fs.writeFile(
      path.join(dir, "src", "a.ts"),
      ["// nothing here", "/* @concept: claim-handle */", "// also nothing"].join("\n"),
    );
    await fs.writeFile(
      path.join(dir, "src", "b.ts"),
      ["// @concept: scope", "// @concept: scope"].join("\n"),
    );
    const out = await scanConceptAnnotations({ repoRoot: dir });
    expect(out).toEqual([
      { file: "src/a.ts", line: 2, slug: "claim-handle" },
      { file: "src/b.ts", line: 1, slug: "scope" },
      { file: "src/b.ts", line: 2, slug: "scope" },
    ]);
  });

  it("respects ignore patterns", async () => {
    await fs.mkdir(path.join(dir, "node_modules", "pkg"), { recursive: true });
    await fs.writeFile(path.join(dir, "node_modules", "pkg", "x.ts"), "// @concept: nope");
    const out = await scanConceptAnnotations({ repoRoot: dir });
    expect(out).toHaveLength(0);
  });
});
