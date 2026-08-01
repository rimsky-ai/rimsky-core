# Intent Dossier: asset

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- **"Trace lineage to consumers" is not a rimsky capability** (user ruling 2026-07-17): rimsky has no first-class notion of an asset's consumers — external substrate readers are invisible, and cross-system consumer tracking belongs to the OpenLineage export's ecosystem. The asset-management story's capability is the lineage trace rimsky actually delivers (the forward run-level walk: which runs read the materializing runs' outputs); consumer-impact reasoning moved to the story's "so that" side as motivation. Story rewritten with a lineage acceptance clause and falsifier that were previously missing.

- An asset is a **documented compound, not a new primitive**: a claim against a DataProcessing-capable producer with `lifetime: durable`. The presentation surface is a query alias — `state='committed' AND lifetime='durable'` joined to the instance — never its own table (the separate `rimsky_assets` table, Option D, was articulated and rejected 2026-05-17).
- Canonical asset identity is `{instance_id}.{asset_alias}`, dotted `{node_alias}.{claim_alias}` form in URLs.
- Committed-durable rows are the asset surface: retention-exempt, still tripping scope conflict at acquire time, released only via producer Release — instance termination or operator DELETE.
- **The asset-materialize endpoint is gone** (retired 2026-06-15/17, transcript): re-materialization is expressed only through messages (the empty root-wake or an author-designed typed message). Asset list / detail / versions / materialization-history / delete surfaces stay.
- Fan-out outputs reach downstream through exactly three sanctioned channels — producer `producer_metadata`, the data-processing aggregator mix-in, and the asset surface keyed by the handle's `version_id` — never by aggregating child attributes onto the parent.

## Required behaviors (open promises)

- DELETE /instances/{id}/assets/{alias} calls `ClaimProducer.Release`; the producer GCs the durable data per its own policy; the request refuses with 409 while any in-flight run holds the claim (2026-05-15, data-platform-extensions, artifact).
- The retention sweep never deletes durable-committed rows; a committed-durable row keeps tripping scope conflict at acquire time even after the acquirer terminated — pinned by a dedicated regression test (2026-05-17, post-data-platform-cleanup, artifact).
- The asset query is `WHERE state='committed' AND lifetime='durable' AND instance_id=?`; the control-api asset JSON envelope surfaces explicit `state` and `lifetime` string fields (2026-05-17, artifact).
- Release-path deletion is deliberately not claimant-guarded: post-Promote `holder_supervisor_id` is NULL, so both ReleaseHeldDurableClaims and the asset DELETE handler route through an absence-guarded DeleteResolved (`WHERE id AND state IN ('committed','abandoned') AND holder_supervisor_id IS NULL`) (2026-05-17, notes, artifact).
- Operators can list an instance's assets with current versions, walk version history and the materialization audit, delete, and trace lineage to consumers (2026-06-08, corpus-bootstrap, artifact; note the *trigger re-materialization* limb of that story was retired 2026-06-15 — see intentional absences).
- ~~Backfill on an asset honors a partition-selector override (supervisor materializes runs against the override, not the template default), reports truthful per-partition progress, and cancels mid-flight through the real supervisor cancel path (2026-06-08, corpus-bootstrap, artifact-only).~~ STRUCK 2026-07-20, ledger 2532: backfill was retired as a first-class primitive (2026-06-14, bfc9febb, transcript — a backfill is now a message carrying a partition override read via substitution; see message dossier and `_retired` items). Verified: no backfill runtime code exists anywhere in the tree (grep hits only migration column comments, a conformance attribute-override helper unrelated to this promise, and `cli/roles/operator.json`).
- CLI asset surface: `asset list/show/versions/delete` and `asset lineage <id>:<alias> --version <v>` (2026-05-15, artifact; `asset materialize` retired 2026-06-17).
- Asset and lineage control-api handlers carry test coverage (backfilled 2026-06-02 after 4/6 asset and 5/8 lineage handlers were found untested), and the whole-system feature-trace pass is a standing check (2026-06-02, rimsky-core-remediation, artifact).
- ~~Dashboard is asset-primary: Assets top-nav, cross-instance asset list, per-asset detail with versions/materialization history/lineage (2026-05-15, artifact-only; Materialize action retired with the endpoint).~~ STRUCK 2026-07-20, ledger 2534: out of rimsky-core's scope, not a genuine loss. This repo ships no frontend/dashboard (see CLAUDE.md: "Public docs — not part of this repo"; no web UI sources exist in the tree) — the asset surface this promise depends on is served entirely by the guarded control-api (list/get/versions/materialization-history/delete), which is real and covered elsewhere in this dossier. A dashboard consuming it would live in a separate, out-of-repo project.

## Intentional absences

- **POST /instances/{id}/assets/{alias}/materialize** — retired entirely: handler, route, CLI subcommand, MCP tool schema, and action row deleted, verified by zero grep matches (2026-06-15, 4c42fe5b + 2026-06-17, b95ff4a7, transcript, user): "B is the correct path as it removes no capabilities and makes materialization fully explicit through messages." One-off invalidation of the producing node is a footgun — an asset from a full run might differ from one produced by an isolated re-run.
- **A separate `rimsky_assets` table** — rejected (2026-05-17); assets are committed-durable claim rows.
- **Parent-side aggregation of per-partition attributes into asset/parent state** — forbidden; the three sanctioned channels above (2026-06-22, 10cf843b, transcript; recorded as a fan-out invariant so future agents don't re-invent the merge).

## Corrections and restorations (drift-fight record)

- **Release-path delete mismatch** (2026-05-17): claimant-guarded Delete with an empty supervisor id failed to match NULL after Promote, breaking TestDurableLifetimeE2E; DeleteResolved landed early to fix it.
- **Untested asset/lineage handlers** (2026-06-02): wired-but-untested endpoints (including silently-failing JSONB reverse-lookups) backfilled with tests; feature-trace made standing.
- **Materialize retirement executed** (2026-06-17): the previously shipped operator surface was swept out completely per the signed-off 2026-06-15 spec — a finding that expects the endpoint is asserting drifted expectations.

## Superseded / historical

- Asset alias projection `{node_type}.{producer_name}` landed as a conscious approximation of the spec's `{template_node_alias}.{claim_alias}` (2026-05-15, artifact-only) — imprecise for multi-claim-per-producer templates; surfaced for review, never explicitly re-ruled.
- GET /instances/{id}/assets/{alias}/versions landed as a 501 stub pending DataProcessing gRPC client wiring, with no recorded replacement dispatch (2026-05-15, artifact-only) — the 2026-06-02 remediation later dialed DataProcessing clients at supervisor startup; whether the versions endpoint was completed is not settled by this record.
- "Trigger a re-materialization via the asset surface" (2026-06-08 story limb) → messages-only re-materialization (2026-06-15, transcript).
- Frame-creation shape for asset/materialize and node/reset → mooted for materialize by its retirement; the open question was deliberately deferred to the 2026-06-15 instance-creation-and-empty-message-trigger sketch (2026-06-15, 91ec93d1, transcript).
