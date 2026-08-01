# Intent Dossier: lineage

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- **Claim lineage walks are symmetric, and route names must tell the truth** (user ruling 2026-07-17): the claim-handle lineage surface offers both directions, like run lineage — a descendants walk following sub-claim ids downward, and a true ancestors walk following the claim-tree parent pointer upward. The shipped single claim route is named "ancestors" but walks sub-claims downward; it must be renamed to descendants and joined by a genuine ancestors walk (queued fix-code). The doc's backward/forward promise is ratified as intent.

- Lineage means attribute/data lineage: data flow via substitution_refs. Wake-only causality (a consumer woken without reading the upstream's attributes) is explicitly out of scope, documented as a boundary; a future causal lineage would be a parallel surface, not a polymorphic field on substitution_refs (2026-06-23, user).
- rimsky_lineage is an append-only projection with two record kinds: leaf_run (one per leaf-run terminal) and claim_terminal (renamed from claim_commit; outcome discriminator committed | abandoned | force_cancelled), plus a settling_signal_type field that replaced the retired last_outcome projection.
- Pass-through nodes (fan-out parents, pure-cascade nodes) cannot mutate their own attributes and therefore produce no lineage artifact by design. Sub-graph exit nodes are NOT pass-through: they are normal executor-bearing nodes that emit leaf_run records.
- Lineage retention is a flat trailing window (default 30d) and has been since day one; retention coupling to artifact sweeps was never implemented. The rebuild-from-events path was "deferred V1" intent, never a present-tense capability (both adjudicated fix-doc, 2026-07-13).
- The retention sweeps (run-tree, lineage) are wired into the scheduler tick with parsed config; retention is on by default.
- Two complementary forensics surfaces: rimsky_lineage answers "what data versions resulted" (content-identity-over-time); post-refactor rimsky_claim_handles answers "what claims existed and how did they resolve" — including "which supervisor terminated this claim", which lineage answers, not the claim-handle row.
- The bundled OpenLineage lifecycle-subscriber is poller-only: it polls rimsky_lineage with its own persisted cursor; the event-subscription transport was rejected for V1.

## Required behaviors (open promises)

- Lineage query surface: walk a run's lineage upstream to producers and downstream to consumers, query by claim handle, pivot by source or named producer, prune records strictly older than a cutoff (records at or after the cutoff untouched, deletion count surfaced); forward/reverse walks over control-api, depth-bounded (max 50) (2026-05-15, data-platform-extensions + 2026-06-08, corpus-bootstrap, artifact).
- leaf_run records carry substitution refs, held claims, executor/template/params hashes, trigger metadata; claim_terminal records carry version_id and the outcome discriminator (committed/abandoned/force_cancelled) making claim resolutions queryable from lineage (2026-05-15 + 2026-05-17, artifact).
- DataProcessing Commit returns version_id and rimsky records it in claim handles and lineage (2026-05-15, data-platform-extensions, artifact) `(artifact-only)`.
- Retention sweeps actually run: SweepRunTreeRetention and SweepLineageRetention wired into the scheduler tick, retention: config parsed and threaded, on by default (recent_frames_kept 100, lineage 30d), pointer-field loader (explicit 0 disables, absent key defaults, negative rejected) (2026-06-02, rimsky-core-remediation, artifact).
- lineage:prune dry-run returns a real exact would-prune count sharing one WHERE-clause constant with the DELETE so the two cannot drift; prune is synchronous (2026-05-29, console-upstream-auth-audit, artifact).
- Settling metadata: lineage records carry settling_signal_type (the settling signal's canonical type-path), replacing last_outcome (2026-05-23, signal-taxonomy, artifact).
- "Which supervisor terminated this claim" is answered by rimsky_lineage; the claim-handle Promote nulls holder_supervisor_id (2026-05-17, post-data-platform-cleanup, artifact).
- Wire-level terminal cause strings (natural, sibling_cancel, descendant_cancel) preserved via CauseString() so lineage-ledger consumers see no shape change across the TerminalOutcome flattening (2026-06-19, 08d65bfe, transcript).
- Sub-graph exit nodes emit leaf_run lineage records like any other executor-bearing node; only fan-out parents and pure-cascade nodes emit none (2026-06-23, 10cf843b, transcript).
- The lineage-exploration story (general operator lineage walking) stays, with its proof a plain producer-to-consumer substitution chain (2026-06-23, 10cf843b, transcript).
- OpenLineage subscriber: translates lifecycle events and claim terminal records into well-formed OpenLineage 1.x JSON (own minimal emitter, no external openlineage-go dependency), cursor advancing only past successfully emitted rows; template deploy → dataset-version event, run-scope terminal → job-run event (2026-05-15 + 2026-06-08, artifact) — transport is poller-only (see Intentional absences).
- Untested lineage control-api handlers backfilled with tests (JSONB reverse-lookups fail silently if subtly wrong) (2026-06-02, rimsky-core-remediation, artifact).

## Intentional absences

- Lineage rebuild-from-events as a present capability: always "deferred V1" intent; a corpus-bootstrap edit stripped the qualifier and wrongly converted it to a present-tense ownership claim. Adjudicated fix-doc, finding 449 (2026-07-13, 3f71f90a, transcript).
- Retention coupling (delete lineage rows whose artifacts were swept): never implemented; retention is flat trailing-window and always was. Adjudicated fix-doc, finding 448 (2026-07-13, transcript). This supersedes the 2026-05-15 plan language describing artifact-coupled deletion.
- Event-subscription transport for the OpenLineage subscriber: rejected for V1; poller-only is deliberate; the story's dataset-version RPC clause never had corresponding code. Adjudicated fix-doc, finding 1958 (2026-07-13, transcript).
- backfill_operation_id lineage column: dropped with the backfill primitive collapse — a backfill is just a message carrying a partition override; history/rollup come from general message observability (2026-06-14, bfc9febb, transcript).
- last_outcome column and its lineage projection: retired; settling_signal_type is strictly more expressive (2026-05-23, artifact).
- Lineage artifacts from pass-through nodes (fan-out parents, pure-cascade): none by design; a test asserting substitution-ref lineage from a fan-out parent was testing a non-feature and was deleted (2026-06-23, transcript).
- Wake-only causality in lineage: out of scope; operators consult the audit log or wait-set ledger (2026-06-23, user: "yes, data lineage for now").
- Column-level lineage: V2 deferral (2026-05-15, artifact).
- Lenient substitution markers ({{X?}}) on sources known never to resolve, as a test patch: rejected — fix the underlying design question instead (2026-06-22, 10cf843b, transcript, user).

## Corrections and restorations (drift-fight record)

- SweepLineageRetention and SweepRunTreeRetention were defined-but-unused at 2026-05-17 (contradicting the spec's assumed cadence), deliberately left unwired then; ruled dead code and fully wired with config plumbing 2026-06-02 (rimsky-core-remediation, artifact).
- The lineage-exploration proof conflated run-tree depth with substitution lineage by using a fan-out topology; the user rejected deleting the story as an orphan ("re-read the story. lineage wasn't specific to fan-out") and had the proof rewritten as a linear chain (2026-06-23, transcript).
- An earlier sketch note listed subgraph exits among pass-through non-emitters; corrected — only fan-out parents and pure-cascade nodes emit no leaf_run record (2026-06-23, transcript).
- Three 2026-07-13 adjudications (findings 448, 449, 1958) all ruled fix-doc: docs/stories had canonized aspirations (retention coupling, rebuild path, RPC transport) that were never code. Precedent: for lineage, artifact-tier capability claims without corroborating code default to doc drift.

## Superseded / historical

- rimsky_lineage described as "rebuildable from rimsky_events + claim-handle lifecycle" (2026-05-15, artifact) → rebuild was deferred-V1 intent only; present-tense claim is doc drift (2026-07-13).
- claim_commit record kind → claim_terminal with the three-valued outcome discriminator (2026-05-17).
- last_outcome projection → settling_signal_type (2026-05-23).
- Backfill as first-class concept with endpoints, CLI verbs, and lineage column (2026-05-15) → collapsed to a message convention (2026-06-14, transcript).
- Outcome × Cause product type → flat four-value TerminalOutcome enum, wire cause strings preserved for lineage consumers (2026-06-19, transcript).
- cycle-3 best-effort populateAcquisitionLineageFields lookup → retired under RunScope-first (run_scope_id non-null by schema) (2026-05-22, artifact).
