import fs from "node:fs/promises";
import path from "node:path";
import { StateHandlerDeps, UnauthenticatedError } from "./handler-deps.js";
import { materializeFindings } from "./materialize.js";

export interface GetReviewContextRequest {
  session_token: string;
  assigned_finding_ids: string; // comma-separated; empty = none
}

export interface GetReviewContextResponse {
  context_json: Uint8Array;
}

// Spec lines 436-477: ContextPayload is role-polymorphic. The producer
// builds the payload here and returns it as JSON bytes; the executor's
// review_context gate just unwraps and returns to Claude.

interface ConceptDocPayload {
  slug: string;
  path: string;
  content: string;
}

const FINDING_CATEGORIES_HELP = [
  "Crimefinder uses a 5-class finding scheme:",
  "  class 1 — correctness: code does the wrong thing.",
  "  class 2 — security: code admits an attacker action.",
  "  class 3 — performance: code is correct but wasteful.",
  "  class 4 — clarity/maintainability: code is correct but hard to follow.",
  "  class 5a — architecture/design level: the design is the problem.",
  "  class 5b — design-doc may be wrong: the spec/doc itself is mistaken.",
].join("\n");

const DEFAULT_IGNORE = [
  "node_modules/",
  ".git/",
  "dist/",
  "build/",
  "coverage/",
  ".crimefinder/",
];

async function loadDocsFromDir(absDir: string, repoRoot: string): Promise<ConceptDocPayload[]> {
  const out: ConceptDocPayload[] = [];
  let entries: import("node:fs").Dirent[];
  try {
    entries = await fs.readdir(absDir, { withFileTypes: true });
  } catch {
    return out;
  }
  for (const ent of entries) {
    if (!ent.isFile()) continue;
    if (!ent.name.endsWith(".md")) continue;
    const full = path.join(absDir, ent.name);
    try {
      const content = await fs.readFile(full, "utf-8");
      out.push({
        slug: path.basename(ent.name, ".md"),
        path: path.relative(repoRoot, full),
        content,
      });
    } catch {
      // skip
    }
  }
  return out;
}

export async function handleGetReviewContext(
  req: GetReviewContextRequest,
  deps: StateHandlerDeps,
): Promise<GetReviewContextResponse> {
  const meta = deps.tokens.validate(req.session_token);
  if (!meta) throw new UnauthenticatedError();
  const designDocs = deps.config.design_docs;
  const ignorePatterns = [
    ...DEFAULT_IGNORE,
    ...deps.config.partitioning.additional_ignore_patterns.map((p) =>
      p.endsWith("/") ? p : `${p}/`,
    ),
  ];

  // Role is set on session-token issue (via fan-out child Open or the
  // parent scope handler). A token without a role should never reach this
  // gate — review_context is only called by dispatched agents, all of which
  // get role-tagged tokens. Fail loudly instead of silently returning the
  // wrong payload shape.
  if (!meta.role) {
    throw new Error("session token has no role; cannot select ContextPayload shape");
  }
  const role = meta.role;
  const passId = meta.passId;
  const zoneId = meta.zoneId;

  // Concept docs + open tensions are shared across roles.
  let conceptDocs: ConceptDocPayload[] = [];
  let openTensions: ConceptDocPayload[] = [];
  if (designDocs) {
    conceptDocs = await loadDocsFromDir(
      path.resolve(deps.repoRoot, designDocs.concepts_dir),
      deps.repoRoot,
    );
    const tensionsRoot = path.resolve(deps.repoRoot, designDocs.tensions_dir);
    openTensions = await loadDocsFromDir(tensionsRoot, deps.repoRoot);
  }

  const zones = deps.partitionCache.getZonePlan(passId) ?? [];
  const zone = zones.find((z) => z.id === zoneId);

  if (role === "review-zone") {
    const findingsRows = await deps.store.readFindings();
    const materialized = materializeFindings(findingsRows);
    const existing: Array<Record<string, unknown>> = [];
    for (const { row, status } of materialized.values()) {
      if (row.pass_id !== passId) continue;
      if (zoneId && row.zone_id !== zoneId) continue;
      existing.push({
        id: row.id,
        file: row.file,
        class: row.effective_class,
        status,
        description_summary: row.description.slice(0, 200),
      });
    }
    const payload = {
      pass_id: passId,
      zone_id: zoneId ?? null,
      zone_label: zone?.label ?? null,
      zone_files: zone?.files ?? [],
      mission: "convergence pass",
      concept_docs: conceptDocs,
      open_tensions: openTensions,
      existing_findings_in_zone: existing,
      finding_categories_help: FINDING_CATEGORIES_HELP,
      ignore_patterns: ignorePatterns,
    };
    return { context_json: new TextEncoder().encode(JSON.stringify(payload)) };
  }

  if (role === "fix-cycle") {
    const wantedIds = new Set(
      (req.assigned_finding_ids ?? "")
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
    );
    // Fix-cycle requires an explicit, non-empty assigned_finding_ids list.
    // splitAffected threads per-zone IDs onto each child's address; an
    // empty list means the producer told the executor "this zone has
    // nothing to fix" (e.g. all unresolved findings were already 5a/5b,
    // fixed, or deferred). Falling back to "every finding in this zone"
    // would mislead the agent into seeing 5a/5b/fixed/void rows it
    // cannot or should not act on.
    const assigned: Array<Record<string, unknown>> = [];
    if (wantedIds.size > 0) {
      const findingsRows = await deps.store.readFindings();
      const materialized = materializeFindings(findingsRows);
      for (const { row } of materialized.values()) {
        if (row.pass_id !== passId) continue;
        if (!wantedIds.has(row.id)) continue;
        assigned.push({
          id: row.id,
          file: row.file,
          line_start: row.line_start,
          line_end: row.line_end,
          description: row.description,
          concept_slug: row.concept_slug,
          tension_slug: row.tension_slug,
          prior_fix_attempts: [],
        });
      }
    }
    const payload = {
      pass_id: passId,
      zone_id: zoneId ?? null,
      zone_label: zone?.label ?? null,
      assigned_findings: assigned,
      test_command: deps.config.tests?.command ?? "",
      require_tests_before_commit: deps.config.require_tests_before_commit,
      concept_docs: conceptDocs,
      open_tensions: openTensions,
    };
    return { context_json: new TextEncoder().encode(JSON.stringify(payload)) };
  }

  if (role === "dedup") {
    // The shared ReviewContextOutputSchema requires `file_groups` on the
    // dedup payload — the agent has no other way to learn which finding
    // IDs it owns in this batch. The session token carries `batchIndex`
    // (set when the dedup-batch child Open issues the token); we look the
    // batch up in the partition cache and project to {file, finding_ids}.
    const batches = deps.partitionCache.getDedupBatches(passId) ?? [];
    const batch =
      typeof meta.batchIndex === "number" ? batches[meta.batchIndex] ?? [] : [];
    const fileGroups = batch.map((g) => ({
      file: g.file,
      finding_ids: g.findingIds ?? [],
    }));
    const payload = {
      pass_id: passId,
      role: "dedup",
      file_groups: fileGroups,
      concept_docs: conceptDocs,
      open_tensions: openTensions,
    };
    return { context_json: new TextEncoder().encode(JSON.stringify(payload)) };
  }

  // re-review
  const payload = {
    pass_id: passId,
    zone_id: zoneId ?? null,
    zone_label: zone?.label ?? null,
    zone_files: zone?.files ?? [],
    role: "re-review",
    concept_docs: conceptDocs,
    open_tensions: openTensions,
  };
  return { context_json: new TextEncoder().encode(JSON.stringify(payload)) };
}
