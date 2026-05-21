import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { readConcept } from "./parser.js";

describe("readConcept", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-concept-"));
  });

  async function write(content: string): Promise<string> {
    const p = path.join(dir, "claim-handle.md");
    await fs.writeFile(p, content, "utf-8");
    return p;
  }

  it("extracts Boundaries and Invariants sections", async () => {
    const p = await write(
      [
        "# Claim handle",
        "",
        "Some intro.",
        "",
        "## Boundaries",
        "The claim handle does not perform IO.",
        "",
        "## Invariants",
        "Each handle has exactly one holder.",
        "",
      ].join("\n"),
    );
    const doc = await readConcept(p);
    expect(doc.slug).toBe("claim-handle");
    expect(doc.boundaries).toContain("does not perform IO");
    expect(doc.invariants).toContain("exactly one holder");
  });

  it("returns empty strings for missing sections", async () => {
    const p = await write("# foo\n\nNo headings here.");
    const doc = await readConcept(p);
    expect(doc.boundaries).toBe("");
    expect(doc.invariants).toBe("");
  });

  it("stops the section at the next ## heading", async () => {
    const p = await write(
      ["## Boundaries", "bounded text", "", "## Other", "this is not boundaries"].join("\n"),
    );
    const doc = await readConcept(p);
    expect(doc.boundaries).toBe("bounded text");
  });
});
