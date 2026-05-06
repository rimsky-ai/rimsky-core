# Layer Crystallization

**Status:** Design draft, 2026-05-04.
**Scope:** A single coordinated architectural reshape of Rimsky into four crisply-layered concerns (foundation, modeling, service protocols, bundled services + examples), with Go module enforcement of the foundation/protocols boundaries, comprehensive contracts for foundation and modeling layers, settled nomenclature, internal consolidations, and a phased implementation sequence.
**Authority:** Once landed, the contracts produced by Phase 1 (foundation, modeling, service-protocol) become authoritative until v1. Ad-hoc amendment is forbidden.
**Delivery:** This spec produces a single sequenced implementation, organized into 7 architectural phases. The phasing describes *what* gets built in what order; delivery process (commits, PRs, branches) is out of scope.

---

## 1. Purpose

Rimsky is conceptually a reactive computation graph integrated with a lock manager, plus a modeling layer (templates, instances, frames, schedules, attributes), plus a set of pluggable services (claim producers, executors, lifecycle subscribers), plus bundled reference implementations and examples. These layers exist conceptually but leak into each other in code: package boundaries are aspirational, vocabulary is ambiguous between layers, and the service-protocol surface is shaped by historical accident rather than intent.

Pre-v1 status gives us permission to make a single coordinated architectural cut that:

1. **Crystallizes the foundation layer** — the small, stable core (cascade engine + lock manager + integration). Compile-enforced as a separate Go module.
2. **Crystallizes the modeling layer** — comprehensive contract treatment for templates, instances, frames, schedules, attributes, control-plane API, public vocabularies. Single source of truth.
3. **Crystallizes the service-protocol layer** — three distinct wire protocols (`ClaimProducer`, `Executor`, `LifecycleSubscriber`), each with a clear purpose, in a separately-importable Go module so external service authors don't pull rimsky's transitive deps.
4. **Settles nomenclature** — `region` → `scope`, protocol-level "store" → `ClaimProducer` (services-layer "store" survives for data-backed colloquial), foundation subsystem names (`cascade`, `locks`, `integration`).
5. **Consolidates internal mechanisms** — worker-request bookkeeping (currently split across `rimsky_dispatch` and `rimsky_lock_holders`), orphan reapers (currently two), terminal-decision modules (currently two).

The result: future work focuses on building services, examples, and product features against a stable foundation and modeling layer that don't need re-litigating.

## 2. Goals & non-goals

### Goals

- Produce three durable contract documents (foundation, modeling, service-protocol) that survive until v1 unchanged.
- Enforce the foundation/protocols boundaries via Go module structure, not just package conventions.
- Settle vocabulary so subsequent product work reads as one coherent system, not the geological record of multiple redesigns.
- Eliminate parallel-but-distinct internal mechanisms in favor of single sources of truth.
- Leave the bundled-services layer (filesystem store, postgres store, claude-agent, http-node) functionally unchanged — only renamed/restructured to align with the new layer boundaries.

### Non-goals

- New product features (no new claim producers, executors, or modeling capabilities).
- v1 feature freeze. This spec gets us to a state where v1 can be a deliberate later decision; it does not declare v1.
- External-library shipping of the foundation as a published Go module. The split *enables* that consumption pattern but isn't motivated by it.
- Performance optimization. Schema and code consolidation may incidentally improve performance; that's a side effect, not a goal.
- Migration tooling for production data. Pre-v1 break-freely applies — schema reshapes happen via drop-and-recreate.

## 3. Decisions summary

The brainstorm settled the following twelve decisions; this section is the canonical record:

| # | Decision | Choice |
|---|----------|--------|
| 1 | Spec scope | Architectural reshape + nomenclature + Go module split, all in one spec |
| 2 | Module architecture | γ — three modules: `foundation/`, `protocols/`, root |
| 3 | "Store" rename | Layered: protocol = `ClaimProducer`; services-layer keeps "store" for data-backed |
| 4 | Worker-request consolidation | Aggressive — one logical worker-request abstraction |
| 5 | Modeling-layer contract scope | Comprehensive — full contract treatment for all modeling subsystems |
| 6 | Public vocabulary | Keep `fresh`/`stale`/`running`/`failed`, `invalidate`/`recalculate`, `retry`/`invalidate`/`give_up`; rename `region` → `scope` |
| 7 | Write-semantics location | Envelope + per-claim, with byte-equal-scope uniformity constraint |
| 8 | `LifecycleSubscriber` granularity | One service with 6 methods; return `nil` for non-react |
| 9 | Foundation subsystem package names | `cascade`, `locks`, `integration` |
| 10 | Disposition of existing modeling design docs | Supersede; archive to `docs/history/` |
| 11 | Refactor pass sequencing | Doc-first: contracts in Phase 1, code follows in Phases 2–7 |
| 12 | Testing strategy | Per-phase verification with existing scenario/conformance/integration infrastructure |

## 4. Layered architecture

### 4.1 Four layers

