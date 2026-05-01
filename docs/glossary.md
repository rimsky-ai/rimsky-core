# Rimsky Glossary

Vocabulary for the stores-redesign / lock-primitive refinement. Compiled from the brainstorm working document at `docs/history/2026-04-26-stores-spec-scope.md` and consolidated here as the spec's authoritative naming reference. Stores-redesign-v3 (`docs/specs/2026-04-27-stores-redesign-v3-design.md`) introduces additional terms for the out-of-process world; they're listed in §V3 below.

When this glossary contradicts older docs, the glossary wins. Spec text and code comments should align here.

## V3 additions

- **Store-service** — the standalone binary that implements the
  `proto/v1/store_service.proto` gRPC server for a given underlying
  storage technology. Standard impls live under `stores/<kind>/`.
  Rimsky talks to a store-service exclusively via the wire protocol;
  it does not in-process-link any store implementation code.
- **`claim_id`** — the rimsky-generated UUID (textual form on the
  wire; `core/store.ClaimID` in Go) that identifies a single claim
  across every protocol verb in its lifecycle. Generated client-side
  immediately before `Open`; persisted in `rimsky_lock_holders.id`;
  passed unchanged on `Commit` / `Abandon` / `Release`. Per spec
  §4.2.

---

## Entity & primitives

| Term | Definition |
|---|---|
| **Store** | A named entity in operator config, with a kind, a `write_semantics`, and optional `pick_policies`. Rimsky's unit of interaction with persistent state. Operators configure stores; templates reference them by name. |
| **Store implementation** | The code that fulfills the `Store` interface for a given kind. In-process Go impl in v1; possibly RPC-based post-v1. No special noun beyond "the store" or "the store's implementation." |
| **Underlying storage** | Informal — the physical thing a store wraps (filesystem, postgres, S3, git, etc.). Not a Rimsky-side concept. Used only when context genuinely requires reference to the physical layer. |
| **Claim** | A row in `rimsky_lock_holders` with `(store_name, region_data, intent)`. The store-anchored primitive. Halts node dispatch when conflicting claims are held on overlapping regions. |
| **Named lock** | A row in `rimsky_lock_holders` with `(lock_name, limit)`. The non-store primitive. Halts node dispatch when the count of holders equals the limit. |
| **Lock-holder row** | A row in `rimsky_lock_holders`. Common shape across both primitives; CHECK constraint enforces exactly-one-of (`lock_name`) or (`store_name` + `region_data`) populated. |
| **Lock state** | The set of all current lock-holder rows. Lives only in postgres (blessed invariant 9). No store implementation persists lock state. |

---

## Verbs (4 runtime verbs + 1 startup handshake)

| Verb | Signature | Purpose |
|---|---|---|
| **Open** | `Open(region, intent) → OpenOutcome` | Produce a store-supplied address for the executor and register whatever store-side state the `(intent × write_semantics)` combination requires. Returns `OpenOutcome{Available: true, Result: ...}` on a successful acquisition; returns `OpenOutcome{Available: false}` (`Unavailable{}` on the wire) when the store has no claim to give right now (e.g. an empty items-table queue). |
| **Commit** | `Commit(region, address) → ()` | Signals that the consumer of the claim succeeded. The store decides what to do with its own state per its own configuration. |
| **Abandon** | `Abandon(region, address) → ()` | Signals that the consumer of the claim failed. The store decides what to do with its own state per its own configuration. |
| **Release** | `Release(region, address) → ()` | Tear down store-side read state (snapshot, MVCC transaction) for a read claim. Fires only when the store implementation registered such state. |

