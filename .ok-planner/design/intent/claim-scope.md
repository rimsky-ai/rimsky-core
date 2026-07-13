# Intent Dossier: claim-scope

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- The concept is named **ClaimScope** everywhere — schema `claim_scope_data`, lock_kind value `claim_scope`, Go `ClaimScopesByteEqual`, proto fields, substitution directive `{{claim.<alias>.claim_scope}}`. Bare "Scope" is a parking-lot word, never standalone; "region" is the pre-2026-05-04 vocabulary and must not appear (2026-05-22, fan-out-safety-scope-first).
- Conflict predicate: byte-equality is the default primitive, but overlap is **producer-defined** — a producer advertising `SupportsScopesConflict` supplies its own `ScopesConflict` predicate and rimsky must consult it at acquisition, including the fan-out sub-claim path (invariant 4b). Producers not advertising the capability get byte-equal comparison and are never asked (2026-05-15 onward).
- Write-semantics uniformity: all claim handles with the same (producer, scope-bytes) carry identical realized write-semantics; cross-semantics coexistence is deliberately undefined and must not be modeled, tested, or documented as a defined case (2026-05-04 artifact; ratified in transcript 2026-06-19).
- Committed-durable claim rows retain scope ownership and participate in conflict detection until released; committed non-durable rows do not.
- Scopes are recursively partitionable via the producer's optional `SplitScope`; the producer decides the partition, returns a flat list, and the fan-out node only iterates it.

## Required behaviors (open promises)

- Two claim handles with byte-equal (producer, scope-bytes) conflict; the foundation never parses, globs, or range-matches scope bytes — canonicalization is the producer's job (2026-05-04, foundation-contract, artifact): "Byte-equality is the *only* conflict primitive." (Default path only — see ScopesConflict below.)
- Handles with the same (producer, scope-bytes) must carry identical `realized_write_semantics`; the `Capabilities()` envelope constrains what `Open` may return; coexistence is evaluated at insert time only (2026-05-04, foundation-contract, artifact; reaffirmed 2026-06-19, a02fe167, transcript: cross-semantics coexistence is deliberately undefined).
- When a producer advertises `SupportsScopesConflict`, rimsky consults the producer's `ScopesConflict` during claim acquisition and in the fan-out sub-claim path, so byte-distinct-but-overlapping scopes cannot be co-held by two writers (2026-05-15, data-platform-extensions, artifact; ordered as a gap-closure fix 2026-06-06 after the predicate was found to have zero callers: "two writers whose scopes overlap (but are NOT byte-equal) cannot both hold the claim simultaneously").
- ScopesConflict governs concurrency: disjoint sub-claim scopes dispatch concurrently; overlapping scopes serialize (2026-05-19, crimefinder, artifact).
- Recursive scope partitioning: `SplitScope` returns sub-scope descriptors; rimsky opens one sub-claim per descriptor; sub-claims are claims and recursively partitionable; a parent claim auto-terminals only after all sub-claims are terminal, bottom-up (2026-05-15, data-platform-extensions, artifact). The producer decides what is in the partition — `partition_request` is producer-specific, the response is always just a list, and the fan-out node only iterates it (2026-06-18, 9fb55f08, transcript): "the producer has to device what is in the partition, and all the fan-out node does is iterate the list returned".
- A committed-durable claim-handle row continues to trip scope conflict at acquire time even after the original acquirer terminated — pinned by a dedicated regression test (2026-05-17, post-data-platform-cleanup, artifact). After release (asset Release endpoint or instance termination), a subsequent acquirer succeeds (2026-06-10, cascade-and-claim-handoff, artifact).
- Scope-ownership predicate (`ListByProducerScope`): `state = 'active' OR (state = 'committed' AND lifetime = 'durable')` — committed-subgraph rows must NOT block reacquisition of the same scope (2026-05-17, post-data-platform-cleanup, artifact, correction of the spec's `state IN ('active','committed')`).
- Conflict routing distinguishes persistent from in-flight conflicts: a conflict against a committed-durable row routes to the acquire/unavailable open result so the operator's `error_types` chain fires; an in-flight-holder conflict keeps the retry-then-bail shape (2026-06-10, cascade-and-claim-handoff completion report, artifact).
- The substitution directive `{{claim.<alias>.claim_scope}}` is the single canonical spelling: it passes registration validation and resolves at runtime; the legacy `{{claim.<alias>.scope}}` is rejected at registration with an error naming the rename (2026-06-06 gap-closure + 2026-06-08 corpus-bootstrap, artifact).
- `OpenRequest` carries `template_id` (content hash) and `instance_id` (UUID) as a per-spec scope envelope — opaque to rimsky, available to the producer for namespace routing (2026-05-04, service-protocol-contract, artifact-only).

## Intentional absences

- **Any parsing/globbing/range-matching of scope bytes inside the foundation** — never existed by design; interpretation belongs to the producer (2026-05-04, foundation-contract).
- **The bare term "scope" / files-and-symbols using unqualified Scope** — retired by the 2026-05-22 ClaimScope rename (disambiguation from RunScope); `scope.md` became `claim-scope.md`, `scope_data` became `claim_scope_data`, `{{claim.<alias>.scope}}` became `.claim_scope`. Exception by explicit judgment: the Go constant `LockKindScope` keeps its identifier name (only its string value changed to `"claim_scope"`) — the asymmetry was preferred over a no-gain call-site ripple (2026-05-22, fan-out-safety divergences).
- **The term "region"** (SQL `region_data`, proto `bytes region`, `RegionConflict`) — retired repo-wide and on the wire 2026-05-04 (layer-crystallization + service-protocol-contract).
- **Legacy `{{claim.<alias>.scope}}` directive spelling** — rejected at registration by design, not merely unsupported (2026-06-06, 2026-06-08).
- **Defined cross-write-semantics coexistence cases** — deliberately undefined; must not be modeled, tested, or documented (2026-06-19, a02fe167, transcript).

## Corrections and restorations (drift-fight record)

- **ScopesConflict had zero callers** (2026-06-06, comprehensive-gap-closure): the producer capability was advertised on the wire but acquisition compared only byte-equality. Ruled: code drifted from invariant 4b; wire the predicate into acquisition and the fan-out sub-claim path.
- **Directive split brain** (2026-06-06, comprehensive-gap-closure): registration validator accepted only legacy `.scope`, runtime resolver only `.claim_scope` — no spelling passed both. Ruled: make `claim_scope` canonical end-to-end, reject legacy at registration.
- **ListByProducerScope over-broad predicate** (2026-05-17, post-data-platform-cleanup): spec's `state IN ('active','committed')` blocked reacquisition for the whole retention window; execution corrected it to durable-committed-only. The spec was wrong, the executed refinement is authoritative.
- **Doc-stale rationale** (2026-07-13, 3f71f90a, transcript): the claim-scope concept doc's byte-equality-only canonicalization rationale predates `SupportsScopesConflict`; adjudicated fix-doc (finding 35) — rewrite the rationale; code and the claim-producer concept/story already agree.

## Superseded / historical

- "region" as the conflict-predicate noun → renamed "scope" (2026-05-04), then "claim-scope" (2026-05-22).
- Byte-equality as the *only* conflict primitive (2026-05-04) → byte-equality as the *default*, with producer-defined overlap via `ScopesConflict` for producers advertising it (2026-05-15, invariant 4b rephrase).
- Spec predicate `ListByProducerScope WHERE state IN ('active','committed')` → durable-committed-only predicate (2026-05-17).
- Every scope conflict retried-then-bailed uniformly → persistent/in-flight conflict split (2026-06-10).
