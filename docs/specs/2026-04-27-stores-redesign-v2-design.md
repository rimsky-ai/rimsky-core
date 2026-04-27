# Stores Redesign v2 — Implementation Spec

**Status:** spec, ready for planning.
**Authoritative for:** the second-major-rewrite of `core/store/` and adjacent code paths after the prior stores-redesign landed.
**Companion documents:**
- `docs/glossary.md` — vocabulary reference (authoritative; this spec aligns to it).
- `docs/2026-04-26-stores-spec-scope.md` — brainstorm working document (decision history).
- `docs/2026-04-26-stores-redesign.md` — discursive design conversation (rationale, alternatives explored). §19 of that doc is the authoritative resolution where it differs from in-line text.
**Supersedes:** `docs/specs/2026-04-25-stores-redesign-design.md` substantially (see §1.1 for the relationship).

---

## 1. Goal

Refine the protocol and lock model that emerged from the prior stores-redesign, with three substantive changes:

1. **Verb-set rewrite (option Y).** Five protocol verbs replace today's `AcquireLock` / `OpenHandle` / `Commit` / `ReleaseLock` shape. Verbs operate on already-granted claims and split cleanly between the address-producing `Open` and the terminal-action verbs `Commit` / `Abandon` / `Delete` / `Release`.
2. **Pick policies are substrate-side.** No "claim store" kind; no "empty selector" protocol concept. A store accepts opaque selector text; substrate-recognized selector forms (recommended convention: `@policy-name`) trigger configured pick policies. One uniform protocol for regional and queue/ring-buffer style stores.
3. **Held claims via explicit inheritance.** The `held: true` flag dissolves; downstream nodes declare `inherits:` on a claim alias from the source. Auto-terminal fires resolution at holding-subgraph completion based on aggregate outcome — no per-terminal-leaf reconciliation, no first-delete-wins / last-released-wins.

Plus: capability struct collapses to one field (`write_semantics`), claim content (payload + address + region) is opaque-bytes via a broadened inertness invariant, and the protocol designed here is portable to out-of-process implementation in a follow-up cycle.

### 1.1 Relationship to the prior stores-redesign spec

The prior spec (2026-04-25) introduced stores, regions, locks, claims, attributes substitution, and the dispatch claim machinery. This spec rewrites:

- The `core/store/Store` interface (5 verbs, two-noun primitives split).
- The capability manifest (one field).
- `rimsky_lock_holders` schema (`claim_id` column drops; folds into `region_data`).
- `rimsky_claim_holders` schema (added `lock_holder_id` FK; dropped `actual_action` / `delete_won` / first-delete-wins reconciliation).
- The held-claim DSL (`held: true` → `inherits:` on downstream nodes).
- The substitution path surface (`{{claim.<alias>.address}}` / `{{claim.<alias>.region}}` added).
- Pick policies (substrate-side, not a separate kind).

This spec preserves blessed invariants 1, 2, 3, 4, 5, 6, 7, 8, 11, 12, 14 verbatim (state machine, dispatch claim bracketing, deterministic sorted lock acquisition, claimant-guarded release, verify-before-run, orphan-claim cutoff, advisory-lock scheduler tick, session advisory lock on migrations, userdata opacity, attributes validate twice, pure RegionsConflict).

This spec **revises**:

- **Blessed invariant 9** (lock state lives only in postgres) — strengthened with an explicit prohibition against store-side serialization on lock-shaped predicates (the §9-strategy-2 reader-lease serialization pattern is forbidden).
- **Blessed invariant 10** (atomic acquisition) — preserved for in-process implementations; will be revisited in the OOP cycle when `pgx.Tx` sharing is no longer available.
- **Blessed invariant 13** (held-claim resolution) — was first-delete-wins-and-last-released-wins reconciliation; now auto-terminal aggregate-outcome at holding-subgraph completion.

This spec **adds**:

- **Blessed invariant 15** — `Open` fires inside the acquisition transaction (in-process; preserves invariant 10 atomicity).
- **Blessed invariant 20** — claim content (payload, address, region) is inert in Rimsky.

The frame-resolution model (`docs/specs/2026-04-26-frame-resolution-design.md`) is preserved entirely. Attributes substitution machinery is extended to new paths but unchanged in shape. Operator-configured stores; supervisor-pool specialization; `accepted_executors`-style filtering — all preserved.

This spec **defers** to follow-up cycles:

- Out-of-process (RPC) implementation of the protocol. The protocol IS designed here; the wire format and atomicity-without-Tx-sharing redesign are the OOP cycle's work.
- `core/queue/DispatchQueue` `pgx.Tx` leak refactor (platform-state-backend pluggability concern).
- `staged_async`-capable substrate (no v1 store implementation exercises read-during-write semantics).

## 2. Non-goals (deliberately out of scope)

- **Out-of-process store implementation.** Wire protocol, supervisor↔store-service auth, deployment topology, discovery, and the atomicity-without-Tx-sharing redesign (revising invariant 10) all belong to a follow-up cycle.
- **Sidecar / versioned modes.** Versioned mode is permanently out (see §10). Sidecar mode is post-v1.
- **`staged_async` substrate implementation in v1.** Protocol supports it; no in-process store implementation exercises it.
- **Multi-store atomic commit.** Each store commits independently. No batched-commit verb.
- **Multi-tenant store provisioning.** A control-layer concern; lives in `docs/2026-04-26-control-layer.md`.
- **`core/queue/DispatchQueue` pgx leak refactor.** Out; platform-state-backend concern.
- **Reference encrypt-before-pass helper library.** Out; not a Rimsky concern (specs the protocol; implementers handle crypto).
- **Bridge framework / language SDKs.** Out; not a Rimsky concern.
- **OCI registries / package distribution / ecosystem mechanics.** Follow-up to OOP.
- **Migrations as compatibility shims.** Pre-v1: rewrite `001-initial.sql` in place; nuke dev DB before running new code.

## 3. Execution constraints

- The work is executed end-to-end by autonomous subagents; the user is not present.
- No interactive prompts.
- No remote-side actions (no `git push`, no PR creation, no Docker push, no calls to external services beyond what already exists — testcontainers, npm install).
- Tests use Docker (testcontainers-go for scenarios; docker-compose + testcontainers for the smoke fixture). Docker socket assumed available.
- Mandatory final checks: `go build ./...`, `go test ./... -race -count=1`, `make lint`, `make proto-gen` (if proto changed), `cd executors/claude-agent && npm install && npm test && npm run build`.
- Pre-v1: no production data; nuke dev DB on adoption.

## 4. Motivation (brief)

The prior stores-redesign exposed locks, claims, regions, and write semantics as first-class primitives. Walking the design end-to-end after it landed produced four refinements that this spec implements:

1. **The verb shape was redundant.** Three "produce an address" verbs (`ResolveRegion`, `Allocate`, `AcquireRead`) all did one thing from the executor's perspective — hand back an address — with substrate-side state lifecycle as a secondary differentiator. Collapsing to one parameterized `Open` removes the redundancy.
2. **"Empty selector" was a Rimsky-side concept that didn't earn its keep.** Rimsky never needs to know whether a substrate runs a pick policy behind a selector; the substrate decides what selectors mean. Pushing pick policies fully substrate-side dissolves the `claim-store` kind, simplifies the capability struct, and supports multiple named pick policies per store naturally.
3. **Multi-terminal first-delete-wins / last-released-wins was load-bearing for an unsafe access pattern.** Once held claims propagate the live address (via inheritance), per-terminal-leaf reconciliation is unsafe — a delete during one node's read would corrupt. Auto-terminal at subgraph completion eliminates the unsafety and dramatically simplifies the algorithm.
4. **The `held: true` flag conflated two distinct things.** Lifetime extension (for exclusion) vs. address propagation (for shared substrate access) had been a single switch. Splitting them — value-pass via captured attributes, claim-pass via explicit inheritance — gives the graph author finer control and makes the "no hold + pass address" combination structurally impossible.

## 5. Vocabulary

The authoritative vocabulary lives in `docs/glossary.md`. This section summarizes the load-bearing terms; the glossary holds the full definitions.

### 5.1 Two primitives

- **Claim** — a row in `rimsky_lock_holders` with `(store_name, region_data, intent)`. Store-bound. Halts node dispatch when conflicting claims are held on overlapping regions. Mode is derived from `(intent, store.write_semantics)` at conflict-check time, not stored.
- **Named lock** — a row in `rimsky_lock_holders` with `lock_name`. Non-store. Halts node dispatch when the count of holders for the same name equals the configured limit. Limit is operator-configured (see §15.2); templates reference by name only. There is no `mode` discriminator: a "mutex" is operator-configured as `limit: 1`; a "counting semaphore" is operator-configured as `limit: N>1`. The supervisor's conflict predicate is uniformly `count(holders) >= limit`.

These are different primitives with different shapes, identities, coexistence rules, and lifecycle verbs. They share `rimsky_lock_holders` because they share operational machinery (acquisition tx, heartbeat, orphan reap, claimant-guarded release, observability). The CHECK constraint enforces exactly-one-of (`lock_name`) or (`store_name` + `region_data`) populated.

### 5.2 Region

`(store, selector)`. The conceptual unit of "what's claimed." Also the concrete opaque bytes stored on the lock-holder row (column name `region_data` for historical reasons).

### 5.3 Selector

Opaque text the graph author supplies (post-substitution). Substrate parses; Rimsky doesn't classify or validate. May contain `{{...}}` substitution directives resolved at dispatch.

Always present at the protocol level — there is no "selector absent" state. Pick policies are expressed via substrate-recognized selector forms (recommended convention: `@policy-name`).

### 5.4 Address

Substrate-native pointer the executor uses to access claimed state. Returned by `Open`. Substitutable via `{{claim.<alias>.address}}` in inheriting nodes.

### 5.5 Payload

