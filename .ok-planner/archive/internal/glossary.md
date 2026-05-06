# Rimsky Glossary

Authoritative naming reference for the codebase, post-2026-05-04 layer-crystallization refactor. When this glossary contradicts older docs, the glossary wins.

The three contracts in `docs/specs/` define the layered responsibilities behind the vocabulary:

- `docs/specs/2026-05-04-foundation-contract.md` — foundation layer (cascade engine, lock manager, integration).
- `docs/specs/2026-05-04-modeling-layer-contract.md` — modeling layer (templates, instances, frames, schedules, attributes).
- `docs/specs/2026-05-04-service-protocol-contract.md` — service protocols (`ClaimProducer`, `Executor`, `LifecycleSubscriber`).

---

## Four-layer model — quick summary

Rimsky is structured as four layers; vocabulary is layered too. A term may have a foundation-layer meaning, a modeling-layer presentation, and a colloquial bundled-services-layer name.

1. **Foundation** — cascade engine + lock manager + integration. Traffics in **bits + annotations** (`has_value`, `has_outstanding_request`, `auto_recovers`), **claim handles**, **scope bytes**, an **undifferentiated parameterized failure-terminal**.
2. **Modeling layer** — templates, instances, frames, schedules, attributes, control-plane API. Presents named states (`fresh`/`stale`/`running`/`failed`), named messages (`invalidate`/`recalculate`), named error actions (`retry`/`invalidate(targets)`/`give_up`).
3. **Service protocols** — `ClaimProducer`, `Executor`, `LifecycleSubscriber`. Wire contracts external services implement.
4. **Bundled services + examples** — reference implementations, agentic-workflow primitives, deployment recipes. The colloquial **store** survives here for data-backed claim-producers (filesystem store, postgres store, stub store).

When an entry below has multiple sense, the layer is named.

---

## Core vocabulary

### Claim producer

The protocol-level term for a service that produces claim handles for Rimsky's lock manager. Implements the `ClaimProducer` interface (`Open` / `Commit` / `Abandon` / `Release` + `Capabilities()`).

A claim producer is the protocol-level entity. A **store** (data-backed reference impl) is one kind of claim producer; the colloquialism survives at the bundled-services layer for those impls. Use "claim producer" when discussing the protocol; "store" when discussing data-backed colloquial.

### Claim handle

A persistent row asserting "holder H has acquired scope S for purpose P." Implementation: `rimsky_claim_handle` row (renamed from `rimsky_lock_holders` in Phase 5). Carries `id`, `holder`, `scope_data BYTEA`, `address`, `payload`, `purpose` tag, `realized_write_semantics`, `is_held` flag, `worker_request_id` FK.

The foundation introspects only `id`, `holder`, `scope_data`, and the worker-request correlation. `address`, `payload`, and `purpose` are inert per **invariant 20**.

### Worker request

A row in `rimsky_worker_request` representing one outstanding piece of work for one node. Replaces the legacy `rimsky_dispatch`. The lifecycle (`phase` column):

- **`pending`** — created; awaiting claim.
- **`active`** — claimed by a runner instance; the dispatch claim brackets the node's `running` window.
- **`held`** — actively-terminal but with held claim handles still outstanding.
- **`completed`** — final.

The orphan reaper covers `phase='active'` rows with stale heartbeat. Held-phase rows are NOT reaped at the worker-request level — auto-terminal handles their resolution.

### Scope

Both the conceptual `(producer, selector)` pair and the concrete opaque bytes that identify it on the claim_handle row. The conceptual sense names the slice of a producer's namespace under claim; the concrete sense is the resolved selector text or pick-policy-picked identifier (stored in the `scope_data` column). Substitutable via `{{claim.<alias>.scope}}`.

**Renamed from `region` in Phase 3.** The legacy term `region` (and `region_data` SQL column) is deprecated; use `scope` everywhere. The historical name survives only in archived design docs under `docs/history/`.

### Selector

The opaque text the graph author supplies (post-substitution). The producer parses; Rimsky doesn't classify or validate. May contain `{{...}}` substitution directives resolved at dispatch.

### Address

Producer-supplied pointer the executor uses to access claimed state (path, table reference, snapshot handle, etc.). Returned by `Open`. Substitutable via `{{claim.<alias>.address}}` in inheriting nodes.

### Payload

