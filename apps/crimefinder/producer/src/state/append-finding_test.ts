import { describe, it, expect, beforeEach } from "vitest";
import fs from "node:fs/promises";
import path from "node:path";
import { makeStateDeps } from "./test-helpers.js";
import { handleAppendFinding } from "./append-finding.js";
import type { StateHandlerDeps } from "./handler-deps.js";

describe("handleAppendFinding", () => {
  let dir: string;
  let deps: StateHandlerDeps;
  let token: string;

  beforeEach(async () => {
    const r = await makeStateDeps();
    dir = r.dir;
    deps = r.deps;
    token = deps.tokens.issue({
      passId: "p_1",
      claimHandleId: "sess_1",
      zoneId: "z_1",
      role: "review-zone",
      issuedAt: 0,
    });
  });

  it("appends a basic class-1 finding", async () => {
    const r = await handleAppendFinding(
      {
        session_token: token,
        class: "1",
        file: "src/foo.ts",
        line_start: 0,
        line_start_present: false,
        line_end: 0,
        line_end_present: false,
        description: "bad",
        confidence: "high",
      },
      deps,
    );
    expect(r.finding_id).toMatch(/^f_/);
    expect(r.effective_class).toBe("1");
    expect(r.auto_rerouted).toBe(false);
  });

  it("rejects an unknown session token", async () => {
    await expect(
      handleAppendFinding(
        {
          session_token: "nope",
          class: "1",
          file: "x.ts",
          line_start: 0,
          line_start_present: false,
          line_end: 0,
          line_end_present: false,
          description: "x",
          confidence: "high",
        },
        deps,
      ),
    ).rejects.toThrow(/invalid session_token/);
  });

  it("dedups same-fingerprint findings within the same pass", async () => {
    const args = {
      session_token: token,
      class: "1",
      file: "src/foo.ts",
      line_start: 0,
      line_start_present: false,
      line_end: 0,
      line_end_present: false,
      description: "missing return",
      confidence: "high",
    };
    const a = await handleAppendFinding(args, deps);
    const b = await handleAppendFinding(args, deps);
    expect(a.finding_id).toBe(b.finding_id);
  });

  it("auto-routes to class-5b when concept boundaries are not quoted", async () => {
    await fs.mkdir(path.join(dir, ".ok-planner/design/concepts"), { recursive: true });
    await fs.writeFile(
      path.join(dir, ".ok-planner/design/concepts/example.md"),
      "## Boundaries\nthe claim handle does not perform side effecting operations of any kind ever today.\n",
    );
    deps.config = {
      ...deps.config,
      design_docs: {
        concepts_dir: ".ok-planner/design/concepts",
        tensions_dir: ".ok-planner/design/tensions",
        annotation_marker: "@concept:",
      },
    };
    const r = await handleAppendFinding(
      {
        session_token: token,
        class: "1",
        file: "src/foo.ts",
        line_start: 0,
        line_start_present: false,
        line_end: 0,
        line_end_present: false,
        description: "the concept is wrong here in ways that don't quote anything",
        concept_slug: "example",
        confidence: "high",
      },
      deps,
    );
    expect(r.effective_class).toBe("5b");
    expect(r.auto_rerouted).toBe(true);
  });

  it("does NOT auto-route when the description quotes 8+ boundary tokens", async () => {
    await fs.mkdir(path.join(dir, ".ok-planner/design/concepts"), { recursive: true });
    await fs.writeFile(
      path.join(dir, ".ok-planner/design/concepts/example.md"),
      "## Boundaries\nclaim handle does perform side effecting filesystem operations heavily during commit transactions today.\n",
    );
    deps.config = {
      ...deps.config,
      design_docs: {
        concepts_dir: ".ok-planner/design/concepts",
        tensions_dir: ".ok-planner/design/tensions",
        annotation_marker: "@concept:",
      },
    };
    const r = await handleAppendFinding(
      {
        session_token: token,
        class: "1",
        file: "src/foo.ts",
        line_start: 0,
        line_start_present: false,
        line_end: 0,
        line_end_present: false,
        description:
          "claim handle does perform side effecting filesystem operations heavily during commit transactions today even when it shouldn't",
        concept_slug: "example",
        confidence: "high",
      },
      deps,
    );
    expect(r.effective_class).toBe("1");
    expect(r.auto_rerouted).toBe(false);
  });

  it("routes to tension_confirmation when tension_slug is open", async () => {
    await fs.mkdir(path.join(dir, ".ok-planner/design/tensions"), { recursive: true });
    await fs.writeFile(path.join(dir, ".ok-planner/design/tensions/foo.md"), "# foo");
    deps.config = {
      ...deps.config,
      design_docs: {
        concepts_dir: ".ok-planner/design/concepts",
        tensions_dir: ".ok-planner/design/tensions",
        annotation_marker: "@concept:",
      },
    };
    const r = await handleAppendFinding(
      {
        session_token: token,
        class: "1",
        file: "src/foo.ts",
        line_start: 0,
        line_start_present: false,
        line_end: 0,
        line_end_present: false,
        description: "we see this all the time",
        tension_slug: "foo",
        confidence: "high",
      },
      deps,
    );
    expect(r.tension_confirmation).toBe(true);
    const rows = await deps.store.readFindings();
    expect(rows.some((row) => row.kind === "tension_confirmation")).toBe(true);
    expect(rows.some((row) => row.kind === "finding")).toBe(false);
  });
});