`intent ∈ {r, rw}`. Store disposition (commit-vs-release-vs-delete on the store's own state) is governed by per-store config. Per the 2026-04-30 stores cleanup, no store-internal vocabulary fields cross the rimsky↔store boundary; rimsky carries only the success/failure binary.

---

## Claim attributes

| Term | Definition |
|---|---|
| **Region** | Both the conceptual `(store, selector)` pair and the concrete opaque bytes that identify it on the lock-holder row. The conceptual sense names the slice of a store's namespace under claim; the concrete sense is the resolved selector text or pick-policy-picked identifier (stored in the `region_data` column for historical reasons). Substitutable via `{{claim.<alias>.region}}`. |
| **Selector** | The opaque text the graph author supplies (post-substitution). The store parses; Rimsky doesn't classify or validate. May contain `{{...}}` substitution directives resolved at dispatch. |
| **Address** | Store-supplied pointer the executor uses to access claimed state (path, table reference, snapshot handle, etc.). Returned by `Open`. Substitutable via `{{claim.<alias>.address}}` in inheriting nodes. |
| **Payload** | Store-supplied data captured at acquisition (e.g., a picked queue item's user data). Substitutable via `{{claim.<alias>.payload.<field>}}`. |
| **Intent** | `r` (read) or `rw` (read-write). The graph author's declaration of what the executor will do with the claim. |
| **Alias** | Per-claim name within a node. Used in substitution paths and `inherits:` references. Defaults to the store name; can be set explicitly when a node has multiple claims on the same store. |

---

## Lifecycle & propagation

| Term | Definition |
|---|---|
| **Acquirer** | The node that calls `Open` for a given claim. Exactly one acquirer per claim. |
| **Inheritor** | A downstream node that declares `inherits:` on the acquirer's claim alias. Inheritance extends the claim's lifetime to cover the inheritor's run. |
| **Inheritance** | The DSL mechanism by which a downstream node declares it will use the live claim from an upstream acquirer. Direct only — does not propagate transitively through dep chains. Each node that needs the live claim declares it explicitly. |
| **Holding subgraph** | The set of nodes a held claim's lifetime spans: acquirer + directly-declared inheritors. Computed at template deploy from explicit `inherits:` declarations. |
| **Auto-terminal** | The mechanism by which a held claim's resolution fires automatically when the holding subgraph completes. Aggregate outcome (all-success → `Commit`; any-failure → `Abandon`) determines the store verb. No graph-author terminal designation required. |
| **Value-pass** | Propagation mode: source extracts captured fields into its own attributes; downstream nodes consume via `{{deps.<source>.<field>}}`. Lifetime-independent — works after the source's claim has closed. |
| **Claim-pass** | Propagation mode: downstream node inherits the live claim and uses `{{claim.<alias>.address \| payload.<f> \| region}}`. Requires the claim to remain open; the inheriting node's existence holds it. |

---

## Store-side mechanisms

| Term | Definition |
|---|---|
| **Staging area** | Informal — a store-internal private workspace where in-progress writes accumulate before atomic publication. Visible to Rimsky only as the address `Open` returned. |
| **Atomic swap** | Informal — the store's native mechanism that publishes staging into live data atomically. Examples: filesystem rename, SQL `ALTER TABLE` swap, S3 manifest pointer flip, git merge. The moment of `Commit`. |
| **`write_semantics`** | Per-store config field with values `direct \| staged_blocking \| staged_async`. Determines (a) whether reads can dispatch concurrently with writes on the same region, and (b) whether the supervisor calls the staging-related verbs. Operator-configured; bounded above by the store kind's max capability. |

---

## Rules & invariants

| Term | Definition |
|---|---|
| **Inertness (blessed invariant 20)** | Rimsky reads claim content (payload, address, region) by named-field path **only at substitution-leaf extraction**; does not log, validate, transform, normalize, decrypt, hash, index, pattern-match, attach to traces, include in errors, or otherwise act on claim content. Substitution-leaf extraction is the only sanctioned introspection site. Operationally: treat claim content as transit-only bytes; the only sanctioned read is substitution-leaf extraction. |
| **Substitution-leaf extraction** | The single sanctioned operation Rimsky performs on claim content under invariant 20: walk the named field path, return leaf bytes, pass through to the next destination (downstream attribute, executor envelope). Intermediate hops are bytes-only. |
| **Encrypt-before-pass** | Operator-side practice: sensitive fields (any of payload / address / region) are encrypted before they enter Rimsky's address space; Rimsky transports ciphertext as opaque bytes; the consuming executor decrypts at point of use. Encryption can happen at any producer-side boundary (underlying storage, store implementation, control-layer admin verbs, or operator-managed pipeline) — Rimsky doesn't care which. Asymmetric is the recommended default (executor holds private key; producer holds public). Field-level, not whole-content. Rimsky-side awareness: zero. |
| **Auth-blind** | Rimsky has no protocol surface for credentials. No verbs, fields, or types in the protocol mention auth. Credentials and other auth content flow as ordinary claim content (and via attribute substitution); Rimsky transports bytes without introspection. Service-to-service auth between Rimsky processes is operator-configured at the deployment layer (mTLS, IAM, service mesh). |
| **Single-writer-per-region** | Structural invariant: at most one `rw` claim on overlapping regions at any time, regardless of mode. Enforced by the dispatch claim machinery as part of the conflict predicate. Folds into the `w×w ❌` cells of the coexistence matrix. |

---

## Frame & scheduling

| Term | Definition |
|---|---|
| **Frame** | The unit of cascade resolution per `docs/specs/2026-04-26-frame-resolution-design.md`. Every `rimsky_dispatch` row carries a non-null `frame_id`. At most one frame is `running` per instance under serial_queue / coalesce; multiple may run concurrently under post-v1 parallel_buffered. |
| **Holding-subgraph completion** | All nodes in a holding subgraph have terminated (committed or failed). Trigger for auto-terminal. |
| **`frame_id` (on lock-holder rows)** | Observability-only. Not consulted at acquisition, eligibility, orphan-reap, or held-claim resolution. Used for "which frame held this?" debugging queries. |

---

## Substitution paths

| Path | Reads from | Lifetime |
|---|---|---|
| **`{{deps.<node>.<field>}}`** | Upstream node's persisted attributes (captured values). | Independent of any claim's lifetime. |
| **`{{claim.<alias>.address}}`** | The live claim's address. | Valid only in the acquirer's own node OR in nodes inheriting the alias. Implies inheritance — using this path in a non-acquirer node requires an `inherits:` declaration; deploy-time validation enforces. |
| **`{{claim.<alias>.payload.<field>}}`** | The live claim's payload at a named field path. | Same validity rule as `address`. |
| **`{{claim.<alias>.region}}`** | The live claim's region (resolved selector or picked identifier). | Same validity rule as `address`. |
| **`{{params.<key>}}`** | Instance-level config params (passed when the instance was created). | Independent of any claim. |

---

## Coexistence matrix (claim vs claim, on overlapping regions)

Mode is derived per claim from `(intent, store.write_semantics)` at conflict-check time — not stored on the lock-holder row.

| | sync-r | sync-w | async-r | async-w |
|---|---|---|---|---|
| **sync-r** | ✅ | ❌ | (n/a) | (n/a) |
| **sync-w** | ❌ | ❌ | (n/a) | (n/a) |
| **async-r** | (n/a) | (n/a) | ✅ | ✅ |
| **async-w** | (n/a) | (n/a) | ✅ | ❌ |

- **Sync block** (store with `direct` or `staged_blocking`): r×r ✅; everything else ❌.
- **Async block** (store with `staged_async`): r×r ✅, r×w ✅, w×w ❌.
- Cross-quadrant cells are n/a — two claims on the same store share its `write_semantics`.
- The `w×w ❌` cells in both blocks are the structural single-writer-per-region rule.

Named locks have no mode dimension; their coexistence rule is purely numeric (count vs. limit).

---

## Things deliberately NOT in the vocabulary

- **"Bridge"** — replaced. Use "store" or "store implementation."
- **"Substrate"** as a synonym for "store" — never. A substrate is the underlying physical storage technology (a filesystem on disk, a postgres database, an S3 bucket); a store is the rimsky-service that wraps it and speaks the storage-claim wire protocol. Use "store" or "store-service" when referring to the rimsky-side concept; use "substrate" only when explicitly referring to the underlying physical layer (and even then, "underlying storage" / "underlying X" is often clearer).
- **"Held: true" flag** — dissolved; held is implicit from inheritance.
- **"Region claim" / "Item claim" / "Budget claim"** as sub-types — replaced by the cleaner two-noun split (claim / named lock); the "item" sub-form is just a claim whose store runs an items-table queue convention behind the selector.
- **"Versioned mode" / "Restore"** — eliminated entirely; orchestrator has no version concept.
- **"`SupportsResume`" / "`ResumableStore`" / "`SupportsRegionLock`" / "`SupportsEmptySelector`" / etc.** — capability struct collapsed to one field (`write_semantics`); resume is a universal behaviour pattern, not a capability.

---

## Store-internal vocabulary (not part of rimsky's protocol surface)

The terms below are used by some store-service implementations
(e.g. the postgres reference store-service) but do not appear in the
rimsky↔store wire protocol or the rimsky-side template grammar. They
appear only in store-service-specific documentation and config. Per
the 2026-04-30 stores cleanup
(`docs/specs/2026-04-30-stores-protocol-cleanup-design.md`).

- **Pick policy** — An items-table queue convention some store-services
  implement. The store recognizes special-form selectors
  (recommended convention: `@policy-name`) and picks an item per its
  configured logic (FIFO queue, ring buffer, LIFO scratchpad, etc.).
  The postgres reference store-service exposes per-policy
  `on_commit_default` / `on_give_up_default` config in its own
  `config.yml`. See `docs/store-author-guide.md` and
  `deploy/store-postgres.yml`.

- **`pick_policies`** — A store-service's own config block listing
  named pick policies it implements. Each entry is keyed by the
  recognized selector form (e.g. `@review-queue`) and carries
  store-specific configuration (item path, ordering, default
  actions, visibility timeout, etc.). Store-internal — not part of
  rimsky's `stores.yml`.

- **`release_to_back` / `release_to_head`** — Per-policy disposition
  actions in pick-policy store-services' configs (e.g. the postgres
  reference store-service). Store-internal; not visible to rimsky.

- **Items-table `delete` (action)** — A per-policy disposition action
  in pick-policy store-services that removes the row from the items
  table. Distinct from any rimsky-level concept. (The legacy
  `Store.Delete` wire verb that existed pre-2026-04-30 was removed
  by the cleanup; the action lives entirely inside the store's
  Commit / Abandon handlers.)
