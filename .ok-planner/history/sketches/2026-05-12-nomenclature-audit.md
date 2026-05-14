# Nomenclature Audit — 2026-05-12

**Generated:** 2026-05-12 by automated inventory pass.
**Scope:** every load-bearing noun across `foundation/`, `protocols/`, `modeling/`, and bundled services, cross-referenced with all 46 concepts in `.ok-planner/design/concepts/`.
**Working artifact:** not durable documentation; will be walked through, condensed into a spec, then archived.

## How to read this file

Each layer section lists the layer's load-bearing nouns. For each noun (one concept per subsection):

- A small surface table showing where the noun appears as Go code, DB tables, proto types, YAML keys, prose — using the citation grammar from `.claude/rules/citation-grammar.md`.
- A **drift call** line, one of:
  - `rename artifact`: artifact diverges from concept slug; should rename to match.
  - `rename concept`: concept slug diverges from canonical artifact name; slug should change.
  - `keep — layer-appropriate`: divergence is intentional (e.g., "store" at bundled-services).
  - `unclear — discuss`: needs human decision.
  - `aligned`: artifact and concept agree; no action.
- A short **notes** line.

For most concepts most rows are aligned and the entry is one line. Drift is the interesting case.

Concepts are placed under the layer that **owns** the canonical implementation (per the concept doc's "Boundaries" section). Some concepts span layers — those carry a "cross-layer touch" note and also appear in the cross-layer concerns section.

---

## Layer 1: `foundation/`

(Module: `pkg:github.com/fallguy/rimsky/foundation`. Owns: cascade engine, claim/lock primitives + persistence ledgers, integration runner + sweeps, foundation persistence drivers.)

### `concept:cascade` — Engine that propagates "this node changed" downstream via stale-marking walks and pure-cascade fresh-rolls.

| Surface | Citation |
|---|---|
| Go package | `` `code:foundation/cascade/` `` |
| Cascade-on-terminal walk | `` `code:foundation/integration/cascade_invalidate.go` `` |
| Pure-cascade walk | `` `code:foundation/integration/cascade_recalculate.go` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/cascade.md` `` |

**Drift call:** aligned (vocabulary overload resolved by cross-layer #10's three-word split: walk / propagation / fallthrough inside the `concept:cascade` umbrella)

**Notes:** "cascade" is the single word covering two distinct walks (cascade-on-terminal stale-mark vs pure-cascade fresh-roll). See `tension:cascade-walks-overloaded`. The artifact surface is consistent (`foundation/cascade/`, `cascade_invalidate.go`, `cascade_recalculate.go`); the overload was in prose vocabulary, not in naming-vs-concept-slug drift. Cross-layer #10's decision adopts a three-word vocabulary (walk / propagation / fallthrough) inside the umbrella concept; no concept-level rename needed.

### `concept:node-state` — Small enum on `rimsky_nodes.state` (fresh, stale, running, failed, parked) with explicit transition table.

| Surface | Citation |
|---|---|
| Go function | `` `code:foundation/cascade/state.go::NextState` `` |
| DB column | `` `col:rimsky_nodes.state` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/node-state.md` `` |

**Drift call:** aligned

**Notes:** Concept slug, Go function family, and DB column all agree. Open tension `tension:state-count-drift` is about doc-prose drift (4 vs 5 states across CLAUDE.md and concepts) — not a rename target.

### `concept:last-outcome` — TEXT column on `rimsky_nodes` carrying fresh_changed, fresh_unchanged, passed, pure_cascade, failed.

| Surface | Citation |
|---|---|
| DB column | `` `col:rimsky_nodes.last_outcome` `` |
| Go var | `` `code:foundation/integration/terminal_outcome.go::resolveLastOutcome` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/last-outcome.md` `` |

**Drift call:** aligned

**Notes:** Column name = concept slug (snake-cased). Cascade-firing gate reads `last_outcome == fresh_changed`. The overlap with `concept:transition-reason` is a separate concern — see cross-layer section.

### `concept:transition-reason` — Audit-vocabulary enum carried on every node-state transition; sibling to last_outcome for the cascade-fire predicate.

| Surface | Citation |
|---|---|
| Go type | `` `code:foundation/cascade/state.go::TransitionReason` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/transition-reason.md` `` |

**Drift call:** aligned

**Notes:** Go type name maps cleanly to concept slug. Vocabulary overlap with `concept:last-outcome` is captured by `tension:transition-reason-vs-last-outcome`.

### `concept:parked-state` — Fifth legal node-state entered from running when the executor emits ParkRequested.

| Surface | Citation |
|---|---|
| Go function | `` `code:foundation/integration/runner_terminal_park.go` `` |
| DB phase enum value | `` `col:rimsky_worker_request.phase` (value `'parked'`) `` |
| State enum value | `` `code:foundation/cascade/state.go` (state `'parked'`) `` |
| Proto event | `` `proto:executor.proto::ParkRequested` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/parked-state.md` `` |

**Drift call:** aligned

**Notes:** Concept slug `parked-state` is slightly verbose; the in-code values are bare `'parked'`. Consider whether the concept slug should drop the `-state` suffix to match the column value (`parked`). Adjacent open tension: `tension:timeout-policy-asymmetry`.

### `concept:claim` — Protocol-layer noun for a node's request to access a producer-managed resource.

| Surface | Citation |
|---|---|
| Go type | `` `code:protocols/claimproducer/types.go::ClaimSpec` `` (re-exported as `` `code:foundation/locks/types.go::ClaimSpec` ``) |
| Result type | `` `code:protocols/claimproducer/types.go::ClaimResult` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/claim.md` `` |

**Drift call:** aligned

**Notes:** Listed under foundation because the persistence-side `claim-handle` lives here and the locks package re-exports the protocol types. `ClaimSpec.StoreName` field — the legacy `Store` substring is a cross-layer concern (see cross-layer #1).

### `concept:claim-handle` — Rimsky-side ledger row in `rimsky_claim_handle` representing one acquired claim or named-lock.

| Surface | Citation |
|---|---|
| DB table | `` `table:rimsky_claim_handle` `` |
| Persistence interface | `` `code:foundation/persistence/driver.go::ClaimHandlesStore` `` |
| Field on AcquiredLock | `` `code:foundation/integration/runner.go::AcquiredLock.ClaimHandleID` `` |
| Legacy alias (table) | `rimsky_lock_holders` (Phase-5 renamed) |
| Concept doc | `` `file:.ok-planner/design/concepts/claim-handle.md` `` |

**Drift call:** aligned

**Notes:** Phase-5 schema rename complete in code; residual legacy mentions in prose only — `tension:lock-holder-vs-claim-handle-legacy`. The `ClaimHandlesStore` interface name carries the `Store` colloquialism — see cross-layer #1.

### `concept:claim-producer` — Out-of-process peer service implementing the gRPC ClaimProducer protocol (4 verbs + Capabilities).

| Surface | Citation |
|---|---|
| Go interface (foundation) | `` `code:foundation/locks/interface.go::ClaimProducer` `` |
| Legacy type alias | `` `code:foundation/locks/interface.go::Store` `` (= `ClaimProducer`) |
| Protocol-layer interface | `` `code:protocols/claimproducer/claimproducer.go::ClaimProducer` `` |
| Proto service | `` `proto:claim_producer.proto::ClaimProducer` `` |
| Persistence interface naming | `` `code:foundation/persistence/store.go::Store` `` (DRIVER umbrella; unrelated noun collision) |
| YAML | `` `cfg:claim_producers[]` `` (legacy alias `` `cfg:stores[]` ``) |
| Concept doc | `` `file:.ok-planner/design/concepts/claim-producer.md` `` |

**Drift call:** aligned (resolved by cross-layer #1 + #2)

**Notes:** Canonical cross-layer alias-retirement target. At the foundation/protocol layer the Go `Store = ClaimProducer` type alias is "kept temporarily" — retiring it is in scope. `AcquiredLock.Store` field name (see `concept:write-semantics`/runner) similarly should rename. Note that `foundation/persistence/store.go::Store` is a SEPARATE noun — the persistence-driver umbrella interface, not a claim-producer alias. The cross-layer plan should not conflate them. See cross-layer #1 (alias retirement + Store-substring residue) and cross-layer #2 (persistence-side `Store` → `Driver` rename, frees the namespace). After both: "Store" appears only at the bundled-services layer (`stores/` directory), layer-appropriate.

### `concept:named-lock` — Producer-independent capacity-counter primitive declared in rimsky.yml.

| Surface | Citation |
|---|---|
| Go type | `` `code:foundation/locks/types.go::NamedLockSpec` `` |
| DB row discriminant | `` `col:rimsky_claim_handle.lock_kind` (value `'named'`) `` |
| YAML | `` `cfg:named_locks[]` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/named-lock.md` `` |

**Drift call:** aligned

**Notes:** Type, column, and config key all use "named lock" consistently.

### `concept:scope` — Opaque byte stream that ClaimProducer.Open returns to identify what was acquired; byte-equal compared.

| Surface | Citation |
|---|---|
| Go function | `` `code:foundation/locks/conflict.go::ScopesByteEqual` `` |
| DB column | `` `col:rimsky_claim_handle.scope_data` `` |
| Legacy term | `region` (deprecated; still in comments) |
| Concept doc | `` `file:.ok-planner/design/concepts/scope.md` `` |

**Drift call:** aligned (resolved by cross-layer #7 — delete region legacy comment entirely)

**Notes:** `scope` is the canonical name across code + schema + concept. `region` appears only in legacy comments and historical references — `tension:region-vs-scope-legacy`. Cross-layer #7 deletes the `code:foundation/locks/conflict.go:14-18` "v2's per-store RegionsConflict" paragraph; git log carries the design-evolution history if needed.

### `concept:write-semantics` — Per-claim enum (sync, staged_async, blocking_async, read_only) governing the conflict matrix.

| Surface | Citation |
|---|---|
| Go type | `` `code:foundation/locks/types.go::WriteSemantics` `` (re-export of `claimproducer.WriteSemantics`) |
| DB column | `` `col:rimsky_claim_handle.realized_write_semantics` `` |
| YAML | `` `cfg:claim_producers[].write_semantics_envelope` `` (legacy `write_semantics:` accepted) |
| Concept doc | `` `file:.ok-planner/design/concepts/write-semantics.md` `` |

**Drift call:** aligned (resolved by cross-layer #6 — retire single-value alias, rename `envelope` → `allowed`)

**Notes:** Go type and column align. YAML legacy single-value `write_semantics:` alias of `write_semantics_envelope:` is resolved by cross-layer #6: retire the single-value alias; rename `write_semantics_envelope` → `write_semantics_allowed` across YAML, proto, Go, and docs. Plain English captures the operator-policy framing without the "envelope" metaphor.

### `concept:held-claim` — A claim whose lifetime extends past its acquirer's terminal to cover the holding subgraph.

| Surface | Citation |
|---|---|
| DB row flag | `` `col:rimsky_claim_handle.is_held` `` |
| DB child table | `` `table:rimsky_claim_holders` `` |
| FK on holders | `` `col:rimsky_claim_holders.claim_handle_id` `` (renamed from `lock_holder_id`) |
| Concept doc | `` `file:.ok-planner/design/concepts/held-claim.md` `` |

**Drift call:** aligned

**Notes:** Phase-5 renames complete. `rimsky_claim_holders` is the table name; the legacy `lock_holder_id` column is renamed.

### `concept:auto-terminal` — Mechanism that fires Commit or Abandon exactly once at completion of a held claim's holding-subgraph.

| Surface | Citation |
|---|---|
| Go function | `` `code:foundation/integration/auto_terminal.go::CheckAndFireResolution` `` |
| Unified engine | `` `code:foundation/integration/terminal_decision.go::ResolveClaimHandleTerminal` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/auto-terminal.md` `` |

**Drift call:** aligned

**Notes:** Concept slug, file name, and Go function names align.

### `concept:advisory-lock` — Four advisory-lock primitives on `persistence.AdvisoryLocker`.

| Surface | Citation |
|---|---|
| Go interface | `` `code:foundation/persistence/driver.go::AdvisoryLocker` `` |
| Postgres impl | `` `code:foundation/persistence/postgres/advisory_locker.go` `` |
| SQLite impl | `` `code:foundation/persistence/sqlite/advisory_locker.go` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/advisory-lock.md` `` |

**Drift call:** aligned

**Notes:** Concept and interface names match cleanly.

### `concept:supervisor` — Runtime binary implementing the acquisition transaction, dispatch, terminal handling, auto-terminal.

| Surface | Citation |
|---|---|
| Go file | `` `code:foundation/integration/supervisor.go` `` |
| Binary | `` `code:cmd/rimsky-supervisor/main.go` `` |
| DB table | `` `table:rimsky_supervisors` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/supervisor.md` `` |

**Drift call:** aligned

**Notes:** Concept, package, binary, table all align.

### `concept:worker-request` — Parent row `rimsky_worker_request` for one dispatched run of one node.

| Surface | Citation |
|---|---|
| DB table | `` `table:rimsky_worker_request` `` (legacy `rimsky_dispatch`) |
| Go struct | `` `code:foundation/persistence/postgres/queue.go::WorkerRequest` `` (approx; checked via package) |
| Legacy colloquial prose | "dispatch row" |
| Concept doc | `` `file:.ok-planner/design/concepts/worker-request.md` `` |

**Drift call:** aligned

**Notes:** Phase-5 rename from `rimsky_dispatch` is complete in schema and most code. Some prose still uses "dispatch row" colloquially — captured by `tension:lock-holder-vs-claim-handle-legacy` (which is the umbrella for all Phase-5 schema-rename residue).

### `concept:orphan-reaper` — Periodic sweep that hard-deletes stale `rimsky_worker_request` and `rimsky_claim_handle` rows.

| Surface | Citation |
|---|---|
| Go file | `` `code:foundation/integration/orphan_reaper.go` `` |
| Sweep functions | `SweepStaleHeartbeats`, `SweepOrphanedClaims`, `SweepReady`, `SweepLockHolders` |
| Concept doc | `` `file:.ok-planner/design/concepts/orphan-reaper.md` `` |

**Drift call:** aligned (resolved by cross-layer #5 — `SweepLockHolders` → `SweepOrphanedClaimHandles`)

**Notes:** Concept slug matches the file name. The function name `SweepLockHolders` still carries the pre-Phase-5 `LockHolders` noun in its identifier even though it now operates on `rimsky_claim_handle`. Cross-layer #5 renames to `SweepOrphanedClaimHandles` — mirrors the sibling `SweepStaleHeartbeats` naming pattern. Note: the concept itself renames to `concept:node-run` under cross-layer #14, so the table being swept becomes `rimsky_node_runs`. The reaper function name `SweepOrphanedClaimHandles` is about the claim-handle table; the orphan reaper covers BOTH `rimsky_node_runs` AND `rimsky_claim_handles` rows — the function naming after rename should reflect that (perhaps `SweepOrphanedClaimHandles` + a sibling `SweepOrphanedNodeRuns`, or one umbrella `SweepOrphans`). Brainstorm pass will detail.

### `concept:terminal-resolution` — End-to-end spine from one executor terminal event to (last_outcome, dispatch fate, producer verb, claim-handle delete).

| Surface | Citation |
|---|---|
| Stage-2 dispatch | `` `code:foundation/integration/runner_terminal.go::applyTerminal` `` |
| Unified producer-verb site | `` `code:foundation/integration/terminal_decision.go::ResolveClaimHandleTerminal` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/terminal-resolution.md` `` |

**Drift call:** aligned (concept is umbrella)

**Notes:** No single in-code symbol matches; the concept is umbrella across four constituents (executor, lifecycle-handler, error-policy, auto-terminal). The concept doc explicitly notes "the spine as a whole has no canonical name in the source; this concept introduces 'terminal resolution' as the umbrella."

### `concept:persistence-driver` — Umbrella interface family abstracting Postgres vs SQLite.

| Surface | Citation |
|---|---|
| Go interface | `` `code:foundation/persistence/driver.go::Driver` `` |
| Umbrella `Store` | `` `code:foundation/persistence/store.go::Store` `` (persistence umbrella — distinct from `claim-producer.Store` alias) |
| Postgres impl | `` `code:foundation/persistence/postgres/` `` |
| SQLite impl | `` `code:foundation/persistence/sqlite/` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/persistence-driver.md` `` |

**Drift call:** aligned (resolved by cross-layer #2 — rename persistence-side `Store` → `Driver`)

**Notes:** Concept slug is `persistence-driver`, but the Go umbrella interface is `Store`. This creates a NAME COLLISION with `code:foundation/locks/interface.go::Store` (claim-producer alias). Two unrelated nouns both called `Store`. Cross-layer #2 resolves: rename persistence-side `Store` → `Driver` (matching the concept slug) and rename file `store.go` → `driver.go`. Post-rename the Go interface and concept slug align; namespace is freed for the claim-producer-side `Store` alias retirement (cross-layer #1).

### `concept:blob-backend` — Pluggable byte-stream backend for spilled attribute values, parked payloads, and named-event payloads.

| Surface | Citation |
|---|---|
| Go interface | `` `code:foundation/persistence/blob.go::BlobBackend` `` |
| YAML | `` `cfg:persistence.blob.backend` `` |
| Orphan-blob table | `` `table:rimsky_blob_orphans` `` |
| Conformance binary | `` `code:cmd/rimsky-blob-backend-conformance/main.go` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/blob-backend.md` `` |

**Drift call:** aligned

**Notes:** Concept, interface, config key, and binary name all match.

### `concept:event-log` — Rimsky's internal append-only audit log (`rimsky_events`) with free-form `kind` TEXT.

| Surface | Citation |
|---|---|
| DB table | `` `table:rimsky_events` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/event-log.md` `` |

**Drift call:** aligned

**Notes:** Concept slug differs from table name (`event-log` vs `rimsky_events`) but the prose-vs-schema mapping is unambiguous. Aliases in concept doc: "audit log", "rimsky_events table". Adjacent `tension:events-kind-no-enum` is about schema enforcement, not naming. Note: file name was previously misleading (pre-convergence `event-log.md` also covered `rimsky_node_events`); content is now audit-log-only and named-event ledger moved to `concept:named-event`.

### `concept:opacity` — Cross-cutting discipline making four byte streams (userdata, claim payload/address/scope, blob content, named-event payloads) inert in rimsky.

| Surface | Citation |
|---|---|
| Invariant anchors | `` `invariant:11` ``, `` `invariant:20` ``, `` `invariant:21` `` |
| Sanctioned read sites | `walkPath`, `stringifyRaw`, `makeStoreHandle` |
| Concept doc | `` `file:.ok-planner/design/concepts/opacity.md` `` |

**Drift call:** aligned

**Notes:** Cross-cutting concept; no code/table mapping (conceptual). The single sanctioned site `makeStoreHandle` carries `Store` in its name — see cross-layer #1.

---

## Layer 2: `protocols/`

(Module: `pkg:github.com/fallguy/rimsky/protocols`. Owns: gRPC service protocol Go interfaces + .proto sources + generated bindings. Stdlib + grpc + protobuf only.)

### `concept:executor` — Out-of-process peer service that implements `NodeExecutor.Execute` + optional ExecutorObservability.

| Surface | Citation |
|---|---|
| Go interface | `` `code:protocols/executor/executor.go::Executor` `` |
| Proto service | `` `proto:executor.proto::NodeExecutor` `` |
| Bundled impls | `` `code:executors/http-node/` ``, `` `code:executors/claude-agent/` ``, `` `code:executors/stub/` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/executor.md` `` |

**Drift call:** aligned (resolved by cross-layer #8 — rename proto service `NodeExecutor` → `Executor`)

**Notes:** Proto service name is `NodeExecutor`; Go interface is `Executor`; concept slug + directory + operator vocabulary is `executor`. Three-way naming asymmetry. Cross-layer #8 resolves: drop the `Node` prefix from the proto service. After the rename: Go interface, proto service, concept slug, directory, binary, and prose all uniformly use "executor." Generated bindings emit `ExecutorServer`/`ExecutorClient` into the codegen package, which doesn't collide with the Go interface `Executor` in `protocols/executor/`. Adjacent: `tension:terminal-event-overloaded` (resolved by #9's proto restructure), `tension:async-callback-body-key` (likely superseded by #9; revisit in brainstorm).

### `concept:claim-producer` (protocol layer) — gRPC ClaimProducer service interface.

| Surface | Citation |
|---|---|
| Go interface | `` `code:protocols/claimproducer/claimproducer.go::ClaimProducer` `` |
| Proto service | `` `proto:claim_producer.proto::ClaimProducer` `` |
| Re-export | `` `code:foundation/locks/interface.go::ClaimProducer` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/claim-producer.md` `` (primary entry in foundation section) |

**Drift call:** aligned (residual `StoreName` field resolved by cross-layer #1 — `ClaimSpec.StoreName` → `.ProducerName`)

**Notes:** At the protocol layer, the canonical name `ClaimProducer` is consistent (Go interface, proto service, package name `protocols/claimproducer/`). The `ClaimSpec.StoreName` field is the residual `Store`-substring at the protocol layer — cross-layer #1 renames it to `.ProducerName`.

### `concept:lifecycle-subscriber` — Opt-in peer protocol with six methods for template/instance lifecycle events.

| Surface | Citation |
|---|---|
| Go interface | `` `code:protocols/lifecycle/` `` |
| Proto service | `` `proto:lifecycle.proto::LifecycleSubscriber` `` |
| Idempotency table | `` `table:rimsky_lifecycle_idempotency` `` |
| YAML config key | `cfg:claim_producers[].protocols` (value `lifecycle_subscriber`) |
| Concept doc | `` `file:.ok-planner/design/concepts/lifecycle-subscriber.md` `` |

**Drift call:** aligned

**Notes:** Concept, proto service, and config-value-string all match.

### `concept:observability` — Peer-facing optional observability protocols (ExecutorObservability / StoreObservability) plus startup handshake.

| Surface | Citation |
|---|---|
| Proto services | `` `proto:executor_observability.proto::ExecutorObservability` ``, `` `proto:store_observability.proto::StoreObservability` `` |
| Go handshake | `` `code:modeling/observability/handshake.go` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/observability.md` `` |

**Drift call:** aligned (resolved by cross-layer #1 — `StoreObservability` → `ClaimProducerObservability`)

**Notes:** `StoreObservability` carries the legacy `Store` noun at the protocol level. For consistency with `ClaimProducer` (which is the canonical name for the producer side), cross-layer #1 renames `StoreObservability` → `ClaimProducerObservability`. After the rename: the proto observability services are `ExecutorObservability` and `ClaimProducerObservability` — symmetric pair with no legacy `Store` residue. Cross-layer #15's `Capabilities` rename (drop `Get` prefix on executor side) further aligns the handshake RPC name across both observability services.

### `concept:conformance` — Four standalone binaries that exercise third-party executor / claim-producer / blob-backend impls.

| Surface | Citation |
|---|---|
| Binary | `` `code:cmd/rimsky-executor-conformance/main.go` `` |
| Binary | `` `code:cmd/rimsky-conformance-probe/main.go` `` |
| Binary | `` `code:cmd/rimsky-claim-producer-conformance/main.go` `` |
| Binary | `` `code:cmd/rimsky-blob-backend-conformance/main.go` `` |
| Shared fixtures | `` `code:conformance/` ``, `` `code:foundation/persistence/conformance/` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/conformance.md` `` |

**Drift call:** rename artifact (binary name) — discuss

**Notes:** The four binary names all follow `rimsky-<thing>-conformance` except the executor one which is just `rimsky-executor-conformance` (no specifier). Pre-v1 cosmetic asymmetry. Adjacent: `tension:stub-mode-runtime-only-gate`, `tension:blob-backend-conformance-fixture-asymmetry`, `tension:stub-mode-signature-no-proto-surface`.

**Decision (2026-05-12 walkthrough):**
  - Rename `code:cmd/rimsky-executor-conformance/main.go` → `code:cmd/rimsky-executor-conformance/main.go`. The implicit "no-specifier means executor conformance" is a footgun for cold readers of the binary list; renaming makes the four-binary set self-documenting.
  - All four binaries now follow the pattern `rimsky-<protocol>-conformance`:
    - `rimsky-executor-conformance` (renamed)
    - `rimsky-executor-conformance-probe` (consider renaming the probe sidecar for symmetry; OR keep `rimsky-conformance-probe` since the probe is a generic gate, not protocol-specific)
    - `rimsky-claim-producer-conformance`
    - `rimsky-blob-backend-conformance`
  - **Probe-binary naming:** the probe sidecar is currently `rimsky-conformance-probe`. Two options: rename to `rimsky-executor-conformance-probe` for full symmetry, OR keep as a generic gate name since the probe could plausibly be extended to other protocols later (e.g., a future ClaimProducer stub-mode probe). Lean keep-as-generic; revisit if/when other protocols need stub-mode probing.
  - Touches: `file:Makefile` build target, `file:deploy/docker-compose.yml` (if any service references the binary), CI workflows that invoke `rimsky-executor-conformance`, the rimsky CLAUDE.md "Build & test" section, and `concept:conformance.md` body.
  - **Resolves the conformance binary asymmetry.** Adjacent open tensions (`tension:stub-mode-runtime-only-gate`, `tension:blob-backend-conformance-fixture-asymmetry`, `tension:stub-mode-signature-no-proto-surface`) are independent and stay open.
  - A future addition: `rimsky-lifecycle-subscriber-conformance` for the LifecycleSubscriber protocol — currently no conformance binary exists for that protocol. Out of scope for this rename pass; flagged for the brainstorm if useful.

---

## Layer 3: `modeling/` (root module)

(Module: `pkg:github.com/fallguy/rimsky`. Owns: templates, instances, frames, scheduling, control-api, attributes, quality rules.)

### `concept:template` — Static artifact a consumer registers, keyed by `sha256-<hex>` over the JCS-canonicalized spec bytes.

| Surface | Citation |
|---|---|
| Go type | `` `code:modeling/node/template.go::TemplateSpec` `` |
| DB table | `` `table:rimsky_templates` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/template.md` `` |

**Drift call:** aligned

**Notes:** Concept and table align. `template_hash` is the canonical FK column name; the legacy `template_id` term appears in some prose with a `vocabulary-lint-ignore` annotation.

### `concept:instance` — One live deployment of a template, identified by UUID, bound to a `template_hash`.

| Surface | Citation |
|---|---|
| DB table | `` `table:rimsky_instances` `` |
| Column | `` `col:rimsky_instances.instance_key` `` (legacy `consumer_key`) |
| Concept doc | `` `file:.ok-planner/design/concepts/instance.md` `` |

**Drift call:** aligned (resolved by cross-layer #3 — migration-baseline rebase erases all rename history)

**Notes:** Schema rename `consumer_key` → `instance_key` is done at column level (migration 003); legacy `consumer_key` still appears at `code:foundation/persistence/postgres/migrations/001-initial.sql:62` (the original migration that 003 later renamed) and in `code:foundation/persistence/postgres/instances.go:8` rename-history comment. Cross-layer #3 collapses the migration history into a single `001-baseline.sql` reflecting current schema, drops the rename-history comments, and absorbs this residue completely. Vocabulary lint fixture stays.

### `concept:tag` — Movable string alias pointing at a template_hash, stored in `rimsky_template_tags`.

| Surface | Citation |
|---|---|
| DB table | `` `table:rimsky_template_tags` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/tag.md` `` |

**Drift call:** aligned

**Notes:** Concept slug + table align. `compose:<project>:<...>` prefix is reserved client-side only — `tension:compose-prefix-client-side`.

### `concept:frame` — One cascade resolution, tracked as a row in `rimsky_frames`; every dispatched run carries `frame_id`.

| Surface | Citation |
|---|---|
| DB table | `` `table:rimsky_frames` `` |
| Go type | `` `code:modeling/frame/types.go::Frame` `` |
| Template field | `` `code:modeling/node/template.go::TemplateSpec.FrameResolution` `` (YAML `frame_resolution`) |
| Runtime column | `` `col:rimsky_frames.mode` `` |
| Producer entry | `` `code:modeling/frame/producer.go::EnqueueOrCoalesce` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/frame.md` `` |

**Drift call:** aligned (resolved by cross-layer #4 + #13)

**Notes:** `frame_resolution:` (template-author YAML) vs `mode` (persisted column) is the same value across the same JCS-canonicalized flow. Cross-layer #4 canonicalizes on `frame_resolution_mode` across all surfaces (YAML, `col:rimsky_frames.frame_resolution_mode`, Go type `FrameResolutionMode`, helper `LookupFrameResolutionMode`). Singular-vs-plural concept-doc nit is resolved by cross-layer #13's "standardize on plural" decision (concept docs sweep to use the canonical `rimsky_frames` form everywhere).

### `concept:schedule` — Node-level cron expression in `rimsky_schedules`, advanced by scheduler tick under advisory lock.

| Surface | Citation |
|---|---|
| DB table | `` `table:rimsky_schedules` `` |
| Go pkg | `` `code:modeling/scheduler/` `` |
| Tick | `` `code:modeling/scheduler/scheduler.go::tick` `` |
| Admin route | `` `route:POST /admin/scheduled-nodes/{id}/force-fire` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/schedule.md` `` |

**Drift call:** aligned

**Notes:** `schedule` (concept) / `rimsky_schedules` (table) / `modeling/scheduler/` (package) — concept noun is "schedule"; the actor that processes them is the "scheduler" (binary + package). Both consistent. Adjacent: `tension:force-fire-204-hides-asynchrony`, `tension:coalesced-fire-observability-gap`.

### `concept:invalidate` — Sole graph-level message that the scheduler / control-api / handler emits to mark a node stale.

| Surface | Citation |
|---|---|
| Go function | `` `code:foundation/integration/cascade_invalidate.go::InvalidateNode` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/invalidate.md` `` |

**Drift call:** aligned

**Notes:** Verb-as-concept slug; function name matches. No tensions specific to this concept.

### `concept:attribute` — Typed JSON-Schema inputs and outputs of a node with `{{...}}` substitution.

| Surface | Citation |
|---|---|
| Go package | `` `code:modeling/attribute/` `` |
| DB writeback table | `` `table:rimsky_node_attributes` `` |
| Template field | `attributes:` |
| Substitution | `` `code:modeling/attribute/substitution.go::walkPath` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/attribute.md` `` |

**Drift call:** aligned

**Notes:** Concept, package, and table all align. Open tensions about doc-prose count drift only — `tension:substitution-grammar-count-drift`, `tension:substitution-introspection-site-count`.

### `concept:userdata` — Opaque per-node JSON blob attached by template author, consumed verbatim by executor.

| Surface | Citation |
|---|---|
| Proto field | `` `proto:executor.proto::ExecuteRequest.userdata` `` |
| DB column | `` `col:rimsky_instances.userdata_overrides` `` |
| Merge helper | `` `code:modeling/shared/jsonmerge.go::DeepMergeJSON` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/userdata.md` `` |

**Drift call:** aligned

**Notes:** Single noun across proto, column, and concept. Per-instance overrides folded into this concept under `spec:2026-05-11-design-log-convergence` (previously a standalone concept). Adjacent: `tension:userdata-schema-as-opacity-exception`.

### `concept:lifecycle-handler` — Per-node template declarations routing executor and acquisition events into resolve+invalidate actions across four slots.

| Surface | Citation |
|---|---|
| Template fields | `on_acquire_unavailable`, `on_executor_complete`, `on_executor_blocked`, `on_executor_errored` |
| Runtime apply | `` `code:foundation/integration/runner_terminal_handlers.go::applyTerminalBlockedOrErrored` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/lifecycle-handler.md` `` |

**Drift call:** aligned

**Notes:** Four slot names are stable. The sibling `on_event` is a separate concept (`concept:on-event-handler`). Adjacent: `tension:error-action-count-drift`, `tension:blocked-vs-errored-routing`.

### `concept:on-event-handler` — Per-node `on_event` map sharing the resolve+invalidate vocabulary with the four lifecycle handlers.

| Surface | Citation |
|---|---|
| Template field | `on_event:` |
| Validation | `discovery-cache`-driven cross-check at template registration |
| Concept doc | `` `file:.ok-planner/design/concepts/on-event-handler.md` `` |

**Drift call:** aligned

**Notes:** `on_event` is the map shape; concept slug names the handler explicitly. Older prose (e.g. `docs/concepts/handlers.md`) collapsed this with the four lifecycle handlers as "5 slots".

### `concept:named-event` — Non-terminal executor emission with a name and opaque payload.

| Surface | Citation |
|---|---|
| Proto message | `` `proto:executor.proto::NamedEvent` `` |
| DB table | `` `table:rimsky_node_events` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/named-event.md` `` |

**Drift call:** aligned

**Notes:** Concept, proto, and table align (table name `rimsky_node_events` reflects that they're per-node executor emissions, distinct from the audit `rimsky_events`). Ledger content moved here from `concept:event-log` under convergence.

### `concept:error-policy` — Template-level `error_types:` block mapping per-error_class to retry/invalidate/give_up/pass actions.

| Surface | Citation |
|---|---|
| Template field | `error_types:` |
| Runtime | `` `code:foundation/integration/runner_terminal_errors.go::applyTerminalAppError` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/error-policy.md` `` |

**Drift call:** rename artifact (code file + function only)

**Notes:** Concept slug is `error-policy`; the template field is `error_types:`; the implementation file is `runner_terminal_errors.go`. Three names for the closely-related thing. Adjacent open tensions: `tension:error-action-count-drift`, `tension:blocked-vs-errored-routing`.

**Decision (2026-05-12 walkthrough):**
  - **Keep `concept:error-policy`** as the design-log noun for the decision surface.
  - **Keep `error_types:`** as the operator-facing YAML field. Accurately descriptive of the map shape (error_class → action). `error_policy:` would suggest "one policy" but the field is actually a map.
  - **Rename code surfaces** for symmetry with the concept slug:
    - File: `runner_terminal_errors.go` → `runner_error_policy.go`
    - Function: `applyTerminalAppError` → `applyErrorPolicy`
  - **Document the three-name relationship** in `concept:error-policy.md`: "The design-log noun is `error-policy`; the operator-facing YAML field is `error_types:` (map of error_class → action); the implementation lives in `code:foundation/integration/runner_error_policy.go::applyErrorPolicy`."
  - **Cross-layer #9 interaction:** after collapsing `Blocked` (and never-implemented `Retry`) into `Error{error_class: ...}`, the `error_types:` map becomes the *single* decision surface for all error variants. The "policy" framing strengthens; the rename pass solidifies it across all surfaces.
  - **Resolves `tension:blocked-vs-errored-routing`** (indirectly, via #9's collapse).
  - **`tension:error-action-count-drift`** likely also resolves by being explicit about the four actions (retry / invalidate / give_up / pass) post-rename in the concept doc; revisit in brainstorm.

### `concept:quality-rule` — Template-node-level declarative content validation against a node's writeback.

| Surface | Citation |
|---|---|
| Go package | `` `code:modeling/qualityrule/` `` |
| Template field | `quality_rules:` |
| Concept doc | `` `file:.ok-planner/design/concepts/quality-rule.md` `` |

**Drift call:** aligned

**Notes:** Concept, package, template field all match. Adjacent: `tension:quality-rule-severity-string-footgun`, `tension:quality-rule-custom-handler-ordering`.

### `concept:node` — One declarative unit of work in a template's graph; runtime row in `rimsky_nodes`.

| Surface | Citation |
|---|---|
| DB table | `` `table:rimsky_nodes` `` |
| Go package | `` `code:modeling/node/` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/node.md` `` |

**Drift call:** aligned

**Notes:** Concept slug, table, and package all use bare "node".

### `concept:control-api` — HTTP+JSON operator interface served by `rimsky-control-api`.

| Surface | Citation |
|---|---|
| Go package | `` `code:modeling/controlapi/` `` |
| Binary | `` `code:cmd/rimsky-control-api/main.go` `` |
| MCP shim | `` `code:mcp-servers/control-api/` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/control-api.md` `` |

**Drift call:** aligned

**Notes:** One concept, three surfaces (Go package, binary, MCP shim), all named `control-api` / `controlapi`. The pre-convergence concept `mcp-server` was folded here. Adjacent: `tension:control-api-version-prefix`, `tension:compose-prefix-client-side`.

### `concept:cascade-graph` — Operator-dashboard HTTP-route backplane on control-api.

| Surface | Citation |
|---|---|
| Routes | `/observability/*`, `/events`, `/frames`, `/nodes/{instance}/{type}`, `/dispatches` |
| Go pkg | `` `code:modeling/controlapi/` `` (handlers split inside) |
| Concept doc | `` `file:.ok-planner/design/concepts/cascade-graph.md` `` |

**Drift call:** aligned (resolved by cross-layer #14 — `/dispatches` route renames to `/node-runs`)

**Notes:** The name `cascade-graph` is reasonably descriptive but doesn't appear as a code symbol or directory. It is a *concept* over a set of routes. The `/dispatches` route renames to `/node-runs` per cross-layer #14. The concept slug `cascade-graph` accurately describes what the routes expose (the cascade-as-graph data model); alternatives (`dashboard-api`, `observability-routes`) are either ambiguous or undo the convergence-pass split from `concept:observability`. Routes remain root-mounted; a future `/v1/` or `/cascade-graph/` prefix is a separate decision (`tension:control-api-version-prefix`) for the brainstorm pass.

### `concept:discovery-cache` — In-memory per-peer Capabilities cache populated by the observability handshake at startup.

| Surface | Citation |
|---|---|
| Go file | `` `code:modeling/observability/discovery.go` `` |
| Handshake fill | `` `code:modeling/observability/handshake.go` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/discovery-cache.md` `` |

**Drift call:** aligned

**Notes:** Concept and file align. Promoted from inside `concept:observability` under convergence.

### `concept:rimsky-cli` — Thin HTTP+JSON client over the control-api.

| Surface | Citation |
|---|---|
| Binary | `` `code:cmd/rimsky-cli/main.go` `` |
| Go pkg | `` `code:modeling/cli/` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/rimsky-cli.md` `` |

**Drift call:** aligned

**Notes:** Concept, binary, package all align. Adjacent: `tension:compose-prefix-client-side`.

### `concept:rimsky-yml` — Single YAML file read by all three runtime processes plus migrate.

| Surface | Citation |
|---|---|
| File | `` `file:deploy/rimsky.yml` `` |
| Env var | `` `env:RIMSKY_CONFIG` `` |
| Go pkg | `` `code:modeling/config/` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/rimsky-yml.md` `` |

**Drift call:** aligned

**Notes:** Concept and config-file name align. Open tensions are cross-layer YAML alias issues: `tension:yaml-stores-alias`, `tension:yaml-write-semantics-alias`.

### `concept:module-layout` — Three-Go-module workspace plus MCP-server module, with depguard-enforced import boundaries.

| Surface | Citation |
|---|---|
| Workspace | `` `file:go.work` `` |
| Lint config | `` `file:.golangci.yml` `` (depguard rules) |
| Licensing | `` `file:licensing.yml` `` |
| Concept doc | `` `file:.ok-planner/design/concepts/module-layout.md` `` |

**Drift call:** aligned

**Notes:** Cross-cutting; no single code symbol. Captured pre-convergence as a standalone concept; licensing-boundary content folded in. Note: aliases include `three-go-modules` but the workspace actually has FOUR modules (three + MCP shim) — minor count drift in the concept doc itself.

---

## Layer 4: bundled services

(Owns: claim-producer reference impls under `stores/`, executor reference impls under `executors/`, binaries under `cmd/`.)

### Bundled claim-producer impls

| Surface | Citation |
|---|---|
| Directory | `` `code:stores/filesystem/` `` |
| Directory | `` `code:stores/postgres/` `` |
| Directory | `` `code:stores/stub/` `` |
| Shared helpers | `` `code:stores/common/` `` |
| Concept (overarching) | `` `concept:claim-producer` `` |

**Drift call:** keep — layer-appropriate

**Notes:** "store" is the colloquial-correct noun at the bundled-services layer. Directory naming `stores/` is consistent with that. The bundled-services-layer "store" is distinct from the foundation/protocol-layer `Store = ClaimProducer` type alias — that alias is the rename target, not the directory name.

### Bundled executor impls

| Surface | Citation |
|---|---|
| Directory | `` `code:executors/http-node/` `` (Go) |
| Directory | `` `code:executors/claude-agent/` `` (TypeScript) |
| Directory | `` `code:executors/stub/` `` (Go) |
| Concept (overarching) | `` `concept:executor` `` |

**Drift call:** keep — layer-appropriate

**Notes:** Directory naming `executors/` consistent with concept noun. The three impls span Go + TypeScript.

### Runtime + utility binaries

| Surface | Citation |
|---|---|
| Runtime | `cmd/rimsky-supervisor`, `cmd/rimsky-scheduler`, `cmd/rimsky-control-api` |
| Migrate | `cmd/rimsky-migrate` |
| Unified entrypoint | `cmd/rimsky-entrypoint` |
| CLI | `cmd/rimsky-cli` |
| Conformance | `cmd/rimsky-executor-conformance`, `cmd/rimsky-conformance-probe`, `cmd/rimsky-claim-producer-conformance`, `cmd/rimsky-blob-backend-conformance` |
| Docs tools | `cmd/rimsky-docs-glossary`, `cmd/rimsky-docs-lint`, `cmd/rimsky-docs-llms-full` |
| Licensing | `cmd/rimsky-license-check` |

**Drift call:** aligned

**Notes:** All binaries use the `rimsky-<thing>` prefix consistently. One minor sub-asymmetry already noted under `concept:conformance`: three of the four conformance binaries name their target (`-claim-producer-`, `-blob-backend-`, `-probe`), but the executor conformance is bare `rimsky-executor-conformance`.

### MCP server (operator control-plane shim)

| Surface | Citation |
|---|---|
| Module | `` `pkg:github.com/fallguy/rimsky/mcp-servers/control-api` `` |
| Concept | folded into `concept:control-api` |

**Drift call:** aligned

**Notes:** Standalone Go module; concept folded under `concept:control-api` (Agentic MCP shim subsection) per convergence spec. Note `executors/claude-agent` embeds a separate per-run *internal* MCP server — same protocol, different role. Dual-role observation captured in the concept doc.

---

## Cross-layer concerns

A flat list of nouns that span layers or have non-trivial drift independent of any single layer's catalog. Numbered for walkthrough reference.

### 1. `Store = ClaimProducer` alias and its `Store`-substring residue

- **Where:** `code:foundation/locks/interface.go::Store` (type alias) | `code:foundation/integration/runner.go::AcquiredLock.Store` (struct field) | `code:protocols/claimproducer/types.go::ClaimSpec.StoreName` (proto-layer field) | `proto:store_observability.proto::StoreObservability` (proto service) | `code:foundation/integration/runner_dispatch.go::makeStoreHandle` (sanctioned-introspection helper) | `cfg:stores[]` (YAML alias of `claim_producers[]`) | `stores/` (bundled-impl directory).
- **Drift call:** rename artifact at foundation/protocol layers; **keep** at bundled-services layer.
- **Plan:** retire `code:foundation/locks/interface.go::Store` type alias; rename `AcquiredLock.Store` → `AcquiredLock.Producer`; rename `ClaimSpec.StoreName` → `ClaimSpec.ProducerName`; rename `StoreObservability` proto service + Go type → `ClaimProducerObservability`; rename `makeStoreHandle` → `makeClaimHandle` or `makeProducerHandle`; decide separately on `cfg:stores[]` YAML alias (probably retire pre-v1) and on `stores/` directory (keep at bundled-services layer). Tracks `tension:store-vs-claim-producer-vocabulary`, `tension:yaml-stores-alias`.
- **Decision (2026-05-12 walkthrough):**
  - Retire `code:foundation/locks/interface.go::Store` type alias.
  - Rename `AcquiredLock.Store` → `AcquiredLock.Producer`.
  - Rename `ClaimSpec.StoreName` → `ClaimSpec.ProducerName`.
  - Rename `StoreObservability` proto service + Go type → `ClaimProducerObservability`.
  - Rename `makeStoreHandle` → `makeClaimHandle` (aligns with `table:rimsky_claim_handle`; the function builds an introspection handle for an opened claim, not for the producer).
  - Retire `cfg:stores[]` YAML alias. Pre-v1, no production pin; remove ergonomic-continuity shim.
  - Keep `stores/` directory and bundled-impl binary naming at bundled-services layer (layer-appropriate).

### 2. Persistence-side `Store` (umbrella interface) collides in name-space with claim-producer `Store` alias

- **Where:** `code:foundation/persistence/store.go::Store` is the umbrella persistence interface — a SECOND, unrelated noun also called `Store`. The locks-side `Store = ClaimProducer` alias and the persistence-side `Store` umbrella are two different things both called `Store`.
- **Drift call:** rename artifact.
- **Plan:** rename persistence-side `Store` → `Driver` to align with `concept:persistence-driver` and to free the noun. Or rename to `Persistence`. Either way the collision is resolved once the claim-producer `Store` alias retires (cross-layer #1). Coordinate the two renames so the noun unambiguously means one thing.
- **Decision (2026-05-12 walkthrough):**
  - Rename persistence-side `code:foundation/persistence/store.go::Store` → `Driver`.
  - Rename the file `foundation/persistence/store.go` → `foundation/persistence/driver.go`.
  - Update all call sites: `var s persistence.Store` → `var d persistence.Driver`, etc.
  - Coordinate with cross-layer #1 so once the claim-producer `Store` alias retires and this rename lands, the noun "Store" no longer means anything ambiguous in the codebase. After both: "store" appears only at the bundled-services layer (`stores/<name>/` binaries), which is layer-appropriate.

### 3. `consumer_key` → `instance_key` rename residue

- **Where:** schema rename complete in migration 003 (`col:rimsky_instances.instance_key`). Legacy still in `code:foundation/persistence/postgres/migrations/001-initial.sql:62` (original CREATE — historically permanent), `code:foundation/persistence/postgres/instances.go:8` (rename-history comment), `code:cmd/rimsky-docs-lint/vocabulary_test.go:31` (vocabulary lint fixture).
- **Drift call:** rename artifact in comments and tests where the historical reference isn't load-bearing; keep in migration 001 (append-only by `rules:pre-v1`).
- **Plan:** sweep prose; ensure `vocabulary_test.go` carries the `vocabulary-lint-ignore` discipline appropriately. Tracks `tension:consumer-key-vs-instance-key`.
- **Decision (2026-05-12 walkthrough) — BROADENED into migration-baseline rebase:**
  - Collapse the numbered migration chain into a single `001-baseline.sql` reflecting the current applied schema. Erases all schema-rename history at the migration-file level — `consumer_key`, the Phase-5 `rimsky_dispatch`/`rimsky_lock_holders`/`lock_holder_id` renames (cross-layer #5), `region` → `scope` (cross-layer #7), and any schema-affecting renames decided later in this walkthrough all fold into this single rebase.
  - **Operational discipline:** hard reset of dev Postgres (drop schema + reapply baseline). Pre-v1 has no production data to preserve; document the requirement in CHANGELOG under Unreleased ("This commit collapses the migration history; dev Postgres requires `DROP SCHEMA public CASCADE; CREATE SCHEMA public;` before `cmd:rimsky-migrate` reapplies the baseline").
  - **Drop the rename-history comments** in code (`code:foundation/persistence/postgres/instances.go#8` and equivalents around Phase-5 / region→scope). Git log carries the rename history.
  - **Keep** `code:cmd/rimsky-docs-lint/vocabulary_test.go` fixture entries — the test's job is to prevent regression to legacy terminology, and the fixture needs the legacy word to do that job. (Verify each fixture entry is still active and not stale.)
  - **Pre-condition for landing:** any schema-affecting decision arrived at later in this walkthrough is folded into the same baseline rebase rather than landed as a forward migration. The baseline reflects the final post-walkthrough target schema.
  - **Subsumes:** schema portions of cross-layer #5 (Phase-5 schema renames), schema portions of cross-layer #7 (region → scope). Code-side residue from #5 and #7 is handled separately in those entries.

### 4. `frame_resolution` (template-author YAML) vs `mode` (persisted column) — two names for one value

- **Where:** `code:modeling/node/template.go::TemplateSpec.FrameResolution` ↔ `col:rimsky_frames.mode`. Lookup helper `code:foundation/persistence/postgres/frames.go::LookupFrameMode` bridges by reading `t.spec->>'frame_resolution'` and returning a `FrameMode` Go type.
- **Drift call:** rename artifact (one of the two).
- **Plan:** pick one canonical: either rename YAML to `frame_mode:` (keep `frame_resolution:` alias under pre-v1 break-freely) or rename column to `frame_resolution` (migration). Tracks `tension:frame-resolution-vs-mode-vocabulary`. Adjacent to other naming-split tensions; resolution should be consistent with the chosen direction across cross-layer #1, #3, #6.
- **Decision (2026-05-12 walkthrough):**
  - Canonical name across the data flow: `frame_resolution_mode`.
  - YAML: `frame_resolution_mode: serial_queue` (renamed from `frame_resolution:`).
  - Column: `col:rimsky_frames.frame_resolution_mode` (renamed from `.mode`; absorbed by cross-layer #3 baseline rebase).
  - Go type: `FrameResolutionMode` (renamed from `FrameMode`).
  - Helper: `code:foundation/persistence/postgres/frames.go::LookupFrameResolutionMode` (renamed from `LookupFrameMode`).
  - Reasoning: `frame_resolution` alone is ambiguous (policy vs outcome); `frame_resolution_rule` implies declarative predicate but the values are policy modes; `frame_resolution_mode` is unambiguous and reads cleanly in every surface.

### 5. Phase-5 schema renames — `rimsky_dispatch` → `rimsky_worker_request`, `rimsky_lock_holders` → `rimsky_claim_handle`, `lock_holder_id` → `claim_handle_id`

- **Where:** schema-level renames are complete. Residue in: route name `/dispatches` (control-api cascade-graph backplane); function name `code:foundation/integration/orphan_reaper.go::SweepLockHolders` (operates on `rimsky_claim_handle`); generated-proto comment `protocols/proto/v1/gen/events.pb.go:1512` (auto-generated; reflects source comment); some prose in `.ok-planner/history/` and older sketches; rules-file paths in `.claude/rules/rules.md` still cite `core/queue/...` etc. (pre-Phase-5 directory layout).
- **Drift call:** rename artifact (code identifiers); keep (history/archive).
- **Plan:** sweep code-level identifiers (`/dispatches` route, `SweepLockHolders` function, comments in source `.proto` that feed generated bindings); update `rules.md` paths; leave `.ok-planner/history/` as-is. Tracks `tension:lock-holder-vs-claim-handle-legacy`.
- **Decision (2026-05-12 walkthrough) — SLIMMED (schema portion handled by cross-layer #3 baseline rebase):**
  - Schema-level residue (any remaining migration text, comments tied to old column names) handled by cross-layer #3's migration-baseline rebase.
  - **Code-side residue** sweep stays in this entry:
    - Decide `/dispatches` route — see cross-layer #14 for the explicit decision on the operator-facing route name.
    - Rename `code:foundation/integration/orphan_reaper.go::SweepLockHolders` → `SweepOrphanedClaimHandles`. Mirrors the sibling sweep-name pattern (e.g., `SweepStaleHeartbeats`) — verb + descriptor + table noun; "Orphaned" is explicit about what the sweep targets, not the table itself.
    - Regenerate proto bindings after editing source `.proto` comments — generated artifacts at `protocols/proto/v1/gen/` will refresh automatically via `cmd:make proto-gen`.
    - Update `file:.claude/rules/rules.md` path references that still cite the pre-Phase-5 `core/queue/...` layout to the current `foundation/persistence/...` etc.
  - Leave `.ok-planner/history/` and `.ok-planner/archive/` as-is (workflow scratch; drift is fine).

### 6. YAML `stores:` and `write_semantics:` legacy single-value aliases

- **Where:** `cfg:claim_producers[]` accepts `stores:` alias; `cfg:claim_producers[].write_semantics_envelope` accepts `write_semantics:` single-value shortcut.
- **Drift call:** rename artifact (retire aliases pre-v1).
- **Plan:** retire both aliases; ensure reference configs and sample YAML use the canonical forms only. Tracks `tension:yaml-stores-alias`, `tension:yaml-write-semantics-alias`.
- **Decision (2026-05-12 walkthrough):**
  - Retire `cfg:stores[]` YAML alias (already recorded in cross-layer #1; reaffirmed here for completeness).
  - Retire the `write_semantics:` single-value shortcut. Single canonical shape, one code path. Pre-v1; no operator-continuity cost.
  - **Rename `write_semantics_envelope` → `write_semantics_allowed`** across all surfaces:
    - YAML: `cfg:claim_producers[].write_semantics_allowed: [sync, staged_async]`.
    - Proto: `proto:claim_producer.proto::Capabilities.write_semantics_envelope` → `Capabilities.write_semantics_allowed`. (Note: proto field rename is wire-format-breaking; pre-v1 no consumer pin.)
    - Go: `Capabilities.WriteSemanticsEnvelope` → `Capabilities.WriteSemanticsAllowed` (and any `WriteSemanticsEnvelope` field on internal types).
    - Docs: `concept:write-semantics.md` Boundaries/Invariants describing the operator-declared envelope; rimsky CLAUDE.md "Non-obvious gotchas" entry citing `write_semantics_envelope`.
  - Reasoning: "envelope" is a precise metaphor (the bounding set of allowed values, like a flight envelope), but technically obscure. "allowed" is plain English, captures the operator-policy/permission framing exactly. No metaphor to decode.

### 7. `region` → `scope` rename residue

- **Where:** `scope` is canonical (`col:rimsky_claim_handle.scope_data`, `code:foundation/locks/conflict.go::ScopesByteEqual`). Legacy `region` appears in `code:foundation/locks/conflict.go:14-18` comment (cites "v2's per-store RegionsConflict") and in older prose / `_discover/` sketches.
- **Drift call:** rename artifact (in code comments).
- **Plan:** scrub `region` from current source comments; keep only in historical archive. Tracks `tension:region-vs-scope-legacy`.
- **Decision (2026-05-12 walkthrough) — SLIMMED (no remaining schema work; only code comments):**
  - No schema action needed (column `col:rimsky_claim_handle.scope_data` already renamed; baseline rebase per cross-layer #3 absorbs any final residue at the migration level).
  - **Delete entirely** the `code:foundation/locks/conflict.go:14-18` "v2's per-store RegionsConflict" paragraph. Git log carries the design-evolution history; if the byte-equal rationale is load-bearing for future maintainers, it belongs in `concept:scope`, not in a code comment.
  - Leave `.ok-planner/_discover/` and other workflow scratch as-is.

### 8. Proto service `NodeExecutor` vs Go interface `Executor` vs operator-vocabulary `executor`

- **Where:** `proto:executor.proto::NodeExecutor` (service) vs `code:protocols/executor/executor.go::Executor` (Go interface) vs `concept:executor` (concept slug) vs `executors/` (directory) vs `cmd/rimsky-executor-conformance` (binary). The `Node` prefix appears only at the proto service level.
- **Drift call:** unclear — discuss.
- **Plan:** decide whether to rename `proto:executor.proto::NodeExecutor` → `Executor` for cross-surface symmetry, OR rename the Go interface to `NodeExecutor` for proto-symmetry. Adjacent tensions: `tension:terminal-event-overloaded`, `tension:async-callback-body-key`.
- **Decision (2026-05-12 walkthrough):**
  - Rename `proto:executor.proto::NodeExecutor` → `Executor`. Wire-format-breaking; pre-v1 no consumer pin.
  - Codegen will emit `ExecutorServer`, `ExecutorClient`, etc. into `protocols/proto/v1/gen/` — lives in a different package than the Go interface at `code:protocols/executor/executor.go::Executor`, so no collision.
  - All other surfaces stay (Go interface, concept slug, directory, binary, prose).
  - **Reasoning + Go protocol-naming note:** Standard Go names interfaces for the role of the implementer (`Executor`, `ClaimProducer`, `LifecycleSubscriber`) — no `*Protocol` / `*Provider` / `I*` decoration; single-method interfaces use verb + `-er` (`Reader`, `Stringer`); multi-method use the role noun directly. The current rimsky interfaces are already idiomatic. The proto `NodeExecutor` was the only odd one out; dropping `Node` aligns it with everything else.

### 9. `terminal` overloaded — three different parallel semantics under one word

- **Where:** `proto:executor.proto` declares five terminal events (`Complete | Blocked | Errored | AsyncAccepted | ParkRequested`); not all are "logically terminal" — `AsyncAccepted` is stream-terminal-but-logical-non-terminal, `ParkRequested` is terminal-but-resumable.
- **Drift call:** unclear — discuss.
- **Plan:** either rename the wire-level term (e.g. "stream-closing events") and reserve "terminal" for logical-terminal, or tabulate the three parallel semantics inline in the proto/docs. Tracks `tension:terminal-event-overloaded`.
- **Decision (2026-05-12 walkthrough) — PROTO RESTRUCTURE (heavy, two-axis):**
  - Restructure `proto:executor.proto::ExecuteEvent` to make channel-mechanics and outcome explicit and orthogonal. New shape:

    ```protobuf
    message ExecuteEvent {
      oneof event {
        Heartbeat heartbeat = 1;
        NamedEvent named_event = 2;
        StreamClose stream_close = 3;        // closes the gRPC stream; carries the outcome
      }
    }

    message StreamClose {
      oneof outcome {
        Success success = 1;
        Error error = 2;                       // all error variants — error_class discriminates
        Snooze snooze = 3;                     // first-class non-error operational pause
        AwaitAsyncCallback await_async = 4;    // async handoff to webhook
      }
    }

    message AsyncCallbackBody {
      oneof outcome {
        Success success = 1;
        Error error = 2;
        Snooze snooze = 3;
        // No AwaitAsyncCallback — webhook is the second half; can't chain another.
      }
    }

    // Outcome messages shared between StreamClose and AsyncCallbackBody:
    message Success            { /* attributes_delta etc. */ }
    message Error              { string error_class = 1; /* error_message, attributes_delta etc. */ }
    message Snooze             { google.protobuf.Timestamp resume_at = 1; /* etc. */ }
    message AwaitAsyncCallback { string async_ack_id = 1; /* deadline etc. */ }
    ```

  - **Vocabulary split.** "Terminal" is no longer a wire-protocol term. The wire-level term for "the last event on a reporting channel" is `StreamClose` (gRPC stream) or just "the outcome" (HTTP webhook body). "Terminal" stays for the state-machine + decision-engine sense (`concept:terminal-resolution`, `code:foundation/integration/terminal_decision.go::*`, `concept:node-state` "terminal state" property).
  - **Folded into Error.** `Blocked` and the never-implemented `Retry` collapse into `Error{error_class: ...}`. Operator-declared `error_types` policy is the single decision surface for error routing (retry / invalidate / give_up / pass). Executors emit `Error{error_class: rate_limited}` or `Error{error_class: transient_io}` etc.; rimsky maps class → action.
  - **Snooze is first-class.** `ParkRequested` → `Snooze`. Carries the non-error metaphor explicitly ("wake me later"). Node state `parked` → `snoozing`. CLAUDE.md "Vocabulary" updates accordingly.
  - **Lifecycle-handler slots drop from 4 to 3.** `on_executor_blocked` folds into `on_executor_errored` (all error_class variants go through one handler; error_types policy discriminates). Touches `concept:lifecycle-handler` Boundaries + Invariants; resolves `tension:blocked-vs-errored-routing`; effectively re-resolves `tension:_resolved/handler-slot-count-drift` (the slot-count claim drops from 4+on-event-handler to 3+on-event-handler).
  - **Resolves:** `tension:terminal-event-overloaded`, `tension:blocked-vs-errored-routing`. Touches `concept:executor`, `concept:terminal-resolution`, `concept:lifecycle-handler`, `concept:parked-state` (renamed), `concept:node-state` (state-name update), `concept:error-policy`.
  - **Implementer churn:** every executor implementation updates to the new shape — `executors/claude-agent/`, `executors/http-node/`, `executors/stub/`, conformance fixtures at `cmd/rimsky-executor-conformance`, smoke fixture at `test/smoke/`. Wire-format-breaking; pre-v1 no consumer pin.
  - **Not yet decided here:** the precise field shape of each outcome message (e.g., what fields go on `Error` vs the existing per-variant fields). Brainstorm pass will detail.

### 10. `cascade` covers two distinct walks (cascade-on-terminal stale-mark vs pure-cascade fresh-roll)

- **Where:** `code:foundation/integration/cascade_invalidate.go` (cascade-on-terminal) vs `code:foundation/integration/cascade_recalculate.go` (pure-cascade). Both live under `concept:cascade`.
- **Drift call:** unclear — discuss.
- **Plan:** options: rename one in prose ("cascade-propagation" vs "cascade-recalculation"); split into two concepts; keep one name and clarify inline at each call site. Tracks `tension:cascade-walks-overloaded`.
- **Decision (2026-05-12 walkthrough) — three-word vocabulary inside one umbrella concept:**
  - Adopt three-word vocabulary for the cascade machinery:
    - **walk** — scheduler-tick-driven traversal of the graph (topology-ordered). The mechanism.
    - **propagation** — cascade-of-stale on `fresh_changed`; mark dependents stale and recurse. Driven by `code:foundation/integration/cascade_invalidate.go::InvalidateNode` (handler for the graph-level `invalidate` message — `concept:invalidate`).
    - **fallthrough** — no-dispatch fresh-roll on `pure_cascade`; roll fresh state forward without running the node. Detected by `code:foundation/integration/cascade_recalculate.go::RecalculateNode` (per-node logic; comment at lines 29-33), executed by the scheduler's pure-cascade sweep.
  - **Keep `concept:cascade` as the umbrella.** Rewrite the concept doc body to use the three-word vocabulary uniformly. The current "two walks" framing dissolves into "one walk; two node-level behaviors (propagation, fallthrough)."
  - **Do not split into multiple concept files.** Three concept files would over-structure for what is genuinely one mechanism with named sub-behaviors. Vocabulary inside the umbrella is sufficient.
  - **Code-level vocabulary alignment.** Refresh doc comments and variable/local names where current prose says "cascade" ambiguously. E.g., the pure-cascade sweep's doc comment uses "executes cascade fallthrough on nodes whose recalculate detected all-deps-fresh + no-executor."
  - **File renames not needed.** `cascade_invalidate.go` and `cascade_recalculate.go` correctly describe their entry messages (invalidate, recalculate). Renaming to `cascade_propagation.go` / `cascade_fallthrough.go` would obscure that mapping. Keep file names; refresh comments.
  - **Resolves:** `tension:cascade-walks-overloaded`.

### 11. `transition-reason` vs `last-outcome` overlapping vocabularies

- **Where:** `code:foundation/cascade/state.go::TransitionReason` (audit enum) and `col:rimsky_nodes.last_outcome` (cascade-fire gate). Both describe "what just happened" with overlapping but distinct vocabularies, in different columns, for different consumers.
- **Drift call:** unclear — discuss.
- **Plan:** options: tabulate both side-by-side with explicit "use X here, use Y here" guidance; cross-link annotations in `state.go`; or collapse to one vocabulary (loses split benefit). Tracks `tension:transition-reason-vs-last-outcome`.
- **Decision (2026-05-12 walkthrough) — both A + B (tabulate in docs + cross-link in code):**
  - The two enums are complementary, not duplicative: `transition_reason` is the audit-grade "why did this transition happen" (finer grain; covers transitions with no outcome like `OperatorReset`, `HeartbeatLost`, `Invalidate`); `last_outcome` is the cascade-decision input (`fresh_changed` / `fresh_unchanged` / `passed` / `pure_cascade` / `failed`). Keep the split.
  - **Concept docs.** Add a "Relationship to sibling concept" subsection to both `concept:transition-reason` and `concept:last-outcome` with a short table showing the typical pairings (e.g., when `transition_reason = HandlerComplete` and handler resolved by_changed → `last_outcome` is `fresh_changed` or `fresh_unchanged`; when `transition_reason = OperatorReset` → `last_outcome` is unchanged from prior run). One table per concept doc, cross-linked.
  - **Code annotations.** `code:foundation/cascade/state.go::TransitionReason` declaration gets a `@concept: transition-reason` annotation plus a short pointer comment ("sibling to `last_outcome` — see concepts/transition-reason.md Relationship section"). Same on the `last_outcome`-writing call sites.
  - **Resolves:** `tension:transition-reason-vs-last-outcome`.
  - No code behavior changes; readability only.

### 12. `kind` vs `type` body-key wire footgun (async-callback)

- **Where:** async-callback POST body must be keyed `type` (not `kind`). The supervisor's chi route enforces, but with a generic error message. Tested in `executors/claude-agent/src/server.test.ts`.
- **Drift call:** unclear — discuss.
- **Plan:** options: accept both; improve chi route's error message; publish a documented JSON Schema. Tracks `tension:async-callback-body-key`.
- **Decision (2026-05-12 walkthrough) — DEFERRED to brainstorm; likely superseded by #9 restructure:**
  - The wire-level `kind` vs `type` discriminator may disappear entirely after #9's proto restructure. The new `StreamClose` + outcome `oneof` (and the parallel `AsyncCallbackBody` outcome `oneof`) eliminates the need for a top-level `kind`/`type` discriminator key — protobuf's JSON encoding for `oneof` produces something like `{ "outcome": { "success": {...} } }` with no competing discriminator at the body root.
  - The internal `col:rimsky_events.kind` column is unaffected by the wire-level restructure; it stays as rimsky-internal vocabulary. No naming conflict with the wire shape post-restructure.
  - **Action:** revisit in the brainstorm pass. If, after #9 restructure, a residual question remains about the rimsky-internal `kind` column wording, scope it narrowly there. For now, mark as superseded-pending.
  - `tension:async-callback-body-key` will likely resolve to "superseded by `tension:terminal-event-overloaded` + proto restructure" once the brainstorm formalizes the new shape.

### 13. Concept-doc table-name singular vs schema plural

- **Where:** Several concept docs cite schema tables in singular form (`rimsky_frame`, `rimsky_event` etc.) while the actual schema uses plural (`rimsky_frames`, `rimsky_events`). Some are correct singular (`rimsky_claim_handle`, `rimsky_worker_request`); some are plural (`rimsky_frames`, `rimsky_schedules`, `rimsky_nodes`).
- **Drift call:** unclear — discuss (it's a small inconsistency at the schema level).
- **Plan:** pre-v1, options are: standardize all rimsky tables to singular OR plural OR live with the existing mix. The mix is a function of when each migration was written, not a design decision.
- **Decision (2026-05-12 walkthrough) — standardize on plural:**
  - All rimsky tables become plural. Matches the dominant Postgres convention in the broader ecosystem (Rails, Django, SQLAlchemy, most modern ORMs default to plural). Already the precedent for most rimsky tables (`rimsky_frames`, `rimsky_schedules`, `rimsky_nodes`, `rimsky_instances`, `rimsky_events`, etc.).
  - Renames absorbed by cross-layer #3's migration-baseline rebase:
    - `rimsky_claim_handle` → `rimsky_claim_handles`
    - `rimsky_worker_request` → `rimsky_worker_requests`
    - `rimsky_claim_holders` (already plural ✓)
    - `rimsky_lifecycle_idempotency` → `rimsky_lifecycle_idempotencies` (verify wording)
    - Any other currently-singular tables sweep to plural.
  - Concept-doc cross-references: update every `concepts/*.md` to use the new canonical plural names. Pre-existing typo "rimsky_frame" / "rimsky_event" type prose gets corrected at the same time.
  - Go-side identifiers may keep singular for the row struct (`NodeRow`, `FrameRow`, `ClaimHandleRow`) — Go convention is to name the row type singularly even when the table is plural. Document this in `concept:persistence-driver` or wherever the row-struct convention lives.
  - Resolves the singular-vs-plural concept-doc inconsistency directly.

### 14. `dispatches` route on control-api still carries the legacy `dispatch` noun

- **Where:** `route:GET /dispatches` (control-api cascade-graph backplane). Underlying table is `rimsky_worker_request` post-Phase-5.
- **Drift call:** rename artifact.
- **Plan:** rename to `/worker-requests` (consistent with the post-Phase-5 table noun) OR keep as colloquial route name. Cross-cuts cascade-graph routes and the control-api version-prefix concern (`tension:control-api-version-prefix`). Pre-v1 — break freely.
- **Decision (2026-05-12 walkthrough) — COLLAPSED into the `worker-request` → `node-run` rename:**
  - The underlying table is renamed: `rimsky_worker_request` → `rimsky_node_runs` (plural per #13; "node-run" = "one execution of one node within a frame"; `concept:frame` stays as "one run of the cascade," preserving the hierarchical model frame ⊃ node-run).
  - **Route name follows the table:** `route:GET /dispatches` → `route:GET /node-runs`.
  - Surface rename across the codebase:
    - `concept:worker-request` → `concept:node-run` (concept-file rename + concept-doc body uses "node-run" throughout)
    - `code:foundation/persistence/types.go::WorkerRequestRow` (or equivalent) → `NodeRunRow`
    - Go variable names: `workerRequest` → `nodeRun`, `workerRequestID` → `nodeRunID`
    - Sweep all `worker_request` / `worker-request` references in concept docs, CLAUDE.md "Vocabulary," and CHANGELOG-worthy prose
  - **Behavior unchanged.** `phase` column values stay (`pending`/`active`/`held`/`completed`). Orphan reaper lifecycle stays. Held-claim semantics stay.
  - Adds natural future-route hierarchy if dashboards want both views: `/frames` (cascade-level runs) and `/node-runs` (node-level runs within frames). Adding `/frames` is out of scope for this rename pass; flagged for the brainstorm.
  - **Resolves:** the `/dispatches` legacy-prose tension and replaces the vague `worker_request` noun with one that maps directly to "one execution of one node within a frame."

### 15. `Capabilities` naming inconsistency between claim-producer and executor

- **Where:** `ClaimProducer.Capabilities()` is part of the 4+1-verb startup handshake; `ExecutorObservability.GetCapabilities` is the same conceptual thing but verbed differently (`GetCapabilities` vs `Capabilities`). The discovery-cache populates from both. Minor; mostly cosmetic.
- **Drift call:** unclear — discuss.
- **Plan:** harmonize the verb (likely `GetCapabilities` for both) OR keep the divergence and note it. Pre-v1 — break freely.
- **Decision (2026-05-12 walkthrough) — harmonize on bare `Capabilities`:**
  - Rename `proto:executor.proto::ExecutorObservability.GetCapabilities` → `Capabilities` (drop the `Get` prefix). Aligns with the claim-producer side and with the rimsky-vocabulary "4 verbs + Capabilities() startup handshake" framing.
  - Reasoning: the capabilities call is a one-shot startup handshake, not a CRUD-style fetch. `Get*` prefixes apply to data RPCs where the prefix disambiguates from create/update/delete; a handshake isn't really a "get."
  - **Optional Go-side cleanup (brainstorm-pass scope):** factor out a shared `CapabilitiesProvider` interface and embed it in each peer protocol (`ClaimProducer`, `Executor`, `LifecycleSubscriber`). One Go-source-of-truth for the handshake signature; the discovery-cache code can accept the embedded type and work uniformly. **Proto-side:** each service still declares the `Capabilities` RPC explicitly — proto3 has no service inheritance — so the proto-side repetition is unavoidable.
  - Cost: one .proto edit + bindings regen + executor implementations update (claude-agent, http-node, stub) + discovery-cache call-site update + docs sweep ("4 verbs + Capabilities()" framing).
  - Pre-v1 wire-format-breaking; no consumer pin.

### 16. Drop `concept:held-claim`; fold into `concept:claim-handle` (row variant + authoring pattern)

- **Where:** `concept:held-claim` is a standalone concept in `.ok-planner/design/concepts/held-claim.md`. Sibling concepts: `concept:claim` (protocol abstraction), `concept:claim-handle` (persistence row), `concept:auto-terminal` (runtime mechanism). Surfaced during walkthrough.
- **Drift call:** drop concept (over-structured).
- **Insight:** "held" is not a different KIND of claim — at the producer level, all claims look identical (same `Open` / `Commit` / `Abandon` / `Release` verbs). "Held" is a tri-fold property:
  1. An **authoring decision** in the template (mark downstream nodes as inheritors → the claim becomes held through them).
  2. A **row flag** at runtime (`col:rimsky_claim_handles.is_held = TRUE` post-#13 plural rename).
  3. A **different lifecycle mechanism** (auto-terminal fires verbs at holding-subgraph completion instead of at the acquirer's terminal; per-member state tracked in `table:rimsky_claim_holders`).
- **Plan — distribute content:**
  - **Row-variant** (`is_held=TRUE` + what the column means + per-member state in `table:rimsky_claim_holders`) → folds into `concept:claim-handle`. The concept doc already mentions the column at line 20; expand to a brief "Held variant" subsection.
  - **Authoring pattern** (how a template declares inheritors → claim becomes held) → folds into `concept:claim-handle` (or `concept:template` if it fits better there) as an "Authoring: held vs unheld" subsection.
  - **Runtime mechanism** (auto-terminal fires at holding-subgraph completion) — already lives in `concept:auto-terminal`. No move needed.
- **After fold:**
  - `concept:claim` — protocol-layer abstraction (unchanged).
  - `concept:claim-handle` — persistence row (gains held-variant + authoring subsections).
  - `concept:auto-terminal` — runtime mechanism for held resolution (unchanged).
  - `concept:held-claim` — **dropped**; references in other concept docs (e.g., `concept:claim-handle` Adjacent currently lists `held-claim`) sweep to point at `concept:claim-handle#held-variant` or `concept:auto-terminal` depending on context.
- **Touches:** `concept:claim-handle` (gain content), `concept:auto-terminal` (verify cross-links updated), Adjacent lists across other concepts that reference `held-claim`. Concept count drops by 1 (46 → 45 if no other promotions; but #18 adds `concept:service`, so net 46).

### 17. Rename `concept:opacity` → `concept:inertness`; verify `concept:userdata` purpose is explicit

- **Where:** `concept:opacity` is the current umbrella for four inert streams (`userdata`, `claim` payload/address/scope, blob content, named-event payloads). `@blessed-invariant 11` says "Userdata is opaque to Rimsky"; `@blessed-invariant 20` and `21` say "Claim content is inert in Rimsky" / "Blob content is inert in Rimsky." Vocabulary mismatch at the invariant level (11 uses "opaque"; 20+21 use "inert"). Surfaced during walkthrough.
- **Drift call:** rename concept (`opacity` → `inertness`); reinforce sibling concept (`concept:userdata` purpose).
- **Insight:** "Opacity" implies byte-opaque (no traversal). But rimsky DOES traverse some of these streams structurally for transport mechanics — userdata via `code:modeling/shared.DeepMergeJSON` deep-merge; attribute values + named-event payloads via `code:modeling/attribute/substitution.go::walkPath` at substitution-leaf extraction. The accurate framing is **inertness**: rimsky doesn't *act on* contents for its own logic, but it may *traverse structure* for transport. Two sub-disciplines under the umbrella:
  - **Byte-opaque inertness** — rimsky never traverses (claim scope/address/payload, blob bytes).
  - **Structural inertness** — rimsky may traverse for transport mechanics but doesn't inspect values (userdata, attribute values, named-event payloads).
- **Plan:**
  - **Rename `concept:opacity` → `concept:inertness`** (or `concept:inert-data` — either works; `concept:inertness` is shorter). Concept doc gets a new body describing the two sub-disciplines and listing the five inert streams.
  - **Reword `@blessed-invariant 11`** text from "Userdata is opaque to rimsky" to "Userdata is inert in Rimsky" — aligns with `@blessed-invariant 20` and `21` framing. The invariant's discipline is unchanged; only the wording changes.
  - **Keep `concept:userdata` as a first-class concept.** It exists today; user note: "easy to forget the purpose of." The concept doc should emphasize userdata's PURPOSE (escape-hatch for executor-specific config that rimsky should not need to learn about — synthetic-blocker scenarios, per-run trace artifacts, ad-hoc tuning, per-instance overrides via `userdata_overrides`). The inertness discipline is a property cross-linked to `concept:inertness`.
  - **Cross-link bidirectionally:** `concept:inertness` lists `concept:userdata` (and `concept:claim`, `concept:blob-backend`, `concept:named-event`, `concept:attribute`) in Adjacent; each of those concepts lists `concept:inertness` in Adjacent.
- **Touches:** `concept:opacity` (rename file + body rewrite), `concept:userdata` (reinforce purpose subsection), `@blessed-invariant 11` text (reword), Adjacent lists in all five owning concepts.
- **Net concept count:** unchanged at 46 (rename, not promotion or drop).

### 18. "peer" vocabulary → "service"; promote `concept:service` as umbrella

- **Where:** "peer" is used 6 times in CLAUDE.md, 2 times in `file:docs/glossary.md`, and scattered as prose across 10+ concept doc bodies (`concept:claim-producer`, `concept:executor`, `concept:cascade-graph`, `concept:discovery-cache`, `concept:control-api`, `concept:invalidate`, `concept:conformance`, `concept:observability`, `concept:lifecycle-subscriber`, `concept:rimsky-yml`). Surfaced during the walkthrough — not in the initial audit.
- **Drift call:** rename artifact (prose only) + promote new concept.
- **Plan:**
  - Sweep "peer" → "service" across CLAUDE.md prose, concept doc bodies, `file:docs/glossary.md`, and any other rimsky-internal documentation. Plain "service" (Option A) — context disambiguates from rimsky's internal binaries (`rimsky-supervisor`, `rimsky-scheduler`, `rimsky-control-api`), which are rarely called "services" in prose.
  - **Promote `concept:service`** as a new umbrella concept. Brief content:
    - **Definition:** an out-of-process gRPC binary that implements one or more rimsky service protocols and is orchestrated by rimsky.
    - **Purpose:** extensibility (third-party implementations) and modularity (reference impls separate from rimsky core).
    - **Boundaries:** the specific protocols (`concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, blob-backend) are sibling concepts. Orchestration mechanics (dispatch, acquisition, supervisor coordination) are rimsky-internal and live in their own concepts (`concept:supervisor`, `concept:terminal-resolution`, etc.). The service concept owns: how a binary declares its protocol membership in `cfg:rimsky.yml`, the Capabilities startup handshake (post-#15), and conformance-validation entry points.
    - **Invariants:** declared in `cfg:rimsky.yml` with explicit `protocols: [...]` list per service; protocol membership advertised via the per-protocol `Capabilities` RPC at startup; per-protocol conformance-binary validates compliance.
    - **Adjacent:** executor, claim-producer, lifecycle-subscriber, blob-backend, rimsky-yml, conformance, observability, discovery-cache.
  - **Rationale:** "peer" implies equivalence/symmetry (peer-to-peer networking sense), but rimsky's relationship to executors/stores is orchestrator-to-orchestrated, not peer-to-peer. "Plugin" implies co-packaging; these are independent binaries. "Service" matches the gRPC-native vocabulary and accurately describes the role.
  - **YAML stays as-is:** `cfg:rimsky.yml` keeps protocol-specific blocks (`claim_producers:`, `executors:`) — the umbrella concept describes the category, but operator config disambiguates by protocol. No `services:` block.
  - **Touches:** every concept doc that uses "peer" in prose, CLAUDE.md "What this repo is" and "Non-obvious gotchas" sections, `file:docs/glossary.md`, and possibly variable / function names in Go code that include `peer` (sweep with grep).

### 19. Rename `modeling/` layer → split into `graph/` + `control/` (two-way layer reorganization)

- **Where:** The current root-module directory `modeling/` lumps together two distinct concerns:
  - **Graph / runtime concerns** (what authors write, what runs): `concept:template`, `concept:instance`, `concept:tag`, `concept:node`, `concept:attribute`, `concept:userdata`, `concept:lifecycle-handler`, `concept:on-event-handler`, `concept:named-event`, `concept:error-policy`, `concept:quality-rule`, `concept:frame`, `concept:schedule`, `concept:invalidate`. Plus runtime engines: `code:modeling/frame/`, `code:modeling/scheduler/` (or wherever the scheduler tick lives), `code:modeling/attribute/`, `code:modeling/qualityrule/`.
  - **Control / observability concerns** (operator-facing surfaces): `concept:control-api`, `concept:cascade-graph`, `concept:discovery-cache`, `concept:rimsky-cli`, `concept:rimsky-yml`, `concept:observability`. Plus their implementation directories: `code:modeling/controlapi/`, `code:modeling/cli/`, `code:modeling/observability/`, `code:modeling/config/`.
  - "Modeling" is too vague — it doesn't capture what either of these groups actually is. Surfaced during walkthrough.
- **Drift call:** rename + restructure layer.
- **Plan:**
  - **Two-way split.** Rename `modeling/` → `graph/`; create new `control/` sibling directory under the root module. Concept-doc layer organization mirrors the split.
  - **Single Go module preserved.** Both `graph/` and `control/` stay inside the root Go module — no new `go.mod`. Cross-references where genuinely needed (e.g., `control/control-api` reading `graph/frame` state for dashboard endpoints) stay free of depguard exception lists.
  - **Layer contents:**
    - **`graph/`**: `template/`, `node/`, `tag/` (if any), `instance/`, `frame/`, `scheduler/`, `attribute/`, `qualityrule/`, plus all per-node behavior subpackages (lifecycle-handler, on-event-handler, named-event, error-policy).
    - **`control/`**: `controlapi/`, `cli/`, `observability/` (the handshake + discovery cache machinery), `config/` (rimsky.yml loading).
  - **Concept docs:** the design log's layer headings reorganize from "Layer 3: `modeling/`" into two new headings — "Layer 3: `graph/`" and "Layer 4: `control/`" (or rebalance numbering across all layers).
  - **Boundary contract** (small + clean, satisfies the "operated on independently" bar):
    - `control` → `graph`: read access via persistence-driver queries; small mutation set (create instance, force-fire schedule, register/deploy/undeploy template).
    - `graph` → `control`: zero. Graph never calls into control. Control is one-way (operator → rimsky).
  - **Why two-way and not three-way (graph / runtime / control)?** The graph-runtime split fails the "independent operation" bar: every node-spec field is consumed at runtime; every runtime decision reads graph state. `rimsky_nodes` carries both definition AND runtime state in the same row. A genuinely clean runtime layer would also require lifting runtime mechanics out of foundation (which already owns supervisor dispatch + terminal handling + orphan reapers + cascade engine) — a much bigger structural pass worth its own design effort, not this audit.
- **Touches:** every directory currently under `modeling/`; depguard rules in `.golangci.yml`; import paths across the root module; CLAUDE.md "Package import rules" section; concept-doc layer headings; the audit file itself (this entry's layer-list cites need updating after the split).
- **Net structural change:** the three-Go-module architecture is preserved (foundation / protocols / root). What changes is the root module's internal directory organization. Concept count stays at 46 (no concept promotions or drops from this rename — content stays, just reorganized).

---

## Summary statistics

### Pre-walkthrough (initial audit pass)

- **Total concepts inventoried:** 46
- **Aligned (no action):** 28
- **Rename artifact:** 4 (at concept-row level; many more rename targets surface in cross-layer concerns)
- **Rename concept:** 0
- **Keep — layer-appropriate:** 2 (`stores/` directory, `executors/` directory under bundled services)
- **Unclear — discuss:** 12 (notably: `cascade`, `executor`, `error-policy`, `cascade-graph`, `persistence-driver`)
- **Cross-layer concerns identified:** 15

### Post-walkthrough (2026-05-12)

All cross-layer concerns and concept-row entries received explicit decisions during the walkthrough. The audit is now ready to be converted into a brainstorm-pass spec.

**Decisions by category:**

- **15 cross-layer concerns** — all decided. Headline outcomes:
  - **#1** retires `Store = ClaimProducer` alias + `Store`-substring residue at foundation/protocol layer; keeps `stores/` at bundled-services layer.
  - **#2** renames persistence-side `Store` umbrella → `Driver` (matches concept slug).
  - **#3 (broadened)** collapses the migration chain into a single `001-baseline.sql`, absorbing schema-rename residue from #3, #5 (schema portion), #7 (schema portion), #13 (singular→plural), #4 (column rename), #14 (table rename). Operational discipline: hard reset of dev Postgres.
  - **#4** canonicalizes on `frame_resolution_mode` across YAML, column, Go type, helper.
  - **#5 (slimmed)** sweeps code-side schema-rename residue (route `/dispatches`, `SweepLockHolders` → `SweepOrphanedClaimHandles`, `.proto` comments).
  - **#6** retires YAML aliases + renames `write_semantics_envelope` → `write_semantics_allowed`.
  - **#7 (slimmed)** deletes the legacy `region` comment in `code:foundation/locks/conflict.go`.
  - **#8** renames `proto:executor.proto::NodeExecutor` → `Executor`.
  - **#9 (proto restructure)** introduces `StreamClose` + outcome `oneof`; folds `Blocked`/`Retry` into `Error{error_class}`; promotes `Snooze` to first-class non-error; lifecycle-handler slot count drops from 4 to 3.
  - **#10** adopts three-word vocabulary inside `concept:cascade`: walk / propagation / fallthrough.
  - **#11** documents the `transition-reason` vs `last-outcome` relationship in both concept docs + cross-link annotations in code.
  - **#12** deferred to brainstorm (likely superseded by #9's wire restructure).
  - **#13** standardizes all rimsky tables on plural.
  - **#14** renames `rimsky_worker_request` → `rimsky_node_runs`; concept `concept:worker-request` → `concept:node-run`; route `/dispatches` → `/node-runs`. Frame stays the higher-level "run of the cascade"; node-run is the per-node execution within a frame.
  - **#15** renames executor `GetCapabilities` → `Capabilities`; optional Go-side `CapabilitiesProvider` factor flagged for brainstorm.
- **5 concept-level unclear-discuss entries** — all resolved (4 collapse into cross-layer decisions; 1 — `concept:error-policy` — gets its own decision: rename code file + function, keep concept slug + YAML field).
- **3 concept-level rename-artifact entries** — all collapse into cross-layer decisions (#1, #1, #4+#13).
- **8 aligned-with-caveat entries** — 6 resolved by cross-layer decisions; 1 (`concept:terminal-resolution`) confirmed aligned by design as the umbrella concept; 1 (`concept:conformance`) gets its own decision: rename `rimsky-executor-conformance` → `rimsky-executor-conformance` for symmetry; probe binary stays generic.

**New tensions surfaced during walkthrough:**

- `tension:executor-conformance-binary-asymmetry` — resolved in this audit (rename to `rimsky-executor-conformance`).
- Lifecycle-handler slot count changes again from 4 to 3 after #9's restructure; re-resolves `tension:_resolved/handler-slot-count-drift` with the new shape.

**Existing tensions resolved (directly):**

- `tension:store-vs-claim-producer-vocabulary` (cross-layer #1)
- `tension:yaml-stores-alias` (cross-layer #1, #6)
- `tension:yaml-write-semantics-alias` (cross-layer #6)
- `tension:frame-resolution-vs-mode-vocabulary` (cross-layer #4)
- `tension:lock-holder-vs-claim-handle-legacy` (cross-layer #3 baseline + #5 sweep)
- `tension:consumer-key-vs-instance-key` (cross-layer #3 baseline)
- `tension:region-vs-scope-legacy` (cross-layer #3 baseline + #7 comment delete)
- `tension:terminal-event-overloaded` (cross-layer #9 proto restructure)
- `tension:cascade-walks-overloaded` (cross-layer #10 vocabulary)
- `tension:transition-reason-vs-last-outcome` (cross-layer #11)
- `tension:blocked-vs-errored-routing` (cross-layer #9 collapse)

**Existing tensions deferred to brainstorm:**

- `tension:async-callback-body-key` (likely superseded by #9 — confirm in brainstorm)
- `tension:control-api-version-prefix` (separate concern; out of nomenclature scope)
- `tension:error-action-count-drift` (revisit post-#9 collapse)
- `tension:stub-mode-runtime-only-gate`, `tension:blob-backend-conformance-fixture-asymmetry`, `tension:stub-mode-signature-no-proto-surface` (conformance-side; out of nomenclature scope)

### Notes on counts

The "Rename artifact" count is low at the per-concept level because most rename targets are cross-cutting and show up in the cross-layer concerns section instead of as a single concept-level call. The cross-layer plan (especially #1, #2, #4, #5, #6) is where the bulk of the actual rename work lives.

Top-five cross-layer concerns by importance (initial read; subject to walkthrough):

1. **#1 (`Store = ClaimProducer` and its `Store`-substring residue)** — the canonical alias-retirement target; touches type alias, struct field, proto field, observability proto service, sanctioned-introspection helper, and YAML alias.
2. **#2 (persistence-side `Store` collides with claim-producer `Store`)** — fixing it is half of resolving #1 cleanly; without this, the noun `Store` continues to mean two things.
3. **#5 (Phase-5 schema rename residue)** — `/dispatches` route, `SweepLockHolders` function, generated-proto comment text. Visible to operators and integrators.
4. **#4 (`frame_resolution` vs `mode`)** — surface that template-authors and operators both touch routinely; the friction accumulates.
5. **#8 (`NodeExecutor` proto vs `Executor` Go/concept)** — cross-surface asymmetry on a load-bearing wire protocol noun. Smaller blast radius than #1 but very visible to third-party executor authors.

### Concepts where layer placement was non-obvious

- `concept:claim` and `concept:claim-handle` are sibling protocol-layer / persistence-layer nouns for the same thing. Placed both under foundation (the persistence-side `claim-handle` is foundation-owned; the protocol-side `claim` types live in `protocols/claimproducer/` but are re-exported through `foundation/locks/`).
- `concept:observability` spans protocol bindings (`protocols/proto/v1/*_observability.proto`) and the modeling-layer handshake/cache. Placed under protocols (the protocol surfaces are the load-bearing artifact).
- `concept:cascade-graph` is HTTP-route concept owned by `modeling/controlapi/` but conceptually serves rimsky-internal state. Placed under modeling.
- `concept:terminal-resolution` is umbrella across foundation (stages 1-2, 5) and modeling (handler resolution in lifecycle-handler). Placed under foundation since the orchestration spine is foundation-side.
- `concept:opacity` is cross-cutting; no clear layer home — listed under foundation alongside the persistence-side opaque streams it governs.