```
┌──────────────────────────────────────────────────────────────┐
│ Layer 4: Bundled services + examples                         │
│   stores/{filesystem,postgres,stub}/                         │
│   executors/{http-node,claude-agent,stub}/                   │
│   compose recipes, agentic-workflow examples                 │
└──────────────────────────────────────────────────────────────┘
              │ implements
              ▼
┌──────────────────────────────────────────────────────────────┐
│ Layer 3: Service protocols (cross-cutting)                   │
│   ClaimProducer, Executor, LifecycleSubscriber               │
│   gRPC services + Go interface types in protocols/ module    │
└──────────────────────────────────────────────────────────────┘
              ▲                                ▲
              │ calls                          │ calls
              │                                │
┌─────────────┴────────────────┐  ┌────────────┴─────────────────┐
│ Layer 2: Modeling            │  │ Layer 1: Foundation          │
│   Templates, instances,      │  │   cascade engine             │
│   frames, schedules,         │  │   lock manager               │
│   attributes, control-plane  │  │   integration                │
│   API, YAML config shape,    │◀─┤   (foundation/ module)       │
│   public vocabularies.       │  │                              │
│   In root module.            │  │                              │
└──────────────────────────────┘  └──────────────────────────────┘
```

Higher layers depend on lower layers. Service protocols are cross-cutting: foundation calls a subset (claim verbs at acquisition/terminal; executor dispatch at worker-request-issue), modeling calls a different subset (lifecycle hooks at control-plane events).

### 4.2 Module mapping (γ)

Three Go modules:

- **`github.com/fallguy/rimsky/foundation`** — cascade engine, lock manager, integration, foundation persistence contract. Depends on stdlib + `protocols`.
- **`github.com/fallguy/rimsky/protocols`** — `ClaimProducer`, `Executor`, `LifecycleSubscriber` Go interface types and protobuf-generated bindings. Depends on stdlib only. External service authors import this module to write a custom service implementation without pulling rimsky's transitive deps.
- **`github.com/fallguy/rimsky`** (root) — modeling layer (templates, instances, frames, schedules, attributes, control-plane API), cmd binaries, bundled service reference implementations (`stores/*`, `executors/*`). Depends on `foundation` + `protocols`.

`go.work` at the repo root coordinates the three modules so `go test ./...` style operations work across the workspace during development.

### 4.3 Package layout (target end state)

```
foundation/
  go.mod
  cascade/        # cascade engine: state, transitions, invalidate cascade, dispatch eligibility
  locks/          # lock manager: claim handles, scope conflict, orphan reaping
  integration/    # acquisition tx, auto-terminal, verify-before-run, lifetime correlation
  persistence/    # foundation persistence contract; driver interfaces
    postgres/     # postgres impl of foundation persistence
    sqlite/       # sqlite impl of foundation persistence
  shared/         # shared types (foundation-internal)
  internal/       # foundation-private helpers

protocols/
  go.mod
  claimproducer/  # Go interface + types
  executor/       # Go interface + types
  lifecycle/      # LifecycleSubscriber Go interface + types
  proto/          # protobuf-generated bindings (post-rename from current proto/v1/gen/)

(root)
  go.mod
  go.work
  cmd/                          # cmd binaries (rimsky-scheduler, -supervisor, -control-api, -migrate, -cli, -conformance, -conformance-probe, -store-conformance, -entrypoint)
  modeling/
    template/                   # template registration, content-addressing, tags
    instance/                   # instance lifecycle, instance_key namespace
    frame/                      # frame resolution (coalesce / serial_queue)
    schedule/                   # cron-driven invalidation
    attribute/                  # attributes schemas, substitution, validation
    controlapi/                 # control-plane API (HTTP/JSON)
    persistence/                # modeling-layer persistence contract; driver interfaces
      postgres/
      sqlite/
  config/                       # rimsky.yml parsing; library entry points (StartScheduler, StartSupervisor, StartControlAPI)
  stores/                       # bundled claim producers (data-backed reference impls)
    filesystem/
    postgres/
    stub/
  executors/                    # bundled executors
    http-node/
    claude-agent/               # TS, separate npm package
    stub/
  test/                         # cross-layer scenario tests
  deploy/                       # docker compose, helm chart, dockerfiles
  docs/                         # architecture docs, glossary, contracts, history
  dashboards/                   # rimsky-dashboard reference impl
```

The current `core/` directory disappears; its contents migrate to either `foundation/`, `modeling/`, or stay at root. The current `proto/v1/` directory migrates to `protocols/proto/`.

