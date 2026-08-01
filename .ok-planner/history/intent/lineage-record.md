# Intent Dossier: lineage-record

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Lineage means **attribute (data) lineage** — data flow via substitution_refs — and stays strictly data-lineage for now (2026-06-23, transcript, user: "yes, data lineage for now").
- Lineage record kinds are **only `leaf_run` and `claim_terminal`** — no named-event kind (2026-06-17, transcript).
- Pass-through nodes cannot mutate their own attributes and therefore **produce no lineage artifact by design**; fan-out parents are pass-through (2026-06-23, transcript).
- `rimsky_lineage` is an append-only **materialized projection**, rebuildable from `rimsky_events` plus claim-handle lifecycle — never itself the source of truth (2026-05-15).
- Records carry `settling_signal_type` (the settling signal's canonical type-path), which replaced the retired last_outcome projection (2026-05-23).
- Lineage records must capture **frame-trigger fields**; the code self-declares the gap and the settled intent is capture — adjudicated fix-code (2026-07-13, transcript, finding 442).

## Required behaviors (open promises)

- Rebuildable projection: "Source of truth is `rimsky_events` (the audit log) + `rimsky_claim_handles` lifecycle. `rimsky_lineage` is a materialized projection rebuildable from those." Two record kinds — one per leaf-run terminal (substitution refs, held claims, executor/template/params hashes, trigger metadata) and one per claim-handle Commit (version_id, sub-claim manifest) (2026-05-15, data-platform-extensions, artifact; kind set confirmed as leaf_run + claim_terminal by 2026-06-17 transcript).
- Query surface: forward/reverse lineage walks over control-api, depth-bounded (max 50) (2026-05-15, data-platform-extensions, artifact) (artifact-only for the exact bound).
- Operator capabilities: walk a run's lineage upstream to producers and downstream to consumers, query by claim handle, pivot by source or named producer, and prune records strictly older than a cutoff — "only records strictly older than the cutoff are removed, records at or after the cutoff are untouched", deletion count surfaced (2026-06-08, corpus-bootstrap, artifact).
- `settling_signal_type` field carries the settling signal's canonical type-path (2026-05-23, signal-taxonomy-and-policy-decoupling, artifact).
- Frame-trigger fields plumbed into lineage records — the "NOT YET plumbed" self-declared gap is a fix-code ruling, not an accepted absence (2026-07-13, 3f71f90a, transcript, assistant-ratified; finding 442).

## Intentional absences

- **last_outcome column and concept** — retired entirely (schema migration drops the column); the granularity moved into signal payload fields (`changed` on terminal/success, `discarded_claims` on transient/retry) and into `settling_signal_type`, "strictly more expressive" (2026-05-23, signal-taxonomy-and-policy-decoupling, reversal).
- **Named-event lineage record kind** — does not exist; kinds are only leaf_run and claim_terminal (2026-06-17, b31002b8, transcript).
- **Lineage artifacts from pass-through nodes (incl. fan-out parents)** — by design; a test asserting substitution-ref lineage from a fan-out parent was testing a non-feature and was deleted (2026-06-23, 10cf843b, transcript, user).
- **Wake-only causality** (a consumer woken by an upstream's settle without reading its attributes) — explicitly out of lineage's scope, documented as a boundary; operators consult the audit log or wait-set ledger. If causal lineage becomes a real need it must be a parallel surface, not a polymorphic field on substitution_refs (2026-06-23, 10cf843b, transcript, user).

## Corrections and restorations (drift-fight record)

- **Wrong N/A rulings from path-grounded hunting** (2026-06-17, b31002b8, transcript): tasks covering breakpoint matchers and lineage records were wrongly marked N/A because literal directory paths didn't exist; the user corrected that the functionality exists (lineage at `lib/runtime/lineage_*.go`) — "hunts must ground on symbols, not paths." Precedent for adjudication: absence of an expected path is not absence of the feature.
- **Fan-out-parent lineage test deleted** (2026-06-23, 10cf843b, transcript): user ruled fan-out parents are pass-through and have no lineage meaning; the test asserted a non-feature. Precedent: refute findings that expect lineage artifacts from pass-through nodes.
- **Frame-trigger fields gap adjudicated fix-code** (2026-07-13, 3f71f90a, transcript, finding 442): the code's self-declared "NOT YET plumbed" marker means the settled intent is capture; the fields must be plumbed.

## Superseded / historical

- last_outcome as cascade gate and lineage projection → settling_signal_type + signal-payload fields (2026-05-23).
- The 2026-05-15 `claim_commit` record-kind name → the kind pair is stated as leaf_run + `claim_terminal` by the 2026-06-17 transcript (transcript outranks).