Producer-supplied data captured at acquisition (e.g., a picked queue item's user data). Substitutable via `{{claim.<alias>.payload.<field>}}`.

### Intent

`r` (read) or `rw` (read-write). The graph author's declaration of what the executor will do with the claim.

### Alias

Per-claim name within a node. Used in substitution paths and `inherits:` references. Defaults to the producer name; can be set explicitly when a node has multiple claims on the same producer.

### Named lock

A row in `rimsky_claim_handle` with `lock_kind='named'`, holding `(lock_name, capacity)`. The non-producer primitive. Halts node dispatch when the count of holders equals the capacity. Modes: `mutex` (capacity 1) / `counting` (declared capacity).

### `claim_id`

The Rimsky-generated UUID (textual on the wire) that identifies a single claim across every protocol verb in its lifecycle. Generated client-side immediately before `Open`; persisted in `rimsky_claim_handle.id`; passed unchanged on `Commit` / `Abandon` / `Release`.

---

## Verbs (4 runtime + 1 startup)

| Verb | Signature | Purpose |
|---|---|---|
| **Open** | `Open(claim_id, spec) → ClaimResult` | Produce a producer-supplied address for the executor and register whatever producer-side state the `(intent × write_semantics)` combination requires. Returns `address`, `payload`, `scope`, and `realized_write_semantics`. |
| **Commit** | `Commit(claim_id) → ()` | Signals that the consumer of the claim succeeded. The producer decides what to do with its own state per its own configuration. |
| **Abandon** | `Abandon(claim_id) → ()` | Signals that the consumer of the claim failed. The producer decides what to do with its own state per its own configuration. |
| **Release** | `Release(claim_id) → ()` | Tear down producer-side read state (snapshot, MVCC transaction) for a read claim. |
| **Capabilities** | `Capabilities() → CapabilitiesResult` | Startup handshake. Returns the producer's `WriteSemanticsEnvelope` (the set of write-semantics values the producer may return on `Open`). Probed once per protocol per peer at process startup. |

`intent ∈ {r, rw}`. Producer disposition (commit-vs-release-vs-delete on its own state) is governed by per-producer config. Rimsky carries only the success/failure binary; no producer-internal vocabulary fields cross the rimsky↔producer boundary.

---

## Lifecycle & propagation

| Term | Definition |
|---|---|
| **Acquirer** | The node that calls `Open` for a given claim. Exactly one acquirer per claim. |
| **Inheritor** | A downstream node that declares `inherits:` on the acquirer's claim alias. Inheritance extends the claim's lifetime to cover the inheritor's run. |
| **Inheritance** | The DSL mechanism by which a downstream node declares it will use the live claim from an upstream acquirer. Direct only — does not propagate transitively through dep chains. |
| **Holding subgraph** | The set of nodes a held claim's lifetime spans: acquirer + directly-declared inheritors. Computed at template deploy from explicit `inherits:` declarations. |
| **Active phase** | The `phase='active'` portion of a worker-request's lifecycle: from claim through the claimant's executor terminal. The dispatch claim brackets this window. |
| **Held phase** | The `phase='held'` portion of a worker-request's lifecycle: post-active-terminal, while held claim handles are still outstanding. Held-phase worker-requests are NOT reaped at the worker-request level; their resolution is auto-terminal. |
| **Auto-terminal** | The mechanism by which a held claim's resolution fires automatically when the holding subgraph completes. Aggregate outcome (all-success → `Commit`; any-failure → `Abandon`) determines the producer verb. No graph-author terminal designation required. |
| **Value-pass** | Propagation mode: source extracts captured fields into its own attributes; downstream nodes consume via `{{deps.<source>.<field>}}`. Lifetime-independent — works after the source's claim has closed. |
| **Claim-pass** | Propagation mode: downstream node inherits the live claim and uses `{{claim.<alias>.address \| payload.<f> \| scope}}`. Requires the claim to remain open; the inheriting node's existence holds it. |

---

## Write semantics

### `WriteSemantics`

Per-claim verdict from `ClaimProducer.Open` plus the per-producer envelope from `Capabilities()`. Values:

- **`sync`** (formerly `direct`) — synchronous in-place writes; no staging.
- **`staged_async`** — writes go to a producer-internal staging area; reads can dispatch concurrently with writes on the same scope.
- **`blocking_async`** (formerly `staged_blocking`) — staging area; reads block until commit.
- **`read_only`** — read-only access (no writes possible).

### Realized write semantics

The per-claim `WriteSemantics` value the producer returns on `Open` (`ClaimResult.RealizedWriteSemantics`). Persisted on the claim_handle row. Used as the conflict-predicate input — byte-equal scope must agree on `realized_write_semantics` (the **byte-equal-scope uniformity invariant**; producers enforce this).

### Write semantics envelope

The set of `WriteSemantics` values a producer may return from `Open` (`CapabilitiesResult.WriteSemanticsEnvelope`). Operators declare a subset envelope per peer in `rimsky.yml` under `write_semantics_envelope:`; capability handshake validates operator-declared ⊆ producer-declared. Per-claim `realized_write_semantics` MUST be a member of the producer-declared envelope.

---

## Service protocols

### `ClaimProducer`

The primary producer protocol. Five methods (`Open`, `Commit`, `Abandon`, `Release`, `Capabilities`). Foundation calls this at acquisition (`Open`), at executor terminal (`Commit`/`Abandon`/`Release` on non-held claims), and at auto-terminal (`Commit`/`Abandon` on held claims).

**Renamed from `Store` (protocol level) in Phase 4.** The legacy protocol-level `Store` interface is gone. The "store" colloquialism survives at the bundled-services layer for data-backed reference impls.

### `Executor`

The executor protocol. Four methods (`Execute`, `StreamTrace`, `GetTrace`, `GetCapabilities`). Foundation calls `Execute` at dispatch.

### `LifecycleSubscriber`

Hooks into Rimsky's control-plane lifecycle events. Six methods (`OnTemplateRegistered/Deployed/Undeployed/Deregistered`, `OnInstanceCreated/Terminated`). Modeling (control-api) calls this on template/instance state transitions.

**New protocol in Phase 4.** Replaces the six lifecycle methods that were previously bundled into the `Store` service. Now opt-in per peer via `protocols: [..., lifecycle_subscriber]` in `rimsky.yml`.

Idempotency tracked in `rimsky_lifecycle_idempotency` (renamed from `rimsky_store_lifecycle`). Each event keyed by (peer-name, event-type, object-id); replays are no-ops.

---

## Producer-side mechanisms (out-of-band)

| Term | Definition |
|---|---|
| **Staging area** | Informal — a producer-internal private workspace where in-progress writes accumulate before atomic publication. Visible to Rimsky only as the address `Open` returned. |
| **Atomic swap** | Informal — the producer's native mechanism that publishes staging into live data atomically. Examples: filesystem rename, SQL `ALTER TABLE` swap, S3 manifest pointer flip, git merge. The moment of `Commit`. |

---

## Public state vocabulary (modeling-layer presentation)

Four named states map to the foundation's two-bit-plus-flag space:

| has_value | has_outstanding_request | auto_recovers | name      |
|-----------|-------------------------|---------------|-----------|
| true      | false                   | n/a           | `fresh`   |
| false     | false                   | true          | `stale`   |
| false     | true                    | n/a           | `running` |
| false     | false                   | false         | `failed`  |

The foundation traffics in the bits and annotation; the modeling layer assigns names.

## Public message vocabulary

- **`invalidate`** — the only graph-level message. Cascades from a node losing/replacing its value to a chosen target set.
- **`recalculate`** — internal to the dispatch loop. Per-node action; not a message in the foundation sense.

## Public error-action vocabulary

Three error actions, each realized as a specific `(auto_recovers, cascade_targets)` pair on the foundation's parameterized failure-terminal primitive:

| Action | auto_recovers | cascade_targets |
|---|---|---|
| `retry` | true | {} |
| `invalidate(targets)` | true | targets |
| `give_up` | false | {} |

---

## Rules & invariants

| Term | Definition |
|---|---|
| **Inertness (blessed invariant 20)** | Rimsky reads claim content (payload, address, scope) by named-field path **only at substitution-leaf extraction**; does not log, validate, transform, normalize, decrypt, hash, index, pattern-match, attach to traces, include in errors, or otherwise act on claim content. |
| **Substitution-leaf extraction** | The single sanctioned operation Rimsky performs on claim content: walk the named field path, return leaf bytes, pass through to the next destination. The only sanctioned introspection site is `modeling/attribute/substitution.go::walkPath`. |
| **Encrypt-before-pass** | Operator-side practice: sensitive fields are encrypted before they enter Rimsky's address space; Rimsky transports ciphertext as opaque bytes; the consuming executor decrypts at point of use. |
| **Auth-blind** | Rimsky has no protocol surface for credentials. No verbs, fields, or types in the protocol mention auth. Service-to-service auth between Rimsky processes is operator-configured at the deployment layer (mTLS, IAM, service mesh). |
| **Single-writer-per-scope** | Structural invariant: at most one `rw` claim on overlapping scopes at any time, regardless of mode. Folds into the `w×w ❌` cells of the coexistence matrix. |
| **Byte-equal-scope uniformity** | Across the lifetime of a producer, two `Open` calls returning byte-equal `scope` MUST return the same `RealizedWriteSemantics`. Producers enforce. Foundation relies on this for the conflict predicate. |

---

## Frame & scheduling

| Term | Definition |
|---|---|
| **Frame** | The unit of cascade resolution per the modeling-layer contract §5. Every `rimsky_worker_request` row carries a non-null `frame_id`. At most one frame is `running` per instance. |
| **Holding-subgraph completion** | All nodes in a holding subgraph have terminated (committed or failed). Trigger for auto-terminal. |
| **`frame_id` (on claim_handle rows)** | Observability-only. Not consulted at acquisition, eligibility, orphan-reap, or held-claim resolution. |

---

## Substitution paths

| Path | Reads from | Lifetime |
|---|---|---|
| **`{{deps.<node>.<field>}}`** | Upstream node's persisted attributes (captured values). | Independent of any claim's lifetime. |
| **`{{claim.<alias>.address}}`** | The live claim's address. | Valid only in the acquirer's own node OR in nodes inheriting the alias. |
| **`{{claim.<alias>.payload.<field>}}`** | The live claim's payload at a named field path. | Same validity rule as `address`. |
| **`{{claim.<alias>.scope}}`** | The live claim's scope (resolved selector or picked identifier). | Same validity rule as `address`. |
| **`{{params.<key>}}`** | Instance-level config params. | Independent of any claim. |

---

## Coexistence matrix (claim vs claim, on overlapping scopes)

Mode is derived per claim from `(intent, realized_write_semantics)` at conflict-check time.

| | sync-r | sync-w | async-r | async-w |
|---|---|---|---|---|
| **sync-r** | ✅ | ❌ | (n/a) | (n/a) |
| **sync-w** | ❌ | ❌ | (n/a) | (n/a) |
| **async-r** | (n/a) | (n/a) | ✅ | ✅ |
| **async-w** | (n/a) | (n/a) | ✅ | ❌ |

- **Sync block** (claim with `sync` / `blocking_async` / `read_only`): r×r ✅; everything else ❌.
- **Async block** (claim with `staged_async`): r×r ✅, r×w ✅, w×w ❌.
- Cross-quadrant cells are n/a — uniformity invariant means two claims on byte-equal scope share the same `realized_write_semantics`.

Named locks have no mode dimension; their coexistence rule is purely numeric (count vs. capacity).

---

## Things deliberately NOT in the vocabulary

- **`region`** — replaced. Use `scope`. (Renamed in Phase 3.)
- **`Store` (protocol level)** — replaced by `ClaimProducer`. The "store" colloquialism survives at the bundled-services layer for data-backed reference impls (filesystem store, postgres store, stub store) only.
- **`StoreService`** — gone. The protocol-level binary is a "claim producer" (or "claim-producer service").
- **"Bridge"** — replaced. Use "producer" or "claim producer."
- **"Substrate"** as a synonym for "store" — never. A substrate is the underlying physical storage technology (a filesystem on disk, a postgres database, an S3 bucket); a producer is the rimsky-side service that wraps it. Use "underlying storage" or just "the producer's database/filesystem" when explicitly referring to the underlying physical layer.
- **"Held: true" flag** — dissolved; held is implicit from inheritance. Persisted on the claim_handle row as `is_held BOOLEAN` for observability.
- **"Region claim" / "Item claim" / "Budget claim"** as sub-types — replaced by the cleaner two-noun split (claim / named lock); the "item" sub-form is just a claim whose producer runs an items-table queue convention behind the selector.
- **"Versioned mode" / "Restore"** — eliminated entirely; orchestrator has no version concept.
- **"`SupportsResume`" / "`ResumableStore`" / "`SupportsRegionLock`" / "`SupportsEmptySelector`" / etc.** — capability struct is per-protocol; resume is a universal behaviour pattern, not a capability.
- **`rimsky_dispatch`** — replaced by `rimsky_worker_request` (Phase 5). The legacy table no longer exists.
- **`rimsky_lock_holders`** — replaced by `rimsky_claim_handle` (Phase 5). The legacy table no longer exists.
- **`rimsky_store_lifecycle`** — replaced by `rimsky_lifecycle_idempotency` (Phase 4).

---

## Producer-internal vocabulary (not part of Rimsky's protocol surface)

The terms below are used by some claim-producer implementations (the postgres and filesystem reference producers) but do not appear in the Rimsky↔producer wire protocol or the rimsky-side template grammar. They appear only in producer-specific documentation and config.

- **Pick policy** — An items-table queue convention some producers implement. The producer recognizes special-form selectors (recommended convention: `@policy-name`) and picks an item per its configured logic (FIFO queue, ring buffer, LIFO scratchpad, etc.). The postgres and filesystem reference producers both expose per-policy `on_commit_default` / `on_give_up_default` config in their own `config.yml`. The filesystem producer auto-discovers folder items by reading the configured sub-root, so `mkdir`/`rm -rf` is the insertion/removal mechanism. See `docs/claim-producer-author-guide.md`, `deploy/store-postgres.yml`, and `docs/history/2026-05-03-fs-store-pick-policies-design.md`.
- **`pick_policies`** — A producer's own config block listing named pick policies it implements. Each entry is keyed by the recognized selector form (e.g. `@review-queue`) and carries producer-specific configuration. Producer-internal — not part of `rimsky.yml`.
- **`release_to_back` / `release_to_head`** — Per-policy disposition actions in pick-policy producers' configs. Producer-internal; not visible to Rimsky.
- **Items-table `delete` (action)** — A per-policy disposition action in pick-policy producers that removes the row from the items table. Distinct from any rimsky-level concept.

---

## Control-plane v1 vocabulary

Per the modeling-layer contract §3-§6 (which supersedes the 2026-05-01 design doc).

- **Template**: a content-addressed bundle of node-defs, attribute schemas, claim/lock declarations, and frame-resolution config. The id is `sha256-<64-hex>` over an RFC 8785 JCS-canonicalized spec. Re-registering the same spec is a cheap no-op (idempotent on hash). Templates persist through four lifecycle states: `registered → deployed → undeployed`, with `deregistered` as the absent state.
- **Instance**: a running execution of a template, identified by a Rimsky-generated UUID. Instances bind to a specific template content hash at creation; tag movement does not migrate live instances. An instance's `instance_key` (nullable) is a caller-supplied dedup key.
- **Tag**: a movable alias from a string identifier to a template content hash. Stored in `rimsky_template_tags`. Hash-shape strings (the `sha256-<64-hex>` form) are rejected as tag identifiers so the `tag_or_hash` resolution stays unambiguous.
- **Deploy** / **undeploy** / **register** / **deregister**: the four template state transitions. `OnTemplate*` lifecycle events fire on each.
- **Lifecycle event**: one of the six `LifecycleSubscriber` RPCs fired at template or instance state transitions: `OnTemplateRegistered`, `OnTemplateDeployed`, `OnTemplateUndeployed`, `OnTemplateDeregistered`, `OnInstanceCreated`, `OnInstanceTerminated`. All subscribed peers implement all six; peers that don't react just return `nil` from each method.
- **Scope envelope** *(historical)*: the `(template_id, instance_id)` pair on `OpenRequest`. Now spelled with `template_hash` on the wire post-Phase-4.
- **Content hash**: the canonical SHA-256 of an RFC 8785 JCS-canonicalized template spec, prefixed with `sha256-`. Two semantically-identical specs produce identical hashes.

## CLI & compose vocabulary

- **Compose project**: the ownership scope declared in `rimsky-compose.yml`'s `project:` field. Format: `^[a-z][a-z0-9-]{0,62}$`. Used as a prefix on every compose-managed resource (`compose:<project>:<tag>`, `compose:<project>:<name>`); `compose up` reconciles only against resources with that prefix.
- **Compose manifest**: `rimsky-compose.yml`. Application-layer YAML declaring templates, tags, and persistent instances that should exist inside an already-running rimsky deployment. Apply-once-and-exit.
- **Context**: a named entry in `~/.rimsky/config.yml` mapping a friendly name to a control-api endpoint URL. Selected via `rimsky-cli ctx use <name>` or pinned per-manifest via the manifest's `context:` field.
- **Infra (operator-supplied)**: the deployment-host commands declared in the manifest's `infra:` block. Rimsky-invisible: `rimsky-cli dev up` shells out to `infra.up.command` with no introspection of what it does.