The `core/store/` directory dissolves: `Store` interface becomes `ClaimProducer` in `protocols/claimproducer/`; the registry becomes a modeling-layer concern (`modeling/config/`); `core/store/remote/` becomes a foundation concern (it's how foundation calls into ClaimProducer impls — moves to `foundation/integration/` or a sibling `foundation/services/` package).

## 5. Foundation contract — required content

The foundation contract draft at `docs/history/2026-05-03-foundation-contract-design.md` (archived after Phase 1 landed; finalized as `docs/specs/2026-05-04-foundation-contract.md`) is the starting point. Phase 1 finalizes it with the following deltas:

1. **Vocabulary update.** All references to `region` → `scope`. Specifically: §4.1 (claim handle field rename), §4.2 (region conflict → scope conflict), §4.3 (acquisition-time predicate), §6.1 (`region_data` SQL column → `scope_data`), all invariant references.
2. **Subsystem package names settled.** §11.4 open question resolved: `cascade`, `locks`, `integration`. Sections 3, 4, 5 reference these as the canonical package names.
3. **Worker-request consolidation direction settled.** §11.2 open question resolved: aggressive consolidation is the target; the held-claim sub-design (§8 of this spec) defines the schema shape.
4. **Write-semantics location settled.** §11.1 open question resolved: envelope + per-claim with byte-equal-scope uniformity (see §7.1 of this spec). §4.3 of the foundation contract gains a precise statement of the uniformity constraint.
5. **Module split settled.** §11.5 open question resolved: foundation is a separate Go module per γ.
6. **Implementation-status section retired.** §10 of the draft (current code locations) is no longer needed once the layering refactor lands; replaced with a brief "the code matches this contract" assertion.
7. **Cross-references updated.** References to `docs/specs/...` paths replaced with `docs/history/...` for the now-archived per-subsystem design docs.
8. **Driver interface set collapsed.** §6.2 of the foundation contract draft listed `Cascade`, `WorkerRequests`, `Locks`, `Coordinator`. Post-consolidation (§8.4 of this spec) collapses `Locks` into `WorkerRequests`. Additionally, the advisory-lock helper currently named `persistence.Coordinator` is renamed `persistence.AdvisoryLocker` to free the name `Coordinator`/`Conductor` for the integration layer's primary type (§9.1; §14 #2 settles the choice). Final foundation driver interface set: `Cascade`, `WorkerRequests`, `AdvisoryLocker`.

The finalized foundation contract is the authoritative source for foundation invariants, persistence contract, integration mechanisms, and what is explicitly NOT in the foundation. It supersedes any historical foundation-related text in `docs/history/`.

## 6. Modeling-layer comprehensive contract — required content

The new modeling-layer contract (a single document, written in Phase 1) covers all modeling subsystems and supersedes the corresponding archived design docs.

### 6.1 Subsystems covered

The modeling layer comprises the following subsystems; the contract specifies each:

1. **Templates.** Content-addressing scheme, canonical hashing, registration, deployment, tags, the `compose:` reserved-prefix policy.
2. **Instances.** Instance lifecycle, instance-key namespace, the binding from instance to template-hash at creation, the terminator goroutine, the create-body schema.
3. **Frames.** Frame-resolution model (`coalesce` / `serial_queue`), the at-most-one-running-per-instance enforcement, the frame-end SQL predicate, the cascade-tick relationship to the frame engine, the relationship between frames and the foundation's worker-request lifecycle.
4. **Schedules.** Cron parsing semantics (`robfig/cron/v3`), `next_fire_at` advancement (no missed-fire backfill), the admin force-fire endpoint.
5. **Attributes.** Schema language, substitution engine (the `{{...}}` directive grammar), validation (twice — at dispatch and at commit), the userdata-is-opaque invariant (modeling-layer invariant 11), the substitution-leaf-extraction call site (`walkPath`).
6. **Control-plane API.** All HTTP routes on `rimsky-control-api`, request/response shapes, the admin vs non-admin route distinction, the lifecycle-event firing model.
7. **Public vocabularies.** The four-state vocabulary (`fresh`/`stale`/`running`/`failed`) as the chosen presentation of the foundation's two-bit-plus-flag state space; the two-message vocabulary (`invalidate`/`recalculate`); the three-error-action vocabulary (`retry`/`invalidate(targets)`/`give_up`) as the chosen surface over the foundation's parameterized failure-terminal primitive.
8. **YAML config shape.** The `rimsky.yml` schema per Option II in §10: persistence block, `claim_producers:` block (post-rename), `executors:` block, named-locks block. Each peer entry declares the protocols it implements via a `protocols:` list (default: a single protocol matching the block name). No separate `lifecycle_subscribers:` block — a peer that implements `LifecycleSubscriber` declares it via the `protocols:` list on whichever block it primarily belongs to. Validation rules. Operator-declared envelope vs producer-declared envelope reconciliation.
9. **Persistence contract for modeling.** Modeling-owned tables: `rimsky_templates`, `rimsky_template_tags`, `rimsky_instances`, `rimsky_schedules`, `rimsky_frames`, `rimsky_events`, `rimsky_store_lifecycle` (post-rename: `rimsky_lifecycle_idempotency`?), and any others. Driver interface set scoped to modeling tables only.
10. **CLI shape.** `rimsky-cli` and `rimsky-cli compose` command surface, the bare-paths-no-versioning policy, the client-side `compose:` prefix validation.

### 6.2 Required content per subsystem

For each subsystem, the contract specifies:

- **Purpose & scope.** What problem it solves; what's in / out.
- **Invariants.** Numbered, each with a code-location reference and a scenario-test reference. Invariants 11 (userdata opaque) and 12 (attributes validate twice) are modeling-layer; the contract owns them.
- **Persistence schema.** Tables, columns, indexes, foreign keys, with rationale.
- **API shape.** Wire-level (for control-plane) or interface-level (for internal subsystems).
- **Vocabulary mapping.** How modeling-layer named vocabulary maps to foundation primitives.
- **Out of scope.** What's deliberately not in this subsystem (boundary clarification).

### 6.3 Foundation/modeling boundary

A dedicated section of the modeling contract defines the four predicates the modeling layer supplies to the foundation:

1. **Cascade target predicate** — given a node and a `changed: bool` declaration, computes the set of dependent nodes to receive the invalidate signal.
2. **Holding-subgraph completion predicate** — given a claim handle, computes whether the subgraph holding it has reached terminal across all its members.
3. **Aggregate-outcome predicate** — given a holding subgraph at completion, computes commit-vs-abandon (typically: any-failed → abandon, all-completed → commit).
4. **Coexistence predicate** — given a pair of byte-equal-scope claim handles with their announced `WriteSemantics` values, computes whether they may coexist (the conflict / no-conflict decision at acquisition-time).

These four predicates are the totality of the foundation's "read me at decision points" surface; the foundation has no other knowledge of modeling semantics.

## 7. Service-protocol contract — required content

Phase 1 produces a service-protocol contract document that supersedes the archived stores-redesign-v3 spec, the cleanup overlay, and the control-plane-and-store-lifecycle spec. The new contract covers three protocols.

### 7.1 ClaimProducer

Renamed from `Store`. The wire surface:

- **`Open(claim_id, claim_spec) → ClaimResult`** — produces a claim handle. `ClaimResult` carries `address`, `payload`, `scope` (renamed from `region`), and **`realized_write_semantics`** (new field; see uniformity invariant below).
- **`Commit(claim_id)`** — terminal commit verb.
- **`Abandon(claim_id)`** — terminal abandon verb.
- **`Release(claim_id)`** — release verb (used in specific paths per the v3 cleanup overlay).
- **`Capabilities() → CapabilitiesResult`** — startup handshake. Carries `write_semantics_envelope` (new — set of permissible `WriteSemantics` values that `Open` may return; singleton sets are the common case).

Removed from this protocol (now in `LifecycleSubscriber`): `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`.

Invariants on this protocol:

- **9b** *(no internal serialization on lock-shaped predicates)* — preserved verbatim from current.
- **20** *(claim content is inert)* — preserved.
- **New: write-semantics uniformity per (producer, scope-bytes)** — across the lifetime of a producer, two `Open` calls that return byte-equal `scope` MUST return the same `realized_write_semantics`. Producers enforce; foundation can rely on it for the conflict predicate.

The existing items-table queue semantics (postgres reference store) is unchanged — it operates entirely store-internally and is not visible at the protocol level.

### 7.2 LifecycleSubscriber

New service in `protocols/lifecycle/`. Six methods, mirroring the current methods bundled into `Store`:

- `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered` — fired by `rimsky-control-api` at template state transitions.
- `OnInstanceCreated`, `OnInstanceTerminated` — fired by `rimsky-control-api` at instance state transitions (the latter from the background terminator goroutine).

Implementer pattern: return `nil` from any method the binary doesn't react to. Binaries that don't react to *any* event simply don't implement the service.

A binary may implement zero, one, or multiple of `ClaimProducer` / `Executor` / `LifecycleSubscriber`. The `rimsky.yml` schema (§6.1.8 above) declares each peer with the protocols it implements; control-api does a `Capabilities()` probe per protocol at startup.

Idempotency continues to be tracked in `rimsky_store_lifecycle` (consider rename to `rimsky_lifecycle_idempotency` — flagged in §14).

### 7.3 Executor

No major shape change. Renamed conceptually from "node executor protocol" to just "Executor protocol" — the "node" prefix was a holdover. Wire shape (`Execute`, `StreamTrace`, `GetTrace`, `GetCapabilities`) preserved.

The minor changes:

- Capabilities response gains the (already-implemented) `http_bridge_url` field for dashboard discoverability.
- Async-callback path documented as part of the contract (currently relies on the supervisor's chi route shape; the contract should specify the wire requirement explicitly).
- Userdata-is-opaque invariant (modeling-layer invariant 11) re-asserted on this protocol.

## 8. Sub-design: worker-request consolidation

The aggressive consolidation decision (Q4-A) requires settling the schema shape. Held claims complicate the picture: a claim handle's lifetime can exceed the dispatch row's, because auto-terminal resolution can fire well after the work that acquired the claim has terminated.

### 8.1 Active-phase + held-phase model

Every worker-request lifecycle has up to two phases:

- **Active phase.** Work is running. The dispatch claim is taken; lock-holder rows for any required claims exist; heartbeat is current. This phase ends at executor terminal (success or failure).
- **Held phase** *(optional)*. The work has completed but it acquired *held* claims that persist into the holding subgraph. The lock-holder rows for those held claims persist; the dispatch row's role at this phase is bookkeeping — the work has terminated but the worker-request as a whole isn't done. This phase ends at auto-terminal resolution (Commit or Abandon fired; lock-holder rows deleted).

For workers that don't acquire any claims: only active phase exists; lifecycle ends at executor terminal.

For workers that acquire only non-held claims: only active phase exists; lock-holder rows are cleaned up at terminal via verb-fire-and-delete, dispatch row deleted concurrently.

For workers that acquire any held claim: both phases exist.

### 8.2 Schema shape

Two clean expressions of the active+held model are viable:

**Option I: Single worker-request row with phase column.**

```sql
CREATE TABLE rimsky_worker_request (
  id UUID PRIMARY KEY,
  node_id ...,
  frame_id ...,
  claimed_by TEXT,                       -- NULL = unclaimed; supervisor_id = active claim
  heartbeat_at TIMESTAMPTZ,
  phase TEXT NOT NULL,                    -- 'pending' | 'active' | 'held' | 'completed'
  active_terminal_at TIMESTAMPTZ,         -- when active phase ended
  ...
);

CREATE TABLE rimsky_claim_handle (
  id UUID PRIMARY KEY,
  worker_request_id UUID REFERENCES rimsky_worker_request(id) ON DELETE SET NULL,
  holder TEXT NOT NULL,                   -- supervisor_id
  scope_data BYTEA NOT NULL,              -- canonicalized scope bytes (renamed from region_data)
  address JSONB,
  payload JSONB,
  purpose TEXT NOT NULL,                  -- producer-name / acquisition path
  realized_write_semantics TEXT NOT NULL,
  is_held BOOLEAN NOT NULL,               -- true = persists into held phase
  ...
);
```

Note on FK semantics: `worker_request_id` is `ON DELETE SET NULL`, **not** CASCADE. An earlier draft of this spec used CASCADE; smoke-test debugging during Phase 5 implementation surfaced a bug where held claim handles were cascade-deleted at active terminal before auto-terminal could fire the producer's Commit verb (items left stuck `in_progress` in the producer's own state). SET NULL lets held claim handles outlive the worker-request's active terminal until auto-terminal explicitly resolves and deletes them. The held-claim resolution path (`foundation/integration/auto_terminal.go::CheckAndFireResolution`) keys off claim-holder rows, not worker-request lifetime.

Worker-request is deleted at executor terminal (active terminal) by the supervisor's `Queue.Complete`. At that moment, any held claim handles have their `worker_request_id` SET NULL but persist; auto-terminal later fires the producer verb on each and deletes the row.

**Option II: Separate worker-request and held-claim-set rows.**

```sql
CREATE TABLE rimsky_worker_request ( ... );          -- short-lived; deleted at executor terminal
CREATE TABLE rimsky_claim_handle ( ... );             -- correlated by worker_request_id, but FK is RESTRICT, not CASCADE
CREATE TABLE rimsky_holding_subgraph ( ... );         -- tracks the "this set of claim handles persists past their worker-request" relationship
```

Spec body recommendation: **Option I**. Single-table-with-phase-column expresses the lifecycle in one place and lets cascade delete clean up children naturally; held vs non-held distinction is a column on the claim handle, not a structural difference. Option II spreads the logic across more tables and makes orphan reaping more complex.

### 8.3 Migration approach

Pre-v1 dev-DB-nuke acceptable. The schema reshape replaces the existing `rimsky_dispatch` and `rimsky_lock_holders` tables in a single migration that drops both and creates the new structure. No backwards-compat shim. Existing modeling-layer FK references (e.g., `rimsky_claim_holders.lock_holder_id`) update to point to `rimsky_claim_handle.id`.

### 8.4 Driver interface impact

The foundation persistence contract collapses `WorkerRequests` and `Locks` driver interfaces into a single `WorkerRequests` interface that owns both tables. Methods:

```
WorkerRequests {
  Insert(...) (id, error)
  Claim(id, supervisor_id) error
  Heartbeat(id, supervisor_id) error
  EnterHeldPhase(id) error
  Complete(id, supervisor_id) error
  ReapOrphans(cutoff) (reaped []id, error)

  InsertClaimHandles(worker_request_id, []ClaimHandleSpec) error
  ReleaseClaimHandle(handle_id, holder) error
  ListHandlesForResolution(worker_request_id) ([]ClaimHandle, error)
}
```

The two orphan reapers (current: one for dispatch, one for lock-holders) collapse into one; the two terminal-decision modules (current: `ApplyTerminalOutcome` for executor terminal, `CheckAndFireResolution` for auto-terminal) collapse into one parameterized engine that dispatches based on phase.

## 9. Sub-design: Go-level public API

### 9.1 `foundation/` module

Top-level packages exported:

- **`foundation/cascade`** — node-state types, cascade-signal types, dispatch-eligibility predicates. Public types: `NodeState`, `CascadeSignal`, `WorkerRequestLifecycle`. Public functions: minimal — most logic stays internal.
- **`foundation/locks`** — claim-handle types, scope conflict primitives. Public types: `ClaimHandle`, `Scope` (alias for `[]byte`), `ConflictPredicate`. Public functions: `BytesEqual`.
- **`foundation/integration`** — acquisition tx, auto-terminal, verify-before-run as a coordinator type. Public type: `Conductor` (chosen to free up "Coordinator" from the persistence helper rename in §5 #8 / §14 #2).
- **`foundation/persistence`** — `Driver` interface, `WorkerRequests` interface (post-consolidation), `AdvisoryLocker` interface (advisory locks; renamed from `Coordinator` per §5 #8), foundation-table types. Driver impls live under `foundation/persistence/postgres/` and `foundation/persistence/sqlite/`.
- **`foundation/shared`** — shared types used across foundation packages (timestamps, IDs, error types).

Internal:

- **`foundation/internal/`** — anything that's foundation-private.

The modeling layer consumes `foundation/cascade`, `foundation/locks`, `foundation/integration`, and `foundation/persistence` as public APIs; `foundation/internal/` is forbidden.

### 9.2 `protocols/` module

Top-level packages:

- **`protocols/claimproducer`** — Go interface `ClaimProducer`, types (`OpenRequest`, `ClaimResult`, `CapabilitiesResult`, `WriteSemanticsEnvelope`).
- **`protocols/executor`** — Go interface `Executor`, types.
- **`protocols/lifecycle`** — Go interface `LifecycleSubscriber`, types.
- **`protocols/proto`** — protobuf-generated bindings for all three protocols.

External service authors import `protocols/claimproducer` (or executor / lifecycle) to write a custom service. They get the Go interface, the wire types, and the generated gRPC server/client. They do not get any foundation or modeling code.

The `protocols/` module has zero non-stdlib dependencies (other than `google.golang.org/grpc` and `google.golang.org/protobuf`).

## 10. Sub-design: YAML config shape post-rename

The `rimsky.yml` schema needs reconciliation with the protocol-level rename. Three options:

- **Option I:** Rename `stores:` block to `claim_producers:`. Keep `executors:` block. Add `lifecycle_subscribers:` block.
- **Option II:** Rename `stores:` to `claim_producers:`. Allow a single peer to declare multiple protocols via its YAML entry shape: `protocols: [claim_producer, lifecycle_subscriber]`.
- **Option III:** Unify under a single `services:` block where each entry declares which protocols it implements.

**Recommendation:** Option II. The YAML key matches the dominant protocol the operator thinks of (a peer is *primarily* a claim producer, secondarily a lifecycle subscriber); cross-protocol declaration is a one-line annotation rather than a structural reshape. Option I forces operators to declare the same peer twice if it implements multiple protocols. Option III is conceptually cleanest but loses the ergonomic affordance of "a service is its primary protocol."

Concrete YAML shape:

```yaml
claim_producers:
  - name: items-pg
    endpoint: localhost:7001
    protocols: [claim_producer, lifecycle_subscriber]   # default: [claim_producer] only
    write_semantics_envelope: [staged_async]            # operator-declared; ⊆ producer-declared at handshake
    # ... per-producer config

executors:
  - name: claude-agent
    endpoint: localhost:7100
    protocols: [executor]
    # ...

# Producers / executors that are also lifecycle subscribers declare it via the
# protocols list. There is no separate `lifecycle_subscribers:` block.
```

Spec body finalizes the exact shape during Phase 1.

## 11. Refactor sequencing — 7 phases

Phases describe *what* gets built in what order. Each phase has defined deliverables and a defined verification gate.

### Phase 1 — Contracts

**Deliverables:**

- `docs/specs/2026-05-04-foundation-contract.md` *(finalized; supersedes the 2026-05-03 draft, which moves to history)*. Reflects all twelve decisions including region→scope, subsystem package names, worker-request consolidation as the target.
- `docs/specs/2026-05-04-modeling-layer-contract.md` *(new, comprehensive)*. Covers all 10 modeling subsystems (§6.1).
- `docs/specs/2026-05-04-service-protocol-contract.md` *(new; supersedes archived stores-redesign-v3 + cleanup overlay + control-plane-and-store-lifecycle docs for service-protocol content)*. Defines `ClaimProducer`, `Executor`, `LifecycleSubscriber`.

**Verification gate:** Spec reviewer (per ok-planner brainstorm flow) approves all three contracts; user approves the consolidated set.

**Code changes:** None.

### Phase 2 — Module split (γ)

**Deliverables:**

- `foundation/go.mod`, `foundation/cascade/`, `foundation/locks/`, `foundation/integration/`, `foundation/persistence/{postgres,sqlite}/`, `foundation/shared/`, `foundation/internal/`. All current code that the foundation contract claims for the foundation moves here. Imports update.
- `protocols/go.mod`, `protocols/claimproducer/`, `protocols/executor/`, `protocols/lifecycle/`, `protocols/proto/`. Current `proto/v1/` migrates.
- Root module trimmed: `core/` directory dissolved; modeling code moves to `modeling/`; cmd binaries stay at `cmd/` (or `core/cmd/` collapsed to root `cmd/`).
- `go.work` at repo root coordinating the three modules.
- `Makefile` targets updated for the multi-module layout.
- `.golangci.yml` depguard rules updated for the new module / package layout: `pgx` allowed under `foundation/persistence/postgres/` and `modeling/persistence/postgres/`; old `core/persistence/postgres/` allowance removed; equivalent updates for any other depguard-restricted imports whose paths move.

**Pure mechanical reorg.** No semantic code changes; no rename yet (region/scope and store/ClaimProducer come in later phases). The new module structure compiles and tests pass.

**Verification gate:** `go build ./...` clean in each module; `go test ./...` clean across modules via `go.work sync`; `make lint` passes.

### Phase 3 — Region → scope rename

**Deliverables:**

- All identifiers, columns, struct fields, proto fields, type names, error messages, doc references, scenario-test names containing `region` (in the conflict-predicate sense) renamed to `scope`. SQL column `region_data` → `scope_data`. Proto field renames trigger `make proto-gen`.
- Foundation contract reference to `RegionsByteEqual` → `ScopesByteEqual` in the helpers.
- Operator-facing error messages updated.

**Verification gate:** `go build ./...` clean; `go test ./...` clean (pre-existing scenario tests pass post-rename); `grep -r '\bregion\b' --include='*.go' | grep -v 'docs/history'` returns nothing in conflict-predicate sense (geographic / cluster-region uses, if any, may stay).

### Phase 4 — ClaimProducer rename + LifecycleSubscriber split + write-semantics envelope

**Deliverables:**

- `Store` interface in `protocols/claimproducer/` becomes `ClaimProducer`. Wire-protocol gRPC service renamed; proto file renamed (`store_service.proto` → `claim_producer.proto`); `make proto-gen` regenerates.
- `LifecycleSubscriber` extracted into `protocols/lifecycle/` as a separate gRPC service with the six methods.
- `ClaimResult` gains `realized_write_semantics`. `CapabilitiesResult` gains `write_semantics_envelope` (supersedes the single `WriteSemantics` field).
- `rimsky.yml` schema updated to Option II shape (§10): `stores:` block renamed to `claim_producers:`; entries gain `protocols:` list; `write_semantics_envelope` operator declaration replaces single value.
- Bundled service reference impls updated: filesystem store, postgres store, stub store, claude-agent, http-node, stub executor — all updated to the new protocol shapes.
- Conformance suites updated: `rimsky-conformance` (executor) renamed appropriately; `rimsky-store-conformance` renamed to `rimsky-claim-producer-conformance`. New conformance for `LifecycleSubscriber`.
- Per-claim WriteSemantics conformance tests added (envelope + realized-value validation; uniformity-per-(producer,scope) conformance).

**Verification gate:** All conformance suites pass against the bundled reference impls; scenario tests pass; protocol changes don't break the smoke test.

### Phase 5 — Worker-request aggressive consolidation

**Deliverables:**

- Schema migration replacing `rimsky_dispatch` + `rimsky_lock_holders` with `rimsky_worker_request` + `rimsky_claim_handle` per §8.2 Option I. Pre-v1 dev-DB-nuke (`drop and recreate`) — no compat shim.
- Foundation persistence driver collapsed: single `WorkerRequests` interface owning both tables (§8.4).
- Modeling-layer FK references (`rimsky_claim_holders` etc.) repointed to the new structure.
- Held-claim phase logic implemented: phase column transitions, auto-terminal detection of phase=`held` rows whose claim-handle children are exhausted.

**Verification gate:** All blessed-invariant scenario tests pass against the new schema (foundation invariants 3, 4, 5, 6, 10, 13 — numbering per the post-Phase-1 foundation contract, which preserves the historical numbers). New held-claim sub-design tests cover both active-only and active+held lifecycles. Smoke test passes.

### Phase 6 — Reaper + terminal-decision unification

**Deliverables:**

- Single orphan reaper replaces the two current reapers (dispatch reaper + lock-holder reaper).
- Single terminal-decision engine replaces `ApplyTerminalOutcome` + `CheckAndFireResolution` — parameterized by phase.
- Tests prove behavioral equivalence: same scenarios produce same outcomes through the unified mechanism.

**Verification gate:** Behavioral-equivalence test suite passes; parallel mechanism removed; no `// TODO: unify with the other reaper` comments remain.

### Phase 7 — Final cleanup

**Deliverables:**

- `CLAUDE.md` rewritten: references the three new contracts as authoritative; removes references to historical specs; updates blessed-invariant catalog with new code locations; removes transitional language about "the v3 cutover" and similar.
- `docs/architecture.md` rewritten: presents the four-layer model; references the three contracts; updates the "where to look first" section.
- `docs/operator-guide.md` rewritten: uses new YAML key shape; references new contracts.
- `docs/glossary.md` rewritten: uses new vocabulary (scope, claim producer, etc.); preserves modeling-layer vocabulary mapping.
- `docs/protocol.md` rewritten or retired in favor of the service-protocol contract.
- `docs/executor-author-guide.md` and `docs/claim-producer-author-guide.md` (the latter renamed from the historical `store-author-guide.md` filename, also under `docs/`, during Phase 4) updated.
- `docs/node-graph-design.md` updated to reflect the foundation/modeling vocabulary distinction.
- Helm chart in `deploy/kubernetes/rimsky-chart/` updated (current chart is known-stale per the v3 CHANGELOG entry).

**Verification gate:** Documentation review confirms all references are coherent; a `docs/...` cross-reference grep returns no broken paths (every `docs/...` link in any rewritten doc resolves to an existing file); operator can read the new guide and run the unified-image stack.

## 12. Testing strategy

### 12.1 Per-phase verification

Each phase's verification gate (defined in §11) is the gate. No phase is considered complete until its gate passes.

### 12.2 Test infrastructure leveraged

- **Scenario tests** (`test/scenarios/`) — exercise blessed invariants against a real Postgres via testcontainers-go.
- **Conformance suites** (`rimsky-conformance` and renamed `rimsky-claim-producer-conformance` plus new `rimsky-lifecycle-conformance`) — verify protocol implementations against the contract.
- **Integration tests** (`test/smoke/`) — end-to-end via the unified image.
- **Per-package unit tests** — distributed across all modules.
- **TS executor tests** (`executors/claude-agent/`) — vitest.

### 12.3 New test coverage required

Phase-by-phase additions:

- **Phase 4:** New `LifecycleSubscriber` conformance suite. Per-claim `WriteSemantics` envelope-conformance tests (each producer reference impl). Uniformity-per-(producer,scope) conformance.
- **Phase 5:** Held-claim active-phase + held-phase lifecycle tests (in `test/scenarios/locks/` — currently a placeholder per the v3 cutover notes; this is its substantive replacement). Schema-migration tests for the worker-request consolidation.
- **Phase 6:** Behavioral-equivalence test pairs for the unified reaper and terminal-decision engine vs. the historical parallel mechanisms.

### 12.4 Placeholder scenario suites

`test/scenarios/{locks, stores, attributes, claim_stores}/` — currently compile-passing placeholders per the v3 cutover. Phases 4 and 5 fill them in with the substantive coverage mentioned above. The spec body's plan articulates which placeholder gets which tests.

## 13. Documentation deliverables

In summary, the spec produces or rewrites:

| Doc | Status post-spec | Phase |
|-----|------------------|-------|
| `docs/specs/2026-05-04-foundation-contract.md` | New (supersedes 2026-05-03 draft) | 1 |
| `docs/specs/2026-05-04-modeling-layer-contract.md` | New (comprehensive) | 1 |
| `docs/specs/2026-05-04-service-protocol-contract.md` | New (supersedes archived stores docs + control-plane lifecycle doc) | 1 |
| `docs/history/2026-05-03-foundation-contract-design.md` | Moved here from `docs/specs/` after Phase 1 landed | 1 |
| `docs/architecture.md` | Rewrite | 7 |
| `docs/glossary.md` | Rewrite (scope, claim producer, layer model) | 7 |
| `docs/operator-guide.md` | Rewrite (new YAML, new vocab) | 7 |
| `docs/protocol.md` | Retire or rewrite as pointer to service-protocol contract | 7 |
| `docs/executor-author-guide.md` | Rewrite | 7 |
| `docs/claim-producer-author-guide.md` | Renamed from the historical `store-author-guide.md` filename (same directory) and rewritten | 7 |
| `docs/node-graph-design.md` | Update with foundation/modeling vocabulary | 7 |
| `CLAUDE.md` | Rewrite | 7 |
| `CHANGELOG.md` | Bullets per phase under `## Unreleased` | each phase |

## 14. Open sub-decisions for spec body

Items the spec acknowledges and the implementation work resolves:

1. **Worker-request schema shape: Option I vs Option II.** §8.2 recommends Option I; Phase 5 finalizes during implementation if Option I turns out to be impractical.
2. **Foundation `integration/` package's primary type name.** Settled: `Conductor`. The old `persistence.Coordinator` (advisory locks) is renamed `persistence.AdvisoryLocker` (per §5 #8) to free the `Coordinator`-shaped name space; the integration layer's primary type uses `Conductor` rather than `Coordinator` for clarity. Both renames land in Phase 2.
3. **Lifecycle idempotency table rename.** Current: `rimsky_store_lifecycle`. Post-rename candidate: `rimsky_lifecycle_idempotency`. Decide in Phase 4.
4. **Conformance binary name(s).** Current: `rimsky-conformance`, `rimsky-store-conformance`. Post-rename: `rimsky-conformance` (executor + lifecycle) vs. `rimsky-claim-producer-conformance` vs. `rimsky-protocol-conformance` covering all three. Decide in Phase 4.
5. **Whether to ship a `protocols` module backwards-compat shim** for any external consumers. Default: no (pre-v1, no consumers). Confirm in Phase 2.
6. **Whether `core/cmd/` flattens to root `cmd/`** as part of Phase 2's package reorg. Default: yes (one less directory level). Confirm in Phase 2.

## 15. Out of scope

- New service implementations (no new claim producers, executors, lifecycle subscribers beyond what already exists).
- New modeling-layer features (no new template features, instance features, frame modes, schedule semantics, attribute capabilities, control-plane endpoints).
- v1 declaration. This spec gets us to a state where v1 can be a deliberate later decision; v1 itself is a separate spec.
- Performance benchmarking / optimization. Schema and code consolidation may incidentally improve performance; that's not a goal.
- Production data migration. Pre-v1 break-freely.
- Helm chart features beyond fixing it to match the new layout (still flagged as stale).
- External-library publishing of the foundation or protocols modules.
- Dashboard / observability work — already in flight as a separate effort; not affected by this refactor except via path renames.

---

*End of design.*