Substrate-supplied data captured at acquisition (e.g., a picked queue item's user data). Substitutable via `{{claim.<alias>.payload.<field>}}`.

### 5.6 Intent

`r` (read) or `rw` (read-write). The graph author's declaration of what the executor will do with the claim.

### 5.7 Alias

Per-claim name within a node. Used in substitution paths and `inherits:` references. Defaults to the store name; can be set explicitly when a node has multiple claims on the same store.

### 5.8 Acquirer / Inheritor / Holding subgraph

- **Acquirer** — the node that calls `Open` for a claim. One per claim.
- **Inheritor** — a downstream node that declares `inherits:` on the acquirer's claim alias. Inheritance extends the claim's lifetime to cover the inheritor's run.
- **Holding subgraph** — acquirer + directly-declared inheritors. Computed at template deploy from explicit `inherits:` declarations. Direct only — does not propagate transitively through dep chains.

### 5.9 Auto-terminal

The mechanism by which a held claim's resolution fires when the holding subgraph completes. Aggregate outcome — all-success → `on_commit`; any-failure → `on_give_up` — drives the substrate verb. No per-terminal-leaf reconciliation.

### 5.10 Pick policy

A substrate-side mechanism that recognizes special-form selectors (recommended convention: `@policy-name`) and picks an item per its configured logic (FIFO queue, ring buffer, LIFO scratchpad, etc.). Configured in the store's `pick_policies` block. Schema is substrate-specific; Rimsky doesn't introspect.

### 5.11 Attributes

Unchanged from the prior spec. A node's attributes is a per-run typed data object with schema-declared properties. Source directives populate from `{{deps.<node>.<field>}}`, `{{claim.<alias>.<...>}}`, or `{{params.<key>}}`. Persisted in `rimsky_node_attributes`. See §12.

### 5.12 Userdata

Unchanged from the prior spec. Purely executor configuration. Rimsky never parses, substitutes, or validates userdata. Substitution syntax inside userdata values is treated as literal bytes.

## 6. Protocol verbs

The store interface defines five verbs. Each is a contract any store implementation must honor — in-process today, RPC-based in a follow-up cycle.

### 6.1 Verb signatures

```
Open(ctx, ClaimSpec) → (Address, error)
Commit(ctx, region, address, policy_override?) → error
Abandon(ctx, region, address, policy_override?) → error
Delete(ctx, region) → error
Release(ctx, region, address) → error
```

`policy_override` is meaningful only for claims served by a pick policy; ignored otherwise. `Address` is opaque-bytes from Rimsky's perspective.

### 6.2 Semantics

**`Open(ctx, ClaimSpec) → Address`** — Produce a substrate-native address for the executor and register whatever substrate-side state the `(intent, write_semantics)` combination requires (staging area, snapshot, MVCC transaction, or nothing).

The substrate detects whether this is a fresh acquisition or a resumed one by lookup against its own state, keyed by the lock-holder identity. There is no `resumed` flag — resume is universal (the supervisor preserves the lock-holder row and calls `Open` again; the substrate handles the resumed-vs-fresh branch internally).

For pick-policy claims (selectors the substrate recognizes as policy-form), `Open` invokes the configured pick policy and returns the picked item's address. The picked identifier becomes the `region_data` on the lock-holder row.

**`Commit(ctx, region, address, policy_override?) → error`** — For regional `rw` claims on `staged_*` substrates: atomically publish the staging area's contents into the live region. For pick-policy claims: apply the configured `on_commit` action (`delete` / `release_to_back` / `release_to_head`) — overridable via `policy_override`. For `direct`-mode regional `rw` claims: substrate no-op (writes already live; `Commit` is a confirmation hook).

**`Abandon(ctx, region, address, policy_override?) → error`** — For regional `rw` claims on `staged_*`: discard the staging area without publishing. For pick-policy claims: apply the configured `on_give_up` action — overridable via `policy_override`. For `direct`-mode `rw`: degenerate (cannot undo direct writes); store-author guidance documents this as an honest limitation. Not called for read-only claims.

**`Delete(region) → error`** — Remove the region's live data. A third terminal action alongside `Commit` and `Abandon` for nodes whose intent is deletion. Regional claims only (pick-policy claims express deletion via `Commit` + `policy_override = delete`).

**`Release(region, address) → error`** — Tear down substrate-side read state (snapshot, MVCC transaction) for a read claim. Fires only when the substrate registered such state (relevant for `staged_async` substrates, none of which ship in v1).

### 6.3 Verb-firing matrix per claim shape

| Claim shape | Mode | Verbs invoked at terminal |
|---|---|---|
| Regional `r` on `direct` / `staged_blocking` | (none) | None — lock-holder row deletion is sufficient |
| Regional `r` on `staged_async` | (read-lease) | `Release(region, address)` |
| Regional `rw` on `direct` | direct | `Commit(region, address)` (substrate no-op) or `Delete(region)` or `Abandon` (degenerate; supervisor logs and proceeds) |
| Regional `rw` on `staged_*` | staged | `Commit(region, address)` or `Abandon(region, address)` or `Delete(region)` |
| Pick-policy claim | (any) | `Commit(region, address, policy_override?)` or `Abandon(region, address, policy_override?)` |

### 6.4 Substrate-side commit failures

Substrate errors during `Commit` (merge conflicts, conditional-put misses, serialization failures, atomic-swap conflicts) surface as `Commit` returning an error. These route through Rimsky's existing `retry / give_up / invalidate(targets)` vocabulary like any executor-side error. No new error class. Same for `Abandon`, `Delete`, `Release`.

### 6.5 Substrate-side fences

Substrate-side fences are brief and applied at commit. The dominant pattern is staging + atomic swap: filesystem rename, SQL `ALTER TABLE` swap, S3 manifest pointer flip, Redis `RENAME`, git merge. **Run-spanning substrate locks (open transactions held across an executor's whole run) are an anti-pattern** — they duplicate the orchestrator's claim machinery and burn substrate resources. Store-author guide restates this principle.

## 7. Region & selector model

### 7.1 Selectors are opaque text

A selector is whatever the graph author writes in `selector:`, post-substitution. The substrate parses it and decides what it means. Rimsky never classifies (static / dynamic / empty), validates against substrate grammar, or interprets selector content.

The graph DSL accepts any string. Substitution directives are resolved at dispatch time. The post-substitution string becomes the selector text passed to the substrate via `Open`.

### 7.2 Pick policies

A store implementation may recognize special-form selectors as triggers for configured pick policies. Recommended convention: `@policy-name` (e.g. `@review-queue`, `@docs-ring`, `@scratchpad`). The convention is **not required** — substrates may use any disambiguating syntax their grammar supports — but reference store implementations follow `@`-prefixed conventions for cross-substrate consistency.

A store's `pick_policies` config block lists the named policies it supports. Each entry is keyed by the recognized selector form and carries substrate-specific config (item path, ordering, `on_commit_default`, `on_give_up_default`, visibility timeout, etc.). The schema is substrate-defined; Rimsky doesn't standardize it.

Multiple pick policies per store are supported. A single `postgres` store can configure `@review-queue` (FIFO queue) alongside `@audit-ring` (ring buffer) alongside `@scratchpad/<id>` (scratch allocation). All three are accessed via the same five verbs; the substrate dispatches internally based on the resolved selector.

### 7.3 Region data on the lock-holder row

`region_data` (JSONB) on `rimsky_lock_holders` carries the substrate's identifier for the claimed region. For static-selector claims it's the resolved selector text. For pick-policy claims it's the substrate-chosen item identifier (e.g., `"item-5"` for a queue). Both are opaque bytes from Rimsky's perspective.

`region_data` is the sole identifier — there is no separate `claim_id` column. The prior spec's `claim_id` column drops in this design.

### 7.4 Selector substitution

Selectors may contain `{{...}}` substitution directives — `{{deps.<node>.<field>}}`, `{{claim.<alias>.<...>}}`, `{{params.<key>}}`. Substitution runs at dispatch time, before lock acquisition. The post-substitution string is what the substrate sees.

Substitution failure → `template_resolution_failed` (existing error class); routes through the node's policy chain.

A selector template that resolves to an empty string is `template_resolution_failed`, not an item-claim signal. Item-claim semantics are expressed via substrate-recognized selector forms (per §7.2), not via empty-string selectors.

### 7.5 Validation surface

There is **no deploy-time `ValidateSelector` hook on the store interface in v1.** Selector interpolation defangs deploy-time validation: the resolved selector is unknown until dispatch.

Authoritative validity check is the store's response to `Open` at dispatch time. If the substrate cannot honestly handle the resolved selector under the requested intent and write_semantics, `Open` returns an error that routes through `retry / give_up / invalidate`.

Deploy-time DSL validation still rejects: undeclared store references, undeclared named-lock references, malformed substitution paths, attribute schema errors, `{{claim.<alias>.<...>}}` references where the alias isn't acquired or inherited, `inherits:` references to non-existent upstream claims. It does NOT validate selector text against substrate grammar.

## 8. Modes — `write_semantics`

A store's `write_semantics` field declares how writes coordinate with readers. Three values:

- **`direct`** — Writes hit live data. No staging area. r×rw on overlapping regions blocks (sync semantics).
- **`staged_blocking`** — Writes go to a substrate-private staging area; `Commit` does atomic swap into live. `Abandon` discards staging. r×rw on overlapping regions blocks (sync semantics).
- **`staged_async`** — Writes go to a staging area; reads see a stable view of live state during writes (substrate-native MVCC or snapshot delegation). r×rw on overlapping regions does NOT block (async semantics).

### 8.1 Default if unspecified

The substrate's max capability. Operator can downgrade (force `direct` on a `staged_blocking`-capable store) but not upgrade (cannot configure `staged_async` on a substrate that doesn't support it). Validation at config-load time rejects upgrade attempts with a clear error.

### 8.2 Substrate max declaration

Each store kind's factory exposes its max via `Factory.MaxWriteSemantics() WriteSemantics`. The factory hardcodes (or otherwise exposes) the substrate's ceiling; operator config is checked against it on `BuildAll`.

### 8.3 No per-region overrides

Per-region `write_semantics` overrides are out for v1. If a substrate needs different semantics for sub-regions, the cleaner expression is two distinct stores pointing at the same underlying storage, each with its own `write_semantics`.

### 8.4 Verb-sequence-by-mode

`direct`: `Open` returns the live address; `Commit` is a substrate no-op; `Abandon` is degenerate; `Delete` valid for region removal. Same `rimsky_lock_holders` machinery as staged modes; only the conflict predicate (§13.2) and substrate-side state behavior differ.

`staged_blocking` / `staged_async`: `Open` provisions a staging area; `Commit` atomically swaps; `Abandon` discards; `Delete` valid.

### 8.5 Mode coexistence matrix

A claim's effective mode is `(sync|async, r|w)` derived from `(intent, store.write_semantics)` at conflict-check time. Not stored on the lock-holder row.

| | sync-r | sync-w | async-r | async-w |
|---|---|---|---|---|
| **sync-r** | ✅ | ❌ | (n/a) | (n/a) |
| **sync-w** | ❌ | ❌ | (n/a) | (n/a) |
| **async-r** | (n/a) | (n/a) | ✅ | ✅ |
| **async-w** | (n/a) | (n/a) | ✅ | ❌ |

- **Sync block** (`direct` / `staged_blocking`): r×r ✅; everything else ❌.
- **Async block** (`staged_async`): r×r ✅, r×w ✅, w×w ❌.
- Cross-quadrant cells are n/a — two claims on the same store share its `write_semantics`.
- The `w×w ❌` cells in both blocks are the structural single-writer-per-region rule (blessed invariant 4).

### 8.6 Store-side serialization is forbidden (invariant 9 restated)

A store implementation **does not internally serialize on lock-shaped predicates.** Specifically: the §9-strategy-2 "reader-lease serialization" pattern (substrate tracks active read leases; writers block at the substrate boundary) is not a valid implementation choice for `staged_async`. Honest support requires snapshot delegation (substrate creates a stable view materialized at `Open(read)`) or native MVCC pass-through (substrate opens a snapshot transaction at `Open(read)`, ends it at `Release`). A substrate that cannot honestly provide stable reads during writes declares `staged_blocking` (or `direct`) and lets the scheduler do serialization.

## 9. Capability manifest

```go
package store

type Capabilities struct {
    WriteSemantics WriteSemantics
}

type WriteSemantics string

const (
    WriteSemanticsDirect         WriteSemantics = "direct"
    WriteSemanticsStagedBlocking WriteSemantics = "staged_blocking"
    WriteSemanticsStagedAsync    WriteSemantics = "staged_async"
)
```

One field. Future capabilities can be added as struct fields without breaking the interface.

### 9.1 Eliminated capability fields

The prior spec's capability fields all drop:

- `SupportsRegionLock` — tautological under the new model; every store implements `Open(region, intent)` with selector text.
- `SupportsClaim` / `SupportsEmptySelector` — pick policies are substrate-side; not a Rimsky-protocol-level capability.
- `SupportsResume` — resume is a universal behaviour pattern, not a capability. Substrate handles resumed-vs-fresh internally via state lookup.
- `SupportsRestore` — versions are eliminated entirely (see §10).
- `SupportsAtomicMulti` / `commit_atomicity_scope` — `Commit(region, address)` is single-region by signature; multi-store atomic commit is a non-goal; substrate-internal multi-region atomicity is invisible to Rimsky.
- `SupportsDiscard` / `read_during_write` — collapsed into `write_semantics`.
- `async_supports_dynamic_selectors` — selector interpolation defangs deploy-time classification.
- `KeepVersionsMax` — versions eliminated.
- `payload_encryption` — informational only; Rimsky doesn't act on it. Encrypt-before-pass is operator practice, not a Rimsky-tracked capability.

## 10. Versions — eliminated

Rimsky has **no version concept.** No version tracking, no change-signal per region, no GC pin, no outstanding-claim-count per active version, no `versioned` mode, no `Restore` verb.

- **Cascade trigger** is "node committed with `changed=true`" via existing node-state transitions. `changed` is producer-declared on the executor's `Complete` event (per blessed-invariant family covering producer trust). `Commit` does not return `changed` — it returns success or error.
- **GC** is the substrate's responsibility entirely. Out-of-band, ambient, substrate-internal. Rimsky has no opinion.
- **Restore / replay / time-travel** are substrate-specific extension verbs that Rimsky never sees. Substrates that retain history (git, S3 with versioning) may expose admin operations for these; not part of the workload-store protocol.

The prior spec's `RestoreVersion` plumbing (in `core/scheduler/invalidate.go`, `controlapi/nodes.go`, etc.) was deleted in the prior work. This spec confirms the deletion is permanent — `versioned` mode does not exist; it is not deferred to post-v1.

## 11. Store interface (Go)

### 11.1 Package layout

- **`core/store/`** — interfaces, value types, registry, transaction-context helpers. Imports `core/shared/` and `pgx/v5` (for the transaction-context helpers; `pgx.Tx` is the only `pgx` symbol leaked through this package's public surface).
- **`core/store/filesystem/`** — direct-mode filesystem store. Imports `core/store/` and stdlib.
- **`core/store/postgres/`** — postgres store. Renamed from `claimstorepg`. Supports regional access with optional `pick_policies` block (the prior items-table mechanism becomes one named pick policy). Imports `core/store/`, `core/shared/`, `pgx/v5`.
- **`core/store/stub/`** — test fixture. Rewritten to new verb set.
- **`core/attributes/`** — substitution engine, JSON Schema validation, callback handler. Unchanged in shape; new substitution paths added (per §12).

### 11.2 Capabilities (Go)

```go
package store

type Capabilities struct {
    WriteSemantics WriteSemantics
}
```

### 11.3 Specs

```go
package store

// ClaimSpec — substrate-bound primitive.
//
// There is no PolicyOverride field on ClaimSpec. Substrate-internal action
// vocabulary (e.g., delete / release_to_back / release_to_head for pick
// policies) is plumbed at terminal time via the policyOverride argument on
// Commit / Abandon — not at acquisition. Per-claim resolution actions are
// declared on the acquiring node's claim_resolutions block (§14.3) and
// passed to the verbs at terminal by the supervisor.
type ClaimSpec struct {
    StoreName string  // name of the configured store
    Selector  string  // opaque text (post-substitution)
    Intent    Intent  // "r" | "rw"
    Alias     string  // per-claim name within node; defaults to StoreName
}

type Intent string

const (
    IntentRead      Intent = "r"
    IntentReadWrite Intent = "rw"
)

// NamedLockSpec — non-substrate primitive.
type NamedLockSpec struct {
    Name string  // operator-configured name
}
```

`ClaimSpec` and `NamedLockSpec` are distinct types with no common interface. Two primitives, two types.

### 11.4 Address and ClaimResult

```go
package store

// Address is the substrate-native pointer the executor uses.
// Opaque to Rimsky (json.RawMessage); substrate-specific shape.
type Address json.RawMessage

// ClaimResult bundles the three substrate-supplied outputs of a claim
// acquisition. All three are opaque-bytes from Rimsky's perspective; the
// substitution engine extracts named-field paths only at the leaf
// extraction site.
type ClaimResult struct {
    Address json.RawMessage  // substrate-native pointer
    Payload json.RawMessage  // substrate-supplied data captured at acquisition
    Region  json.RawMessage  // substrate's identifier (resolved selector OR pick)
}
```

### 11.5 Store interface

```go
package store

// Store is the universal interface every store implementation must satisfy.
//
// @blessed-invariant: Lock state lives only in postgres.
//   No Store implementation persists lock state. Stores may persist *data*
//   state (e.g. items-table flips), but the question "is anyone holding
//   lock X" is answered exclusively by rimsky_lock_holders.
//
// @blessed-invariant: Store implementations do not internally serialize on
//   lock-shaped predicates. The §9-strategy-2 reader-lease serialization
//   pattern is not a valid implementation choice for staged_async; honest
//   support requires snapshot delegation or native MVCC pass-through.
type Store interface {
    Kind() string
    Name() string
    Capabilities() Capabilities

    // RegionsConflict — pure overlap predicate.
    //
    // @blessed-invariant: pure; no side effects, no external state read;
    // deterministic on inputs.
    RegionsConflict(a, b []byte) bool

    // UnmarshalRegion — pure deserialization for use with RegionsConflict.
    //
    // @blessed-invariant: same purity contract as RegionsConflict.
    UnmarshalRegion(raw []byte) ([]byte, error)

    // Open — produce a substrate-native address for the executor and
    // register substrate-side state. Inside the supervisor's atomic
    // acquisition transaction (§13.3); the supervisor passes its open
    // *pgx.Tx via ctx (key store.txKey, accessed through TxFromContext)
    // so substrate writes participate in the same transaction.
    //
    // Substrate detects resumed-vs-fresh by lookup against its own state
    // keyed by lock-holder identity. There is no resumed flag; resume is
    // universal.
    Open(ctx context.Context, spec ClaimSpec) (ClaimResult, error)

    // Commit — publish staging into live (regional rw on staged_*) or
    // apply on_commit policy (pick-policy claims). For direct rw: substrate
    // no-op. policy_override is meaningful only for pick-policy claims.
    Commit(ctx context.Context, region []byte, address []byte, policyOverride string) error

    // Abandon — discard staging or apply on_give_up policy. For direct
    // rw: degenerate (cannot undo direct writes); supervisor logs and
    // proceeds. policy_override is meaningful only for pick-policy claims.
    Abandon(ctx context.Context, region []byte, address []byte, policyOverride string) error

    // Delete — remove the live region. A third terminal action alongside
    // Commit and Abandon for nodes whose intent is deletion. Regional
    // claims only.
    Delete(ctx context.Context, region []byte) error

    // Release — tear down substrate-side read state for a read claim.
    // Fires only when the substrate registered such state (staged_async
    // substrates; not exercised by any v1 store implementation).
    Release(ctx context.Context, region []byte, address []byte) error
}

// Factory builds a Store from per-store config.
type Factory interface {
    Kind() string
    MaxWriteSemantics() WriteSemantics
    Build(name string, cfg map[string]any) (Store, error)
}

// Registry — unchanged from prior spec; per-process set of factories +
// built stores. Built from process YAML at startup.
```

The prior spec's `LockSpec` discriminated union, `RegionLockSpec` / `ClaimLockSpec` / `NamedLockSpec`, `LockHandle`, `NativeHandle` (sealed interface), `ClaimableStore` sub-interface, `ResumableStore` sub-interface, `HasPriorWork`, `OpenHandle`, `AcquireLock`, `ReleaseLock`, `ReleaseAction` enum — all dissolve.

### 11.6 Transaction-context helpers

Same shape as the prior spec's §8.4.1: `WithTx(ctx, tx) context.Context` and `TxFromContext(ctx) (pgx.Tx, bool)`. The supervisor's atomic acquisition transaction passes its `*pgx.Tx` via context; the store's `Open` (and any other in-tx operations) extract it for substrate-side state mutations that must commit atomically with the lock-holder row.

## 12. Schema

### 12.1 `rimsky_migrations` (preserved verbatim)

Unchanged.

### 12.2 `rimsky_templates` (preserved verbatim)

Unchanged.

### 12.3 `rimsky_instances` (preserved verbatim)

Unchanged.

### 12.4 `rimsky_nodes` (preserved verbatim from prior spec; no further changes)

### 12.5 `rimsky_supervisors` (preserved verbatim)

### 12.6 `rimsky_dispatch` (preserved verbatim from prior spec)

### 12.7 `rimsky_schedules` (preserved verbatim)

### 12.8 `rimsky_events` (preserved verbatim; payload kinds extended for inheritance + auto-terminal events)

### 12.9 `rimsky_node_attributes` (preserved verbatim)

### 12.10 `rimsky_lock_holders` (modified — `claim_id` dropped; `address` and `intent` added)

```sql
CREATE TABLE rimsky_lock_holders (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lock_kind            TEXT NOT NULL CHECK (lock_kind IN ('named', 'region')),
    -- For named locks:
    lock_name            TEXT,
    -- For region claims:
    store_name           TEXT,
    region_data          JSONB,
    address              JSONB,            -- substrate-supplied address from Open;
                                           -- needed by Commit/Abandon/Release/Delete at
                                           -- terminal AND by orphan reaper. Opaque bytes;
                                           -- inert in Rimsky per invariant 20.
    intent               TEXT,             -- 'r' | 'rw' for region claims; null for named
    -- Common:
    holder_supervisor_id TEXT NOT NULL,
    holder_node_id       UUID NOT NULL,    -- the acquirer; for held claims, this is the
                                           -- node that called Open. Inheritor membership
                                           -- is tracked in rimsky_claim_holders.
    claimed_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_heartbeat_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at           TIMESTAMPTZ NOT NULL,
    frame_id             UUID,             -- observability-only; not consulted at
                                           -- acquisition, eligibility, orphan-reap, or
                                           -- held-claim resolution
    CHECK (
        (lock_kind = 'named'  AND lock_name IS NOT NULL AND store_name IS NULL AND region_data IS NULL AND address IS NULL AND intent IS NULL)
        OR
        (lock_kind = 'region' AND lock_name IS NULL AND store_name IS NOT NULL AND region_data IS NOT NULL AND intent IN ('r', 'rw'))
        -- Note: address may be NULL for region claims even though store_name + region_data
        -- are populated, because Open writes the address only after a successful return
        -- (within the same transaction); the row is inserted with NULL address and updated
        -- by the supervisor's atomic-acquisition flow per §13.3.
    )
);

CREATE INDEX idx_rimsky_lock_holders_supervisor
    ON rimsky_lock_holders(holder_supervisor_id);
CREATE INDEX idx_rimsky_lock_holders_node
    ON rimsky_lock_holders(holder_node_id);
CREATE INDEX idx_rimsky_lock_holders_named
    ON rimsky_lock_holders(lock_name)
    WHERE lock_kind = 'named';
CREATE INDEX idx_rimsky_lock_holders_region
    ON rimsky_lock_holders(store_name)
    WHERE lock_kind = 'region';
CREATE INDEX idx_rimsky_lock_holders_expires
    ON rimsky_lock_holders(expires_at)
    WHERE expires_at IS NOT NULL;
```

Changes from prior spec:
- `lock_kind` enum reduced from `(named, region, claim)` to `(named, region)`. The `claim` kind dissolves (pick-policy claims are just region claims with substrate-chosen `region_data`).
- `claim_id` column dropped. Substrate's identifier lives in `region_data`.
- `address` column added — substrate-supplied address from `Open`, needed by terminal verbs and the orphan reaper.
- `intent` column added (`r` | `rw` | NULL for named locks).
- `frame_id` is observability-only (clarification).
- Indexes declared explicitly: by supervisor, node, named-lock name, store name (for region claims), and `expires_at` (for orphan-reap sweep).

### 12.11 `rimsky_claim_holders` (modified — `lock_holder_id` FK added; `actual_action` / `delete_won` columns dropped)

```sql
CREATE TABLE rimsky_claim_holders (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lock_holder_id  UUID NOT NULL REFERENCES rimsky_lock_holders(id) ON DELETE CASCADE,
    holder_node_id  UUID NOT NULL,                                     -- a node in the holding subgraph
    state           TEXT NOT NULL CHECK (state IN ('active', 'completed', 'failed')),
    completed_at    TIMESTAMPTZ,
    frame_id        UUID,                                              -- observability-only
    UNIQUE (lock_holder_id, holder_node_id)
);

CREATE INDEX idx_rimsky_claim_holders_lock_holder
    ON rimsky_claim_holders(lock_holder_id);
CREATE INDEX idx_rimsky_claim_holders_node
    ON rimsky_claim_holders(holder_node_id);
CREATE INDEX idx_rimsky_claim_holders_active_subgraph
    ON rimsky_claim_holders(lock_holder_id)
    WHERE state = 'active';
```

Changes from prior spec:
- `lock_holder_id` FK added (with `ON DELETE CASCADE` so claim-holder rows clean up when the lock-holder row is deleted at auto-terminal); sibling-matching predicate becomes `WHERE lock_holder_id = ?`.
- `claim_id` column dropped (substrate's identifier lives on `rimsky_lock_holders.region_data`, accessible via FK).
- `actual_action` and `delete_won` columns dropped — under auto-terminal (§14.4), there is single resolution per held claim, no first-delete-wins reconciliation.
- `on_commit` / `on_give_up` columns dropped from this table — resolution declarations live in template metadata (declared on the acquirer per §14.3); the supervisor reads them at auto-terminal time, not from this table.
- `state` enum gains `'failed'` to record subgraph nodes that failed (informs aggregate outcome).
- `frame_id` is purely observability — no algorithmic role under auto-terminal.

#### 12.11.1 Claim-holder row lifecycle

For each held claim:

1. **At acquirer's `Open` (inside the §13.3 atomic-acquisition transaction):** if the claim is held (i.e., the holding subgraph computed at template deploy contains > 1 nodes), the supervisor inserts one `rimsky_claim_holders` row per holding-subgraph member, all with `state = 'active'`. (For non-held claims — holding subgraph has only the acquirer — no `rimsky_claim_holders` rows are inserted; the lock-holder row alone tracks the lifetime.)
2. **At each subgraph member's terminal:** the supervisor updates that member's row to `state = 'completed'` (success) or `state = 'failed'` (give-up / failure). Single transaction with the node's terminal state transition.
3. **Auto-terminal trigger:** if all rows for the lock-holder are in `'completed'` or `'failed'`, the supervisor (whichever one's terminal triggered the condition) fires the auto-terminal verb per §14.4 and deletes the lock-holder row. Cascade FK cleans up the claim-holder rows.

The `state='active'` filter on the auto-terminal trigger query, combined with the SQL row lock on the lock-holder row, prevents two concurrently-terminating subgraph members from both firing the resolution.

### 12.12 Postgres pick-policy items-table contract (generalized)

A postgres store may configure one or more pick policies that use an items-table pattern. The schema below is the contract any such items-table must satisfy. Each pick-policy entry under `pick_policies:` references its own items-table by name.

```sql
CREATE TABLE <items_table_name> (
    item_id       TEXT PRIMARY KEY,
    payload       JSONB NOT NULL,
    state         TEXT NOT NULL CHECK (state IN ('available', 'in_progress', 'completed')),
    claim_token   TEXT,                          -- supervisor-set on transition to in_progress;
                                                  -- claimant-guard for the substrate's own state changes
    claimed_at    TIMESTAMPTZ,
    enqueued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    priority      INTEGER NOT NULL DEFAULT 0,    -- higher = picked first
    sequence      BIGSERIAL                      -- FIFO ordering tie-breaker
);

CREATE INDEX idx_<items_table_name>_available
    ON <items_table_name>(priority DESC, sequence ASC)
    WHERE state = 'available';
```

The `state` column tracks substrate-side data state: `'available'` (pickable), `'in_progress'` (claimed), `'completed'` (post-`Commit` with `delete` action would have removed the row entirely, so `'completed'` here represents post-`Commit` with non-delete actions for historical retention; ring-buffer-style policies typically transition back to `'available'` rather than `'completed'`).

The pick-policy at `Open` time:
- `SELECT … FOR UPDATE SKIP LOCKED` to find the highest-priority `'available'` row.
- `UPDATE` it to `'in_progress'`, set `claim_token` and `claimed_at`.
- Return its `item_id` as `region_data` and its `payload` as `payload` in the `ClaimResult`.

The pick-policy at `Commit`/`Abandon` time (driven by `policyOverride`):
- `release_to_back`: `UPDATE … SET state = 'available', claim_token = NULL, claimed_at = NULL, sequence = nextval('<items_table_name>_sequence_seq')`.
- `release_to_head`: `UPDATE … SET state = 'available', claim_token = NULL, claimed_at = NULL, priority = priority + 1` (or similar mechanism to push to head).
- `delete`: `DELETE FROM <items_table_name> WHERE item_id = $1`.

Visibility timeout (per pick-policy config; default 300s) is a backstop. Rimsky's heartbeat is authoritative: the orphan reaper releases the lock-holder when expired and the substrate's `Abandon` resets the items-table row. The pick-policy's items-table sweep — owned by the scheduler, once per scheduler tick — handles substrate-side cleanup when the row's `claimed_at` is older than the visibility timeout AND no `rimsky_lock_holders` row exists for the item:

```sql
UPDATE <items_table_name>
   SET state = 'available', claim_token = NULL, claimed_at = NULL
 WHERE state = 'in_progress'
   AND claimed_at < now() - (<visibility_timeout_seconds> * interval '1 second')
   AND NOT EXISTS (
         SELECT 1 FROM rimsky_lock_holders
          WHERE store_name = '<this_store_name>'
            AND region_data = to_jsonb(<items_table_name>.item_id)
       );
```

The `NOT EXISTS` clause guarantees the heartbeat-driven release path runs first under healthy conditions.

### 12.13 Tables removed

None new in this spec. The prior spec's `rimsky_resources` and related tables were already removed.

## 13. Locks & dispatch queue

### 13.1 Dispatch eligibility

A node is eligible to dispatch when:
- Dependencies are fresh.
- Required stores (per template's `stores:` list) are present in the supervisor pool's local registry.
- Required named locks (per template's `locks:` list) are configured in the operator's `named_locks:` block.
- Conflict predicate (§13.2) returns no conflict for any of the node's required claims against existing lock-holder rows.
- Named-lock counts are below their configured limits.

### 13.2 Conflict predicate (two-layer)

For each candidate region claim, conflict = `RegionsConflict(a, b)` AND mode-incompatible.

- **Region overlap** is store-side (`Store.RegionsConflict`, blessed-invariant pure).
- **Mode coexistence** is supervisor-side, evaluated against the §8.5 matrix using `(intent, store.write_semantics)` for both candidate and existing holder.

For named locks, conflict = (`count(holders for this name) >= limit`).

The supervisor's eligibility evaluator computes both layers per candidate claim against existing holders for the same store, using sorted-order acquisition (§13.7) for deterministic tie-breaking.

### 13.3 Atomic acquisition (preserved invariant 10)

The supervisor's acquisition transaction:

1. `BEGIN`.
2. `SELECT … FOR UPDATE` the dispatch row.
3. For each named-lock requirement: under `pg_advisory_xact_lock(hashtext('rimsky_lock:' || name))`, recount holders; if `count < limit`, insert a `rimsky_lock_holders` row.
4. For each claim requirement:
   a. Re-load existing region holders for the store (under `FOR UPDATE`-equivalent locking).
   b. Re-evaluate conflict predicate.
   c. If no conflict: insert a `rimsky_lock_holders` row with `address = NULL`.
   d. Call `Store.Open(ctx, spec)` with the open `pgx.Tx` shared via `store.TxFromContext(ctx)`. The store's substrate-side state mutations participate in the same transaction.
   e. `UPDATE` the lock-holder row's `address` column with the returned `ClaimResult.Address` bytes.
   f. If the claim is held (holding subgraph from template deploy has > 1 members): insert one `rimsky_claim_holders` row per holding-subgraph member, all with `state = 'active'`, FK'd to the lock-holder row.
5. Update the dispatch row's `claimed_by` to this supervisor.
6. `COMMIT`.

Either the transaction commits (lock-holder rows + their `address` updates + claim-holder rows + substrate-side state mutations + dispatch claim ALL committed) or it rolls back (none committed). This preserves blessed invariant 10 for in-process implementations. The OOP cycle will revisit the atomicity model.

### 13.4 Heartbeat (revised for held claims)

Each supervisor's heartbeat tick refreshes `last_heartbeat_at` and `expires_at` for every `rimsky_lock_holders` row whose lifetime should currently be active. Two cases:

- **Standard case (acquirer is still running on this supervisor):** refresh rows where `holder_supervisor_id = $1` AND `holder_node_id` is currently in `'running'` state assigned to this supervisor. This is the prior spec's heartbeat predicate, unchanged.
- **Held-claim case (acquirer has terminated, but holding subgraph has active inheritors):** refresh rows where `holder_supervisor_id = $1` AND there exists an active inheritor (a `rimsky_claim_holders` row referencing this lock-holder with `state = 'active'`) whose node is currently `'running'` (assigned to *any* supervisor — not necessarily this one).

Combined heartbeat query:

```sql
UPDATE rimsky_lock_holders lh
   SET last_heartbeat_at = now(),
       expires_at = now() + ($2 * interval '1 second')
 WHERE lh.holder_supervisor_id = $1
   AND (
        -- Standard case
        lh.holder_node_id IN (
            SELECT id FROM rimsky_nodes
             WHERE assigned_supervisor_id = $1 AND state = 'running'
        )
        OR
        -- Held-claim case: active inheritor running anywhere
        EXISTS (
            SELECT 1 FROM rimsky_claim_holders ch
              JOIN rimsky_nodes n ON n.id = ch.holder_node_id
             WHERE ch.lock_holder_id = lh.id
               AND ch.state = 'active'
               AND n.state = 'running'
        )
   );
```

The acquirer's supervisor remains the heartbeat-owner for the lock-holder row across the holding subgraph's lifetime. If the acquirer's supervisor crashes while inheritors are still running on other supervisors, the heartbeat stops; the row eventually expires; the orphan reaper releases the held claim via `on_give_up` route. This is acceptable (a supervisor crash mid-held-claim is the same blast radius as a supervisor crash mid-non-held-claim — the orphan reaper is the safety net).

#### 13.4.1 Acquirer-supervisor migration on crash

Out of scope for v1. A future enhancement could migrate held-claim ownership to a different supervisor on acquirer-supervisor crash. For v1, the orphan reaper handles supervisor failure mid-held-claim by releasing through `on_give_up`.

### 13.5 Orphan reap

Once-per-scheduler-tick sweep of `expires_at < now()` rows. For each:
- If region kind: invoke `Store.Abandon(ctx, region, address, "")` to undo any substrate-side state, where `region` and `address` come from the row's `region_data` and `address` columns. The empty `policyOverride` lets the substrate apply its `on_give_up_default`.
- Delete the row claimant-guarded on `holder_supervisor_id`. Cascade FK cleans up any `rimsky_claim_holders` rows.

The orphan reaper is a **safety net for supervisor failure (crash, network partition, hung process)**. In healthy operation it rarely finds anything; node terminals do the actual cleanup. Cutoff: `5 × heartbeat_interval`.

For held claims whose acquirer-supervisor crashed mid-subgraph: the orphan reaper releases the held claim via `Abandon` (effectively the `on_give_up` route at the substrate level). Inheritor nodes that were running on other supervisors will see their `rimsky_claim_holders` rows cascade-deleted; their dispatch attempts fail (verify-before-run catches the missing lock-holder row, per blessed invariant 5) and the workflow's policy chain handles the failure.

### 13.6 Release (revised under auto-terminal)

At a node's terminal, in a single transaction with the node's state transition:

1. **For each region claim where this node is the acquirer:**
   - If the claim is held (the holding subgraph computed at template deploy has > 1 members): **do not delete the lock-holder row.** Update the acquirer's `rimsky_claim_holders` row to `state = 'completed'` or `'failed'` per outcome. The auto-terminal mechanism (§14.4) will eventually trigger the resolution and delete the lock-holder row.
   - Otherwise (non-held claim): invoke the appropriate substrate verb based on `claim_resolutions` (or default `Commit` for success, `Abandon` for failure) — passing `region`, `address` (read from the lock-holder row's `address` column), and any `policyOverride` from `claim_resolutions`. Then delete the lock-holder row claimant-guarded on `holder_supervisor_id`.

2. **For each region claim where this node is an inheritor (not the acquirer):**
   - Update this node's `rimsky_claim_holders` row to `state = 'completed'` or `'failed'` per outcome. No substrate verb fires here — auto-terminal handles the eventual resolution.

3. **For each named-lock holder row owned by this node:** delete claimant-guarded.

4. **Auto-terminal check** (per §14.4): for each held claim this node was part of (acquirer or inheritor), check if all `rimsky_claim_holders` rows for the lock-holder are now in `'completed'` or `'failed'` state. If yes, fire the resolution per §14.4.1, deleting the lock-holder row in the same transaction. Cascade FK on `rimsky_claim_holders` cleans up the claim-holder rows.

All four steps commit atomically. The `address` argument to substrate verbs comes from the lock-holder row's `address` column (populated by `Open` per §13.3 and §12.10).

### 13.7 Sort-order invariant for multi-lock acquisition

Unchanged from prior spec. Multi-lock acquisition uses deterministic sorted order to prevent deadlock: `(lock_kind, lock_name | (store_name, region_data_canonical))`. Blessed invariant 3.

## 14. Held claims — inheritance and auto-terminal

### 14.1 Inheritance declaration (DSL)

A downstream node declares it inherits a held claim from an upstream source:

```yaml
nodes:
  - name: pick-item
    stores:
      - name: workspace
        selector: "@review-queue"
        intent: rw
        alias: queue-claim
    claim_resolutions:
      queue-claim:
        on_commit: delete
        on_give_up: release_to_head

  - name: process-with-shared-access
    deps: [pick-item]
    inherits:
      - claim: queue-claim       # references pick-item's alias
    attributes:
      schema:
        addr:
          source: "{{claim.queue-claim.address}}"
          type: string
```

### 14.2 Holding subgraph computation (template deploy)

At template deploy, for each held claim (any claim referenced by an `inherits:` block):
- Walk explicit `inherits:` declarations to enumerate **direct inheritors**.
- The **holding subgraph** = acquirer + direct inheritors. Direct only — not transitive through dep chains.
- Validate: every `inherits:` reference resolves to a claim alias acquired by a node the inheritor depends on (transitively).
- Validate: any `{{claim.<alias>.<...>}}` substitution in a node requires that node to either acquire `<alias>` OR appear in the alias's holding subgraph (i.e., have an `inherits:` declaration for it).

The holding-subgraph membership is materialized as metadata accessible to the supervisor at runtime.

### 14.3 Resolution-action declaration

`claim_resolutions:` declared on the **acquiring node** (not on terminals or inheritors). Per claim alias, declares `on_commit` and `on_give_up`. These are the substrate-side actions invoked by auto-terminal.

### 14.4 Auto-terminal mechanism (supervisor)

At each node's terminal, the supervisor:
1. Identifies every held claim this node is part of (acquirer OR inheritor).
2. For each such claim, checks whether all members of its holding subgraph have terminated (the corresponding `rimsky_claim_holders` rows are all in state `'completed'` or `'failed'`).
3. If a subgraph is complete: computes aggregate outcome:
   - **All members in `'completed'` state** → fire the `on_commit` resolution.
   - **Any member in `'failed'` state** → fire the `on_give_up` resolution.
4. The substrate-side action shares the same SQL transaction as the lock-holder row deletion and the `rimsky_claim_holders` rows finalization.

#### 14.4.1 Resolution action vocabulary

`on_commit` and `on_give_up` values declared in `claim_resolutions:` are one of:

- `"commit"` (default for `on_commit`) — fire `Store.Commit(region, address, "")` (no policy override; substrate's default behavior).
- `"abandon"` (default for `on_give_up`) — fire `Store.Abandon(region, address, "")`.
- `"delete"` (for non-held-claim use too) — fire `Store.Delete(region)`.
- Pick-policy actions: `"release_to_back"`, `"release_to_head"` — fire `Store.Commit(region, address, action)` (success path) or `Store.Abandon(region, address, action)` (failure path), passing the action as `policyOverride`. The substrate's pick policy interprets these per its configured vocabulary.

The supervisor's auto-terminal routing:

| Resolution value | Aggregate-outcome path | Verb call |
|---|---|---|
| `"commit"` (or unset → default) | success | `Store.Commit(region, address, "")` |
| `"abandon"` (or unset → default) | failure | `Store.Abandon(region, address, "")` |
| `"delete"` | either | `Store.Delete(region)` |
| `"release_to_back"` / `"release_to_head"` | success | `Store.Commit(region, address, value)` |
| `"release_to_back"` / `"release_to_head"` | failure | `Store.Abandon(region, address, value)` |

For non-held claims (claims with no holding subgraph), the same vocabulary applies at the acquirer's own terminal: the acquirer's `claim_resolutions:` (if declared) drives the verb. Default is `"commit"` for success, `"abandon"` for failure.

#### 14.4.2 Race safety

A held claim's lock-holder row is deleted exactly once — at auto-terminal — by exactly one of the holding subgraph's nodes (whichever node's terminal triggers the subgraph-complete condition). Race-safe via SQL row locking on the lock-holder row plus `state='active'` filter on the claim-holders rows. Concurrent terminations on the same subgraph see the row already locked / already deleted and no-op.

### 14.5 Pick-policy claims must be `intent: rw`

Pick-policy claims (selectors that match a substrate-recognized pick-policy form, per §7.2) are inherently mutating — the substrate flips item state to `in_progress` at acquisition. Declaring `intent: r` on a pick-policy claim is a category error.

Deploy-time validation: if the substrate identifies a selector as a pick-policy form (via store config), the claim's intent must be `rw`. The substrate exposes this check via its config's `pick_policies` block — Rimsky's deploy-time validator looks up whether the selector matches any configured pick-policy key for the store; if so, requires `intent: rw`.

Future spec sessions may revisit if shared-read-only pick-policy patterns emerge.

### 14.6 Failure propagation

If any node in the holding subgraph enters `failed`, the held claim's auto-terminal fires `on_give_up` when all subgraph members are terminated (whether by success, failure, or give-up). This closes the prior spec's Case 6 gap (failed branch leaves claim_holders rows active forever) — no node can prevent eventual resolution by failing; failure just routes through `on_give_up`.

### 14.7 Two propagation modes (DSL + substitution)

- **Value-pass.** Source extracts captured fields into its own attributes via `source: "{{claim.<alias>.payload.<f>}}"` (or `.region`, `.address` if held); downstream nodes consume captured values via `{{deps.<source>.<field>}}`. Lifetime-independent — works after the source's claim has closed.
- **Claim-pass.** Downstream node inherits the live claim via `inherits:`; substitutes via `{{claim.<alias>.address | payload.<f> | region}}`. Requires the claim to remain open; the inheriting node's existence holds it.

The "no hold + pass address" combination is structurally impossible: `{{claim.<alias>.address}}` requires the alias to be acquired or inherited, and inheritance extends the claim's lifetime.

## 15. Configuration

### 15.1 `stores.yml` (operator)

```yaml
stores:
  workspace:
    kind: filesystem
    root: /var/data
    write_semantics: staged_blocking
    pick_policies:                        # substrate-specific block
      "@review-queue":
        type: queue
        path: /var/data/inbox
        on_commit_default: delete
        on_give_up_default: release_to_head
        visibility_timeout_seconds: 300
      "@docs-ring":
        type: ring
        path: /var/data/docs
        on_commit_default: release_to_back
        on_give_up_default: release_to_back

  app_data:
    kind: postgres
    connection: postgres://...
    write_semantics: direct
    pick_policies:
      "@tasks-queue":
        type: queue
        items_table: tasks_items
        on_commit_default: delete
        on_give_up_default: release_to_head
        visibility_timeout_seconds: 300
```

The schema inside a `pick_policies` map entry is substrate-specific. Each store-author guide documents its own keys.

### 15.2 `named_locks.yml` (operator) — moved from template-level

```yaml
named_locks:
  model-calls:        { limit: 5 }    # counting semaphore
  db-connections:     { limit: 20 }   # counting semaphore
  pipeline-singleton: { limit: 1 }    # mutex (limit=1)
```

Templates reference named locks by name only — `locks: [{name: model-calls}]`; limit is operator-configured. There is no `mode` field; "mutex" is conventional shorthand for `limit: 1`. Deploy-time validation rejects template references to undeclared named locks.

### 15.3 Combined operator config

Stores and named locks live in one operator config bundle (loaded by control-api, supervisor, scheduler at startup). Same `RIMSKY_STORES_CONFIG` env-var path as today; the YAML is extended with the `named_locks:` top-level block.

### 15.4 Per-supervisor specialization

Unchanged from prior spec. Each supervisor pool's config lists which stores it can dispatch against; dispatch eligibility filters out nodes whose required stores aren't in the local pool.

### 15.5 Connection / credentials

Substrate connection details (postgres URLs, filesystem paths, S3 bucket names, etc.) often include credentials. Per the auth-blind philosophy (§17), Rimsky transports these as opaque substrate-specific config — no introspection, no validation, no logging of credential shape.

### 15.6 Hot-reload

Out for v1. Stores and named locks are loaded at process start; config changes require restart.

## 16. Substitution & attributes resolution

### 16.1 Substitution paths

| Path | Reads from | Lifetime |
|---|---|---|
| `{{deps.<node>.<field>}}` | Upstream node's persisted attributes | Independent of any claim |
| `{{claim.<alias>.address}}` | Live claim's address | Acquirer's context OR inheriting nodes |
| `{{claim.<alias>.payload.<field>}}` | Live claim's payload at named field path | Same validity rule |
| `{{claim.<alias>.region}}` | Live claim's region | Same validity rule |
| `{{params.<key>}}` | Instance-level config params | Independent of any claim |

### 16.2 Substitution timing

All substitution runs at dispatch time, before lock acquisition. By the time the supervisor's atomic acquisition transaction runs, all `{{...}}` directives in the node's selectors and attribute schema source-paths have been resolved to concrete strings.

### 16.3 Substitution-leaf extraction

Per blessed invariant 20 (§17), substitution-leaf extraction is the **single sanctioned operation** Rimsky performs on claim content. Substitution walks the named field path, returns leaf bytes, and passes through to the destination. Intermediate hops are bytes-only — no parsing, logging, validating, or transforming.

The substitution engine uses lazy unmarshal: `json.RawMessage` enters; `walkPath` decodes into a transient `map[string]any` only inside the leaf-extraction call; the transient is discarded after extraction. This permanently narrows the surface for accidental introspection (e.g., `slog.Any("address", a)` no longer pretty-prints structure).

### 16.4 Attributes resolution

Two phases (unchanged from prior spec):
1. **At dispatch**, after substitution: every required source-directive must resolve. Failure raises `template_resolution_failed`.
2. **At commit**, after the executor's writes are merged: the populated attributes object must validate against the schema. Failure raises `attributes_schema_failed`.

### 16.5 Where substitution applies

- **Selector** (per-claim): `selector:` field in `stores:` entries on a node.
- **Attribute schema source paths**: `source:` directive in attribute-property declarations.
- **Userdata is opaque** (blessed invariant 11): no substitution.

### 16.6 ResolveContext

```go
package attributes

type ResolveContext struct {
    Deps   map[string]json.RawMessage  // upstream node's persisted attributes
    Claim  map[string]ClaimResult      // claims acquired or inherited by this node, keyed by alias
    Params map[string]json.RawMessage
}
```

All three field-shapes use opaque-bytes; the substitution engine extracts at the leaf only.

## 17. Auth & inertness

### 17.1 Auth-blind

Rimsky has **no protocol surface for credentials.** No verbs, fields, or types in the protocol mention auth. Credentials and other sensitive content flow as ordinary claim content (via `payload`, `address`, or `region`) and via attribute substitution. Service-to-service auth between Rimsky processes (operator → control-api, supervisor ↔ executor, etc.) is operator-configured at the deployment layer (mTLS, IAM, service mesh).

### 17.2 Inertness (blessed invariant 20)

**Claim content (payload, address, region) is inert in Rimsky.** Rimsky reads claim content by named-field path **only at substitution-leaf extraction**; does not log, validate, transform, normalize, decrypt, hash, index, pattern-match, attach to traces, include in errors, or otherwise act on claim content. Substitution-time field extraction is the only sanctioned introspection site.

Annotated at:
- `core/store/types.go` on `ClaimResult`.
- `core/attributes/substitution.go::walkPath` (sanctioned introspection site).
- `CLAUDE.md` blessed-invariants list (entry 20).

### 17.3 Inertness audit

Sweep every code path handling claim content:
- `proto/v1/events.proto` emit path — confirm no event payload includes claim content.
- `core/attributes/substitution.go` error paths — confirm substitution failures don't log claim content.
- `rimsky_events.event_detail` JSON column — confirm no claim-content-derived values land there.
- Supervisor's handle-construction path packaging address into executor envelope.
- Lock-holder row insert path (region_data → JSONB).
- Diagnostic / debug log paths in `core/store/` implementations.
- Trace / span attribute paths.
- Stub executor + test harness pretty-prints.

Each path verified: no logging, validation, transformation, or introspection beyond substitution-leaf extraction.

### 17.4 Pre-sweep type hardening (lands first)

Switch the following from `any` to `json.RawMessage`:
- `ClaimResult.Address`
- `ClaimResult.Payload`
- `ClaimResult.Region`
- `ResolveContext.Deps` → `map[string]json.RawMessage`
- `ResolveContext.Params` → `map[string]json.RawMessage`

Update `core/attributes/substitution.go::walkPath` to lazy-unmarshal into a transient `map[string]any` only inside the leaf-extraction call; discard after extraction.

This permanently narrows the surface for accidental pretty-printing via `slog.Any` or `%+v`.

### 17.5 Inertness applies to claim content, not store-config bytes

Invariant 20 covers **claim content** — the runtime substrate-supplied bytes returned by `Open` (payload, address, region). Store-config bytes (the operator YAML loaded at process start, used by store implementations to reach substrates) are a **different category**: operator-managed config, parsed by the substrate's factory, used to construct `Store` instances. Connection strings, filesystem roots, S3 bucket names, etc. live here.

Store-config bytes are NOT under invariant 20. Operators may include credentials in store config (e.g., `connection: postgres://user:pass@host/db`), and Rimsky's auth-blind philosophy applies — Rimsky doesn't introspect or validate credential shape — but routine logging like "loaded store `X` (kind: postgres)" is not prohibited. Operator discretion governs what gets logged from store config.

The boundary: anything Rimsky receives via `Open` / `Commit` / `Abandon` / `Delete` / `Release` (claim-time or terminal-time substrate I/O) is claim content, inert under invariant 20. Anything Rimsky reads from `RIMSKY_STORES_CONFIG` at startup is store config, governed by operator discretion.

### 17.6 Encrypt-before-pass (operator practice)

Sensitive fields (any of payload / address / region) are encrypted at any producer-side boundary before they enter Rimsky's address space. Rimsky transports ciphertext as opaque bytes; the consuming executor decrypts at point of use.

- Asymmetric is the recommended default (executor holds private key; producer holds public).
- Field-level, not whole-content. Rimsky needs to see structure to substitute by name; sensitive values are individually encrypted.
- Rimsky-side awareness: zero. The protocol is unaware of encryption.

This is not a Rimsky feature — it is documented operator practice. Not in scope for this spec to ship a helper library.

### 17.7 Graph manifest scope enforcement

Deploy-time validation: a graph's claim acquisitions AND `inherits:` declarations must reference stores declared in the graph's manifest. Inheritance doesn't bypass scope enforcement — the source's store dependency covers the inheritor. New deploy-time check: every `inherits:` reference resolves to a real upstream claim alias.

## 18. Templated fields and template-deploy validation

### 18.1 What rimsky parses (DSL)

- `nodes[].name`
- `nodes[].executor`
- `nodes[].deps`
- `nodes[].stores[].name`, `.selector` (with substitution), `.intent`, `.alias`
- `nodes[].locks[].name` (named lock references)
- `nodes[].inherits[].claim` (claim alias references)
- `nodes[].claim_resolutions.<alias>.on_commit | .on_give_up` (declared on acquiring node)
- `nodes[].attributes.schema` (JSON Schema with optional `source:` directives)
- `nodes[].userdata` (opaque to rimsky)
- `frame_resolution: coalesce | serial_queue` (required)

### 18.2 What rimsky does not parse

- Selector text content (substrate parses).
- Userdata content (executor parses).
- Pick-policy config block content (substrate parses).
- Connection-detail fields in store config (substrate parses).
- Claim content (payload / address / region) — opaque bytes per invariant 20.

### 18.3 Removed template fields

- `held: true` flag on claim entries — dissolved; held is implicit from inheritance.
- `claim_resolutions:` on terminal nodes — moved to acquiring node.

### 18.4 Holding-subgraph computation algorithm

Pseudocode:

```
for each node N in template:
    for each entry in N.stores with intent in (r, rw):
        record (N, entry.alias) as an acquirer for that alias

for each node N in template:
    for each inherit-entry in N.inherits:
        find the acquirer A whose alias matches inherit-entry.claim
            and which N depends on (transitively)
        record (A, alias, N) as an inheritance edge
        validate: A exists and is reachable via deps; else reject template

for each acquirer A and alias:
    if any inheritance edges target this alias:
        compute holding-subgraph membership = {A} ∪ {N : (A, alias, N) inheritance edge exists}
        record holding-subgraph membership as deploy-time metadata
        validate: A.claim_resolutions[alias].on_commit and on_give_up are declared; else reject template
    else:
        no held claim — A's terminal releases the claim normally
```

### 18.5 Substitution-path validation

For every `{{claim.<alias>.<...>}}` substitution in a node's attribute schema or selector:
- The node must either acquire `<alias>` (in its own `stores:`) or appear in `<alias>`'s holding subgraph.
- Else reject template at deploy time.

### 18.6 Worked example

A queue worker pipeline with shared workspace access:

```yaml
nodes:
  - name: pick
    executor: claude-agent
    stores:
      - name: workspace
        selector: "@review-queue"
        intent: rw
        alias: queue
    claim_resolutions:
      queue:
        on_commit: delete
        on_give_up: release_to_head
    attributes:
      schema:
        task_id:
          source: "{{claim.queue.payload.task_id}}"
          type: string

  - name: process
    deps: [pick]
    inherits:
      - claim: queue
    attributes:
      schema:
        path:
          source: "{{claim.queue.address}}"
          type: string

  - name: report
    deps: [process]
    attributes:
      schema:
        task_id:
          source: "{{deps.pick.task_id}}"     # value-pass; no inheritance
          type: string
```

Holding subgraph for `queue` claim = `{pick, process}`. `report` is downstream of `process` but doesn't inherit; it's not in the subgraph. The claim is released when `process` terminates (since `pick` already terminated and `process` is the only inheritor); `report` then runs against captured values.

## 19. Executor protocol

Largely unchanged from prior spec. Two adjustments:

### 19.1 ExecuteRequest

The `stores` field of `ExecuteRequest` carries the per-claim handle each executor needs. Under the new model, the handle is the `Address` returned by `Open` — substrate-native, opaque to Rimsky, deserialized by the executor per its substrate-specific knowledge.

### 19.2 Terminal events

Unchanged in shape. The executor's `Complete{changed: bool, attributes_delta: ...}` carries the producer-declared `changed` flag (cascade trigger). `Commit` does NOT return `changed`; the executor's terminal event is the authority.

### 19.3 HTTP+JSON bridge / async handoff / incremental callback

Unchanged from prior spec.

## 20. Files modified, added, and deleted

### 20.1 Added (new files)

- `docs/glossary.md` — vocabulary reference (already created).
- `core/node/inheritance.go` (or equivalent in the existing template parser package) — `inherits:` parsing and holding-subgraph computation per §18.4.
- `core/supervisor/auto_terminal.go` — auto-terminal logic per §14.4 (subgraph-complete check, aggregate-outcome routing, single-transaction resolution + lock-holder deletion).

### 20.2 Modified (heavy changes)

- `core/store/interface.go` — interface rewrite per §11.5.
- `core/store/types.go` — types rewrite per §11.3 / §11.4.
- `core/store/lockholders.go` — schema column changes per §12.10.
- `core/store/registry.go` — minimal updates (factory now exposes `MaxWriteSemantics`).
- `core/store/filesystem/` — adapt to new verb set.
- `core/store/claimstorepg/` → renamed to `core/store/postgres/` — adapt; pick-policies block; `ResolveOnTerminal` simplified per auto-terminal (drops `actual_action` / `delete_won` / first-delete-wins).
- `core/store/stub/` — rewrite to new verb set.
- `core/attributes/substitution.go` — lazy-unmarshal `walkPath`; new substitution paths (`address`, `region`); `ResolveContext.Deps` shape change.
- `core/supervisor/runner.go` — atomic acquisition flow with new verb set; auto-terminal logic; `held: true` flag handling drops; verify-before-run unchanged.
- `core/queue/postgres/queue.go` — eligibility predicate parameterized by `write_semantics`.
- `core/scheduler/scheduler.go` — visibility-timeout sweep iterates each store's `pick_policies` block.
- `core/node/template.go` (or wherever template parsing lives) — accept `inherits:`, `alias:`, drop `held: true` flag handling; deploy-time validation per §18.
- `core/migrations/001-initial.sql` — rewritten in place (pre-v1 nuke).
- `proto/v1/node_executor.proto` — verify no auth fields; minor adjustments if any.
- `core/store/doc.go` — vocabulary section rewrite per glossary.

### 20.3 Deleted

- `core/store/types.go::LockSpec`, `RegionLockSpec`, `ClaimLockSpec`, `NamedLockSpec` (old discriminated union shape) — replaced by new `ClaimSpec` and `NamedLockSpec`.
- `core/store/types.go::LockHandle`, `NativeHandle` (sealed interface), `FilesystemDirectHandle`, `ClaimStoreHandle`, `ReleaseAction` enum.
- `core/store/interface.go::ClaimableStore`, `ResumableStore` sub-interfaces.
- Any code paths checking `held: true` flag on claim specs.
- Any code paths consulting dropped capability fields (`SupportsClaim`, `SupportsRegionLock`, `SupportsResume`, `SupportsRestore`, `SupportsAtomicMulti`, `SupportsDiscard`, `KeepVersionsMax`).
- `actual_action` and `delete_won` reconciliation logic in `core/store/claimstorepg/holders.go`.
- Any remaining references to "claim store" as a kind name (in docs, comments, code).

### 20.4 Doc updates

- `docs/protocol.md` — verb set, address shape, no-version-concept.
- `docs/architecture.md` — package layout, blessed invariants list (add 20), three-collections boundary.
- `docs/operator-guide.md` — operator config (stores + named locks bundle); auth-blind philosophy; encrypt-before-pass documented practice; per-region overrides not supported.
- `docs/store-author-guide.md` — verb contract; pick policies; selector grammar (substrate-defined); store-side serialization forbidden (invariant 9 restated); honest write_semantics reporting.
- `docs/executor-author-guide.md` — handle is opaque address; payload propagation via attributes; auth-blind from the executor side.
- `docs/node-graph-design.md` — two-noun primitives (claim / named lock); inheritance model; auto-terminal; two propagation modes.
- `docs/glossary.md` — already created; referenced from CLAUDE.md and other docs.
- `CHANGELOG.md` — append entry under "Unreleased."

### 20.5 Stale-reference sweep

Sweep `docs/`, `core/`, `proto/v1/` for stale `inline-jsonb` references and any remaining `Resource` (capital R) references that escaped the prior stores-redesign work. Single-digit hits expected.

## 21. Blessed invariants

Full list. Numbers 1–14 preserve their existing semantics; 9 is split into 9a/9b; 13 is revised; 15 and 20 are new.

1. **State machine rejects illegal transitions.** Unchanged.
2. **Dispatch claim brackets the running window.** Unchanged.
3. **Multi-lock acquisition uses deterministic sorted order.** Unchanged.
4. **Claimant-guarded release.** Unchanged.
5. **Verify-before-run.** Unchanged.
6. **Orphan-claim cutoff is `5 × heartbeat_interval`.** Unchanged.
7. **Advisory lock on scheduler tick.** Unchanged.
8. **Session advisory lock on migrations.** Unchanged.
9a. **Lock state lives only in postgres.** No store implementation persists lock state. Stores may persist data state (items-table flips, staging-area metadata), but the question "is anyone holding lock X" is answered exclusively by `rimsky_lock_holders`.
9b. **Store implementations do not internally serialize on lock-shaped predicates.** The §9-strategy-2 reader-lease serialization pattern (substrate tracks active read leases; writers block at the substrate boundary) is not a valid implementation choice for `staged_async`. Honest support requires snapshot delegation or native MVCC pass-through. A substrate that cannot honestly provide stable reads during writes declares `staged_blocking` (or `direct`).
10. **Lock acquisition is atomic with dispatch claim** (preserved for in-process). The §13.3 transaction either claims dispatch AND inserts all required `rimsky_lock_holders` rows AND completes all store `Open` mutations AND inserts any required `rimsky_claim_holders` rows, or none of these. The OOP cycle will revisit this invariant.
11. **Userdata is opaque to rimsky.** Unchanged.
12. **Attributes validate twice** (at dispatch post-substitution; at commit post-executor-merge). Unchanged.
13. **Held-claim resolution is auto-terminal, single, and aggregate-outcome-driven** (revised). At holding-subgraph completion (all `rimsky_claim_holders` rows in `'completed'` or `'failed'`), the supervisor fires exactly one resolution per held claim based on aggregate outcome: all-completed → `on_commit`; any-failed → `on_give_up`. No partial resolutions. No first-delete-wins or last-released-wins reconciliation. Replaces the prior spec's invariant 13.
14. **`RegionsConflict` and `UnmarshalRegion` are pure.** Unchanged.
15. **`Open` fires inside the acquisition transaction** (in-process; new). The supervisor's atomic acquisition transaction calls `Store.Open` with the open `pgx.Tx` shared via `store.TxFromContext`. Substrate-side state mutations and the lock-holder row's `address` update participate in the same transaction. (OOP cycle will revisit.)
16–19. (Reserved for future invariants.)
20. **Claim content (payload, address, region) is inert in Rimsky** (new). Rimsky reads claim content by named-field path only at substitution-leaf extraction; does not log, validate, transform, normalize, decrypt, hash, index, pattern-match, attach to traces, include in errors, or otherwise act on claim content. Substitution-time field extraction is the only sanctioned introspection site. Annotated at `core/store/types.go` (on `ClaimResult`) and `core/attributes/substitution.go::walkPath`. Distinct from store-config bytes (operator-managed; not under invariant 20 — see §17.5).

## 22. Test surface

### 22.1 Scenario tests (testcontainers-go)

New scenario tests:

- `verify_open_inside_acquisition_tx_test.go` — confirms `Store.Open` is called inside the §13.3 atomic transaction; substrate-side state and lock-holder row commit atomically; on substrate error, both roll back.
- `auto_terminal_aggregate_outcome_test.go` — held claim with N-node holding subgraph; mixed terminal outcomes (all-success vs. any-failure) drive correct aggregate action.
- `auto_terminal_failure_propagation_test.go` — non-terminal node in holding subgraph fails; auto-terminal still fires `on_give_up` when subgraph completes (closing the prior spec's Case 6 gap).
- `inheritance_validation_test.go` — deploy-time validation of `inherits:` against undeclared aliases, missing dep paths, etc.
- `address_inheritance_lifetime_test.go` — `{{claim.<alias>.address}}` in inheriting node resolves to live address; substrate-side state survives until subgraph completion.
- `value_pass_lifetime_test.go` — `{{deps.<source>.<field>}}` works after the source's claim has closed.
- `pick_policy_selector_test.go` — substrate-recognized selector forms (`@queue`, `@ring`) trigger configured pick policies; multiple policies per store.
- `frame_id_observability_only_test.go` — held-claim algorithm matches by `lock_holder_id`, not `frame_id`; recycled identifiers across frames don't collide because of the FK.
- `inertness_audit_test.go` — exercise every code path that handles claim content; assert no logs / event fields / span attributes contain claim-content bytes. (This is a scenario test, distinct from the smoke fixture in §22.2.)
- `single_writer_per_region_test.go` — under all three `write_semantics` values, two `rw` claims on overlapping regions never coexist.
- `staged_async_protocol_present_no_substrate_test.go` — protocol verbs `Open(read)` + `Release` exist in the interface; no v1 implementation exercises them; supervisor-side handling of `staged_async` is correct (would route through `Release`).

Existing scenario tests preserved (state machine, dispatch claim bracketing, multi-lock acquisition order, claimant-guarded release, verify-before-run race, orphan-claim cutoff, etc.).

### 22.2 Smoke fixture

Updated to exercise:
- A queue worker pipeline using `@review-queue` selector with multi-node holding subgraph (acquirer + 2 inheritors + 1 value-pass-only downstream).
- Multiple pick policies on the same postgres store.
- The 100 sequential force-fires through `POST /admin/scheduled-nodes/{id}/force-fire`.

### 22.3 Required final checks

- `go build ./...` — pass.
- `go test ./... -race -count=1` — pass.
- `make lint` — pass.
- `make proto-gen` (if proto changed) — regenerate.
- `cd executors/claude-agent && npm install && npm test && npm run build` — pass.
- `docker compose -f deploy/docker-compose.yml up -d` reaches `/health`.

## 23. Risks and accepted limitations

- **No `staged_async` substrate exercises the read-lease lifecycle in v1.** Protocol verbs `Open(read)` + `Release` are present and supervisor-side handling is correct, but no in-process store implementation registers read-side state. The first `staged_async` substrate (likely a future postgres MVCC pass-through) will exercise these paths in a follow-up cycle.
- **In-process `pgx.Tx` sharing** (invariant 10/15) won't translate to OOP. The OOP cycle will revisit the atomicity model.
- **`Abandon` is degenerate for `direct` mode regional `rw` claims.** Direct writes can't be undone. Store-author guide documents this as an honest substrate limitation; templates that require `discard_then_retry` semantics on direct stores must understand they get effectively `keep_then_retry`.
- **Inertness audit may surface latent leaks** in event-emit paths or diagnostic logs. Each leak has a local fix (redact / mark sensitive); the discovery requires a careful sweep across every consumer of claim content.
- **Held-claim resolution at subgraph completion** depends on accurate holding-subgraph computation at template deploy. A bug in the §18.4 walk would either miss a member (early release) or include extra members (delayed release). Scenario tests cover the common shapes; the algorithm itself is small and reviewable.
- **Per-region `write_semantics` overrides are not supported.** If a real workflow surfaces a need, the workaround is two distinct stores pointing at the same underlying storage, each with its own `write_semantics`. This may turn out to be cumbersome; revisit if it becomes a real friction point.

## 24. Pointer to companions

- **Glossary:** `docs/glossary.md` — authoritative naming reference.
- **Brainstorm decision log:** `docs/2026-04-26-stores-spec-scope.md` — sectional walkthrough with decision rationale and what-was-considered-but-rejected.
- **Discursive design:** `docs/2026-04-26-stores-redesign.md` — the discussion that produced this spec. §19 of that document is the authoritative resolution where it differs from in-line text.
- **Frame-resolution spec (preserved):** `docs/specs/2026-04-26-frame-resolution-design.md`.
- **Prior stores-redesign (substantially superseded):** `docs/specs/2026-04-25-stores-redesign-design.md`.
- **Control-layer design (sibling concern):** `docs/2026-04-26-control-layer.md` — provisioning / multi-tenant; out of scope for this spec.
