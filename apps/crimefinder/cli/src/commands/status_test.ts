import { describe, it, expect } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { runStatus } from "./status.js";
import { RimskyCli } from "../rimsky-cli.js";

// Inject a RimskyCli that returns [] for instanceList so the test
// doesn't shell out to the real `rimsky` binary.
function stubCli(): RimskyCli {
  return new RimskyCli({
    exec: async () => ({ stdout: "[]", stderr: "" }),
  });
}

describe("runStatus", () => {
  it("runs against an empty repo without throwing", async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-cli-status-"));
    const code = await runStatus(["--repo", dir], { cli: stubCli() });
    expect(code).toBe(0);
  });

  it("counts findings by class", async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-cli-status-"));
    await fs.mkdir(path.join(dir, ".crimefinder"), { recursive: true });
    const row = JSON.stringify({
      kind: "finding",
      id: "f_1",
      ts: "2026-05-19T12:00:00.000+00:00",
      pass_id: "p_1",
      zone_id: "z_1",
      session_id: "s",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "f.ts",
      line_start: null,
      line_end: null,
      description: "x",
      fingerprint: "sha256:x",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await fs.writeFile(path.join(dir, ".crimefinder", "findings.jsonl"), row + "\n");
    expect(await runStatus(["--repo", dir], { cli: stubCli() })).toBe(0);
  });
});
