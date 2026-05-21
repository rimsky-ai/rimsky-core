import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { readConfig } from "./config.js";

describe("readConfig", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-config-"));
  });

  it("returns defaults when file is missing", async () => {
    const cfg = await readConfig(dir);
    expect(cfg.coverage.threshold_pct).toBe(80);
    expect(cfg.partitioning.max_files_per_zone).toBe(50);
    expect(cfg.allowed_tools).toContain("Read");
    expect(cfg.tests).toBeUndefined();
    expect(cfg.require_tests_before_commit).toBe(false);
  });

  it("merges with defaults when a minimal file is present", async () => {
    await fs.mkdir(path.join(dir, ".crimefinder"), { recursive: true });
    await fs.writeFile(
      path.join(dir, ".crimefinder", "config.yml"),
      "tests:\n  command: 'go test ./...'\n",
    );
    const cfg = await readConfig(dir);
    expect(cfg.tests?.command).toBe("go test ./...");
    expect(cfg.tests?.timeout_seconds).toBe(600);
  });

  it("throws on malformed YAML", async () => {
    await fs.mkdir(path.join(dir, ".crimefinder"), { recursive: true });
    await fs.writeFile(path.join(dir, ".crimefinder", "config.yml"), "key: [unclosed");
    await expect(readConfig(dir)).rejects.toThrow();
  });

  it("throws on schema violation", async () => {
    await fs.mkdir(path.join(dir, ".crimefinder"), { recursive: true });
    await fs.writeFile(
      path.join(dir, ".crimefinder", "config.yml"),
      "coverage:\n  threshold_pct: 200\n",
    );
    await expect(readConfig(dir)).rejects.toThrow();
  });
});
