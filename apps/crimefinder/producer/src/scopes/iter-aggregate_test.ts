import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import pino from "pino";
import { JsonlStore } from "../jsonl-store.js";
import { SessionTokenRegistry } from "../state/session-tokens.js";
import { IterationCounter } from "../state/iteration-counter.js";
import { createGitOps } from "../git-ops.js";
import { ConfigSchema } from "../config.js";
import { openIterAggregate } from "./iter-aggregate.js";
import { createPartitionCache, OpenContext } from "./types.js";

const logger = pino({ level: "silent" });
const NOW = "2026-05-19T12:00:00.000+00:00";

describe("openIterAggregate", () => {
  let ctx: OpenContext;
  let store: JsonlStore;
  beforeEach(async () => {
    const dir = await fs.mkdtemp(path.join(os.tmpdir(), "cf-iagg-"));
    store = new JsonlStore({ repoRoot: dir, logger });
    ctx = {
      selector: "@iter-aggregate:pass_id=p_1&iter_num=1",
      claimId: "c_ia",
      repoRoot: dir,
      store,
      tokens: new SessionTokenRegistry(),
      iterCounter: new IterationCounter(store, logger),
      stateEndpointUrl: "url",
      partitionCache: createPartitionCache(),
      config: ConfigSchema.parse({}),
      git: createGitOps(),
      logger,
    };
  });

  it("more_work_needed:false when all class-1-4 are fixed", async () => {
    // iter_marker establishes the window-start for iter_num=1; the
    // status_update timestamp must fall inside [iter_marker_1, ...).
    await store.appendIterMarker({
      kind: "iter_marker",
      id: "im_1",
      ts: "2026-05-19T11:59:00.000+00:00",
      pass_id: "p_1",
      iter_num: 1,
    });
    await store.appendFinding({
      kind: "finding",
      id: "f_x",
      ts: NOW,
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
    await store.appendFinding({
      kind: "status_update",
      id: "su_1",
      ts: NOW,
      ref: "f_x",
      status: "fixed",
      by_pass: "p_1",
      by_session: "s",
      resolved_at_commit: "abc",
    });
    const r = await openIterAggregate(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.more_work_needed).toBe(false);
    expect(p.findings_resolved_this_iter).toBe(1);
  });

  it("findings_resolved_this_iter counts only fixes inside the iter's timestamp window", async () => {
    // Two iter markers: iter_1 at t=10:00, iter_2 at t=12:00. A fix at
    // t=11:30 belongs to iter_1; a fix at t=12:30 belongs to iter_2.
    await store.appendIterMarker({
      kind: "iter_marker",
      id: "im_1",
      ts: "2026-05-19T10:00:00.000+00:00",
      pass_id: "p_1",
      iter_num: 1,
    });
    await store.appendIterMarker({
      kind: "iter_marker",
      id: "im_2",
      ts: "2026-05-19T12:00:00.000+00:00",
      pass_id: "p_1",
      iter_num: 2,
    });
    for (const fid of ["f_a", "f_b"]) {
      await store.appendFinding({
        kind: "finding",
        id: fid,
        ts: "2026-05-19T10:00:00.000+00:00",
        pass_id: "p_1",
        zone_id: "z_1",
        session_id: "s",
        class: 1,
        effective_class: 1,
        auto_rerouted: false,
        file: `${fid}.ts`,
        line_start: null,
        line_end: null,
        description: "x",
        fingerprint: `sha256:${fid}`,
        concept_slug: null,
        tension_slug: null,
        confidence: "high",
        status: "open",
        originating_zone_id: null,
      });
    }
    await store.appendFinding({
      kind: "status_update",
      id: "su_a",
      ts: "2026-05-19T11:30:00.000+00:00",
      ref: "f_a",
      status: "fixed",
      by_pass: "p_1",
      by_session: "s",
      resolved_at_commit: "aaa",
    });
    await store.appendFinding({
      kind: "status_update",
      id: "su_b",
      ts: "2026-05-19T12:30:00.000+00:00",
      ref: "f_b",
      status: "fixed",
      by_pass: "p_1",
      by_session: "s",
      resolved_at_commit: "bbb",
    });
    const r = await openIterAggregate(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.findings_resolved_this_iter).toBe(1);
  });

  it("more_work_needed:true when an unresolved finding remains", async () => {
    await store.appendFinding({
      kind: "finding",
      id: "f_y",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_1",
      session_id: "s",
      class: 2,
      effective_class: 2,
      auto_rerouted: false,
      file: "g.ts",
      line_start: null,
      line_end: null,
      description: "y",
      fingerprint: "sha256:y",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    const r = await openIterAggregate(ctx);
    const p = JSON.parse(new TextDecoder().decode(r.payload));
    expect(p.more_work_needed).toBe(true);
  });
});
