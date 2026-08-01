# Intent Dossier: data-processing

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- Rimsky stays **substrate-agnostic**: data semantics (versioning, partitioning, materialization, schema, retention) live in ClaimProducer services that opt into the DataProcessing **mix-in protocol**; rimsky's role is orchestration only. Data motion is substrate-direct via the ClaimResult address; the protocol carries only control-plane methods.
- The mix-in is seven control-plane methods: Capabilities, BeginCandidate, CommitCandidate, AbandonCandidate, ListVersions, ListPartitions, GetVersionSchema.
- Candidate lifecycle is pinned to claim-tree events: BeginCandidate at sub-claim acquisition (same transaction as the sub-claim row insert); CommitCandidate at leaf-run success terminal; AbandonCandidate (with GC) on failure paths — leaf failure, strict-cancel from a sibling, backfill abort.
- The `producer_candidate_handle` is inert bytes per the inertness discipline: lives on sub-claim rows only, stored and passed verbatim, never inspected by rimsky; it reaches the leaf executor inside the existing per-claim address structure of ExecuteRequest, not as a top-level field.
- Aggregation lives **inside the producer**: aggregator vocabulary (map_partitioned, union, merge, …) is producer-internal, declared in claim `data:` blocks, interpreted at Commit; rimsky's aggregation is state-only, and it is not a rimsky concept.
- `version_id` returned by Commit persists to the claim-handle row (base protocol and mix-in alike) and feeds lineage and the asset surface.

## Required behaviors (open promises)

- For a DataProcessing-advertising producer referenced from a fan-out node: rimsky calls BeginCandidate per sub-partition, CommitCandidate on leaf success (candidate metadata surfacing in the parent writeback), AbandonCandidate with GC on leaf failure; ListVersions/ListPartitions/GetVersionSchema expose real version/partition/schema state (2026-05-15, data-platform-extensions + 2026-06-08, corpus-bootstrap, artifact).
- The supervisor dials DataProcessing producer clients at startup (DataProcessors registry threaded through runtime config, run args, and callback server) so candidates are actually minted in a real stack (2026-06-02, rimsky-core-remediation, artifact — restoration of a promised capability).
- Dispatch builders set `StoreHandle.CandidateHandle` so the candidate handle reaches the fan-out leaf (2026-06-02, artifact, same restoration).
- The base-protocol Commit response body is real: version_id persisted to the handle row from the base Commit response (not only the mix-in path), producer_metadata surfaced in the fan-out parent's writeback (2026-06-11, last-mile-stability, artifact).
- Fan-out child attributes never aggregate onto the parent; the sanctioned downstream channels are producer_metadata, the producer-internal aggregator, and the asset surface keyed by version_id (2026-06-19 + 2026-06-22, 08d65bfe/10cf843b, transcript): operator-threading of small per-partition values is "future feature work and not gaps."
- The computed-scope pattern is supported: a producer's Open computes a requested view and returns it as payload bytes; templates hoist via `{{claim.<alias>.payload.<field>}}`; address bytes may carry a producer-private data-plane endpoint plus a one-time session token (2026-05-19, crimefinder, artifact).
- A data-processing conformance surface exists, self-tested against the stub-store extension (2026-05-15, artifact).
- examples/ ships a minimal data-processing gRPC server with in-process behavioral tests, completing the one-per-protocol promise for all six consumer-implementable protocols (2026-06-06, comprehensive-gap-closure, artifact).
- ~~Backfill honors partition-selector overrides through the real dispatch and cancel paths (2026-06-08, corpus-bootstrap, artifact-only).~~ STRUCK 2026-07-20, ledger 2497: backfill was retired as a first-class primitive (2026-06-14, bfc9febb, transcript — a backfill is now a message carrying a partition override read via substitution; see message dossier). Verified zero backfill runtime code in the tree (routes, handlers, CLI). The dispatch-honesty principle this promise gestured at survives inherently via message-refire (a re-fire IS a real producing dispatch); see the supervisor dossier.

## Intentional absences

- **Bundled reference stores (parquet-store, geo-parquet-store, geo-postgis-store)** — Section H CUT in full mid-execution at user direction, explicitly not deferred, with instruction that no follow-up dispatch revive it: rimsky is project-agnostic; a reference store worth shipping is major engineering and a naive one misleads users who copy it (2026-05-15, plan notes).
- **A shared rimsky-enforced aggregator vocabulary** — retired as a rimsky concept; producer-internal only (2026-05-15).
- **Rimsky inspecting candidate-handle bytes** — forbidden by the inertness discipline (2026-05-15).
- **Object-store enumeration (S3/GCS bundled store)** — deferred past v1 (flagged v1.1); when it lands it is a new bundled store using the existing expand-folder partition shape, not a new partition_request shape (2026-06-18, 9fb55f08, transcript).
- **Asset-materialize trigger endpoint** — retired 2026-06-15; re-materialization is messages-only (see asset dossier).
- **Host-agent-proxy DataProcessing fronting** — ships as a registered gRPC service returning UNIMPLEMENTED until a follow-up spec wires it; this is declared v1 scope, not drift (2026-05-24, host-agent-and-proxy, artifact).

## Corrections and restorations (drift-fight record)

- **No candidate ever minted in a real stack** (2026-06-02, rimsky-core-remediation): CandidateHandle was persisted and proto-defined but never set by dispatch builders, and the supervisor never dialed DataProcessing clients at all. Ruled: promised capability missing; restored end-to-end (registry, dialing at startup, per-sub-claim BeginCandidate, terminal Commit/AbandonCandidate).
- **Base Commit response discarded** (2026-06-11, last-mile-stability): proto-documented fields (version_id, producer_metadata) were dropped on the floor outside the mix-in path; made real in the unified resolution engine.
- **Versions endpoint stub** (2026-05-15, plan notes, artifact-only): GET …/assets/{alias}/versions landed 501 pending the gRPC client wiring, with no dispatch entry recording its replacement — a candidate promised-capability gap for adjudicators to verify rather than assume either way.

## Superseded / historical

- Spec deliverable of three bundled reference stores → cut (2026-05-15); DataProcessing self-test moved to the stub-store extension.
- version_id persistence as a mix-in-only behavior → base-protocol behavior (2026-06-11).
