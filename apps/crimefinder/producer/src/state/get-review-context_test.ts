import { describe, it, expect, beforeEach } from "vitest";
import { makeStateDeps } from "./test-helpers.js";
import { handleGetReviewContext } from "./get-review-context.js";
import type { StateHandlerDeps } from "./handler-deps.js";

const NOW = "2026-05-19T12:00:00.000+00:00";

describe("handleGetReviewContext", () => {
  let deps: StateHandlerDeps;

  beforeEach(async () => {
    const r = await makeStateDeps();
    deps = r.deps;
    deps.partitionCache.setZonePlan("p_1", [
      { id: "z_a", label: "src/a", files: ["src/a/x.ts"] },
    ]);
  });

  it("fix-cycle with empty assigned_finding_ids returns empty assigned_findings", async () => {
    // splitAffected may emit `assigned_finding_ids: []` for a zone whose
    // bucket is empty (all unresolved findings are 5a/5b/fixed/deferred).
    // The state-client joins [] to "", so the handler must NOT fall back to
    // "every finding in this zone" — that would surface 5a/5b/fixed rows
    // the agent cannot or should not act on.
    //
    // Seed findings of every excluded shape in z_a to prove the fallback
    // is gone: a 5a, a 5b, a fixed class-1, a deferred class-2. If the
    // bug were still present, all four would leak into assigned_findings.
    await deps.store.appendFinding({
      kind: "finding",
      id: "f_5a",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_a",
      session_id: "s",
      class: "5a",
      effective_class: "5a",
      auto_rerouted: false,
      file: "src/a/x.ts",
      line_start: null,
      line_end: null,
      description: "5a",
      fingerprint: "sha256:5a",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await deps.store.appendFinding({
      kind: "finding",
      id: "f_5b",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_a",
      session_id: "s",
      class: "5b",
      effective_class: "5b",
      auto_rerouted: false,
      file: "src/a/x.ts",
      line_start: null,
      line_end: null,
      description: "5b",
      fingerprint: "sha256:5b",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await deps.store.appendFinding({
      kind: "finding",
      id: "f_fixed",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_a",
      session_id: "s",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "src/a/x.ts",
      line_start: null,
      line_end: null,
      description: "fixed",
      fingerprint: "sha256:fixed",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await deps.store.appendFinding({
      kind: "status_update",
      id: "su_fixed",
      ts: NOW,
      ref: "f_fixed",
      status: "fixed",
      by_pass: "p_1",
      by_session: "s",
      resolved_at_commit: "deadbeef",
    });
    await deps.store.appendFinding({
      kind: "finding",
      id: "f_def",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_a",
      session_id: "s",
      class: 2,
      effective_class: 2,
      auto_rerouted: false,
      file: "src/a/x.ts",
      line_start: null,
      line_end: null,
      description: "deferred",
      fingerprint: "sha256:def",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await deps.store.appendFinding({
      kind: "status_update",
      id: "su_def",
      ts: NOW,
      ref: "f_def",
      status: "deferred",
      by_pass: "p_1",
      by_session: "s",
    });

    const token = deps.tokens.issue({
      passId: "p_1",
      claimHandleId: "sess_fix",
      zoneId: "z_a",
      role: "fix-cycle",
      issuedAt: 0,
    });
    const r = await handleGetReviewContext(
      { session_token: token, assigned_finding_ids: "" },
      deps,
    );
    const payload = JSON.parse(new TextDecoder().decode(r.context_json));
    expect(payload.assigned_findings).toEqual([]);
  });

  it("fix-cycle returns only the explicitly-assigned findings", async () => {
    // Seed two unresolved class-1 findings; only one is in the IDs list.
    await deps.store.appendFinding({
      kind: "finding",
      id: "f_keep",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_a",
      session_id: "s",
      class: 1,
      effective_class: 1,
      auto_rerouted: false,
      file: "src/a/x.ts",
      line_start: null,
      line_end: null,
      description: "keep",
      fingerprint: "sha256:keep",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    await deps.store.appendFinding({
      kind: "finding",
      id: "f_drop",
      ts: NOW,
      pass_id: "p_1",
      zone_id: "z_a",
      session_id: "s",
      class: 2,
      effective_class: 2,
      auto_rerouted: false,
      file: "src/a/x.ts",
      line_start: null,
      line_end: null,
      description: "drop",
      fingerprint: "sha256:drop",
      concept_slug: null,
      tension_slug: null,
      confidence: "high",
      status: "open",
      originating_zone_id: null,
    });
    const token = deps.tokens.issue({
      passId: "p_1",
      claimHandleId: "sess_fix",
      zoneId: "z_a",
      role: "fix-cycle",
      issuedAt: 0,
    });
    const r = await handleGetReviewContext(
      { session_token: token, assigned_finding_ids: "f_keep" },
      deps,
    );
    const payload = JSON.parse(new TextDecoder().decode(r.context_json));
    expect(payload.assigned_findings).toHaveLength(1);
    expect(payload.assigned_findings[0].id).toBe("f_keep");
  });
});
