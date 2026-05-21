import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import pino from "pino";
import { JsonlStore } from "./jsonl-store.js";

const logger = pino({ level: "silent" });
const NOW = "2026-05-19T12:00:00.000+00:00";

async function tmpdir(): Promise<string> {
  return fs.mkdtemp(path.join(os.tmpdir(), "cf-jsonl-"));
}

describe("JsonlStore", () => {
  let dir: string;
  let store: JsonlStore;

  beforeEach(async () => {
    dir = await tmpdir();
    store = new JsonlStore({ repoRoot: dir, logger });
  });

  it("appends and reads a finding", async () => {
    await store.appendFinding({
      kind: "finding",
      id: "f_x",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_1",
      session_id: "sess_1",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "src/foo.ts",
      line_start: null,
      line_end: null,
      description: "bug",
      fingerprint: "sha256:abc",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    const rows = await store.readFindings();
    expect(rows).toHaveLength(1);
    expect(rows[0].kind).toBe("finding");
  });

  it("serializes 50 concurrent appends with no corruption", async () => {
    const tasks = Array.from({ length: 50 }, (_, i) =>
      store.appendFinding({
        kind: "finding",
        id: `f_${i}`,
        ts: NOW,
        pass_id: "p_1",
        zone_id: "z_1",
        session_id: "sess_1",
        class: 1,
        effective_class: 1,
        auto_rerouted: false,
        file: "f.ts",
        line_start: null,
        line_end: null,
        description: `bug ${i}`,
        fingerprint: `sha256:${i}`,
        concept_slug: null,
        tension_slug: null,
        confidence: "high",
        status: "open",
        originating_zone_id: null,
      }),
    );
    await Promise.all(tasks);
    const rows = await store.readFindings();
    expect(rows).toHaveLength(50);
    const ids = new Set(rows.map((r) => (r as { id: string }).id));
    expect(ids.size).toBe(50);
  });

  it("readFindings includes status_update rows", async () => {
    await store.appendFinding({
      kind: "finding",
      id: "f_x",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_1",
      session_id: "sess_1",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "f.ts",
      line_start: null,
      line_end: null,
      description: "bug",
      fingerprint: "sha256:a",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await store.appendFinding({
      kind: "status_update",
      id: "su_1",
      ts: NOW,
      ref: "f_x",
      status: "fixed",
      by_pass: "p_1",
      by_session: "sess_1",
      resolved_at_commit: "abc",
    });
    const rows = await store.readFindings();
    const kinds = rows.map((r) => r.kind);
    expect(kinds).toContain("finding");
    expect(kinds).toContain("status_update");
  });

  it("skips malformed lines and keeps valid rows", async () => {
    await store.ensureDir();
    const filePath = path.join(dir, ".crimefinder", "findings.jsonl");
    const valid = JSON.stringify({
      kind: "finding",
      id: "f_x",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_1",
      session_id: "sess_1",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "f.ts",
      line_start: null,
      line_end: null,
      description: "bug",
      fingerprint: "sha256:a",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await fs.writeFile(filePath, valid + "\nthis-is-not-json\n", "utf-8");
    const rows = await store.readFindings();
    expect(rows).toHaveLength(1);
  });

  it("readFindings on missing file returns empty list", async () => {
    const rows = await store.readFindings();
    expect(rows).toEqual([]);
  });
});
