/**
 * @source: prototype-repo:src/features/zones/partition_test.ts
 * @diverged: true
 * @reason: ID generator moved to @crimefinder/shared; assignZonesToSessions is not lifted.
 */

import { describe, it, expect, beforeEach, afterEach } from "vitest";
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import { partitionIntoZones } from "./partition.js";

describe("partitionIntoZones", () => {
  let tmpDir: string;

  beforeEach(() => {
    tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "crimefinder-partition-"));
  });
  afterEach(() => {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  });

  function createFile(relativePath: string): void {
    const full = path.join(tmpDir, relativePath);
    fs.mkdirSync(path.dirname(full), { recursive: true });
    fs.writeFileSync(full, "");
  }

  it("creates one zone for a small directory", () => {
    createFile("src/a.ts");
    createFile("src/b.ts");
    createFile("src/c.ts");
    const zones = partitionIntoZones({ projectRoot: tmpDir });
    expect(zones.length).toBe(1);
    expect(zones[0].files).toHaveLength(3);
  });

  it("splits large directories", () => {
    for (let i = 0; i < 120; i++) {
      createFile(`src/file${i.toString().padStart(3, "0")}.ts`);
    }
    const zones = partitionIntoZones({ projectRoot: tmpDir, maxFilesPerZone: 50 });
    expect(zones.length).toBeGreaterThan(1);
    expect(zones[0].label).toMatch(/\(\d+\/\d+\)/);
    const total = zones.flatMap((z) => z.files).length;
    expect(total).toBe(120);
  });

  it("merges tiny sibling directories", () => {
    createFile("src/utils/a.ts");
    createFile("src/utils/b.ts");
    createFile("src/helpers/c.ts");
    createFile("src/helpers/d.ts");
    const zones = partitionIntoZones({ projectRoot: tmpDir });
    expect(zones.length).toBe(1);
    expect(zones[0].files).toHaveLength(4);
  });

  it("filters default ignore patterns", () => {
    createFile("src/a.ts");
    createFile("node_modules/pkg/index.js");
    createFile(".git/config");
    createFile("dist/out.js");
    const zones = partitionIntoZones({ projectRoot: tmpDir });
    const files = zones.flatMap((z) => z.files);
    expect(files).toEqual(["src/a.ts"]);
  });

  it("returns no zones for an empty tree", () => {
    fs.mkdirSync(path.join(tmpDir, "empty"), { recursive: true });
    const zones = partitionIntoZones({ projectRoot: tmpDir });
    expect(zones).toHaveLength(0);
  });

  it("supports config-supplied ignore patterns", () => {
    createFile("vendor/x.js");
    createFile("src/a.ts");
    const zones = partitionIntoZones({
      projectRoot: tmpDir,
      ignorePatterns: ["vendor", "node_modules", ".git", "dist", ".crimefinder", "build", "coverage"],
    });
    const files = zones.flatMap((z) => z.files);
    expect(files).toEqual(["src/a.ts"]);
  });

  it("zone IDs are deterministic for the same label", () => {
    createFile("src/a.ts");
    const a = partitionIntoZones({ projectRoot: tmpDir })[0];
    const b = partitionIntoZones({ projectRoot: tmpDir })[0];
    expect(a.id).toBe(b.id);
    expect(a.id).toMatch(/^z_/);
  });
});
