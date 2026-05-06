# Store Ecosystem & Lock Primitive Refinement

## Status

- Design notes, 2026-04-25 (originally); revised 2026-04-26 post-implementation (see line below).
- Builds on `docs/2026-04-25-stores-redesign.md` (now landed as `docs/specs/2026-04-25-stores-redesign-design.md`), with the further evolution of frame-resolution at `docs/specs/2026-04-26-frame-resolution-design.md`. This doc captures a follow-on conversation about (a) extending the store concept to enable a third-party ecosystem, (b) refining the lock primitive surface presented to graph authors, and — added in §19 after the post-implementation walkthrough — (c) substantive revisions of the verb set, capability struct, and version handling that supersede the in-line text in §§4–15.
- Originally authored as conversation notes for a future session. The 2026-04-26 post-implementation walkthrough revisited the doc against the as-shipped code and added §19 as the authoritative resolution. **This doc is now ready to be converted into a formal spec.** A fresh session reading this doc should treat §19 as authoritative wherever it differs from in-line text.
- Updated 2026-04-26: §14 added covering the auth landscape (**Rimsky stays auth-blind**; **inert with respect to claim payload content**; encrypt-before-pass for sensitive content per §14.5). §15 added covering **multi-tenant stores** — graphs self-service logical sub-namespaces within ops-provisioned substrates via an optional bridge admin verb set, keeping the IaC boundary clean.
- **Updated 2026-04-26 (post-implementation): §19 added.** After the stores-redesign and frame-resolution implementations landed, a walk-through conversation revised specific claims in §§4, 5, 8, 12, 13, 14, 15. **§19 is the authoritative resolution where it differs from in-line text.** Pending items still to discuss: async-read lifecycle (§19.9), `core/queue/DispatchQueue` pgx leak, and the obsolete `inline-jsonb` reference.
- Next session topic: the **claim payload handling audit** — engineering hygiene, not a security blocker. Encrypt-before-pass (§14.5) is the primary defense; the audit hardens the residual non-secret-but-private content, ciphertext accumulation, and third-party-bridge cases. Sweep every code path that touches `ClaimResult.Payload` or attributes populated from claim payloads to confirm no logging, validation, or transformation leaks content beyond what mechanical substitution requires. **Sequencing per §19.6:** pre-sweep type-shape hardening (§14.8 #6) lands before the audit, so the audit surface is structurally narrowed.

## Context

The prior redesign (`docs/2026-04-25-stores-redesign.md`) establishes Stores as a first-class concept with:

- Postgres-backed lock state (`rimsky_lock_holders`) — the universal exclusion mechanism.
- Three modes (direct, sidecar, versioned). [As-shipped: only `direct` was implemented; sidecar and versioned modes were deferred post-v1, and per §19.1 the `versioned` mode is now eliminated entirely.]
- Claim stores (queues, ring buffers) as a configuration of one `claim_store` kind. [Per §19.2, this collapses into "stores with empty-selector support"; "claim store" as a distinct kind name is dropped.]
- Attributes replacing inputs/outputs/claim metadata; rimsky owns all `{{...}}` substitution.
- A six-commit implementation sequence; v0 hard break at commit 4. [The six commits landed; the implementation is `docs/specs/2026-04-25-stores-redesign-design.md`.]

What that redesign does **not** address — and what this conversation explored:

1. **Ecosystem distribution.** The prior design assumes stores are compiled-in Go interface implementations. This forecloses a third-party ecosystem because every consumer would need to rebuild rimsky to add a store type.
2. **Locking primitive surface.** The prior design exposes lock kinds (`region`, `claim`, `named` with mode `mutex|counting`) and treats lock as a separate primitive from claim. This conversation found a cleaner conceptual frame where claim is the only primitive, with mode determining coexistence rules — and the graph-author surface collapses to two types (`r`, `rw`).
3. **Versions, semantically.** The prior design references versions in passing. This conversation worked through what the orchestrator actually needs from versions (very little) versus what the substrate may offer (a lot). **Resolved per §19.1: nothing. The orchestrator has no version concept at all; "very little" was further reduced to zero in the post-implementation walkthrough.**
4. **Async-write as a footgun.** This conversation specifically arrived at: async-write should not be a graph-author primitive; it's an orchestrator-internal optimization that the orchestrator picks based on substrate capability.

The recommendations here are **compatible** with the prior redesign's machinery. They're a vocabulary and surface refinement plus an additive extension (the bridge protocol) — not a contradiction of the underlying architecture. **Revised per §18 "Watch out for" and §19.3:** post-implementation walkthrough revealed this framing was overstated. The consolidated verb set (§19.3) and capability struct (§19.4) require a third rewrite of `core/store/`, not just a refinement. Pre-v1; rewrites are expected.

**Scope of "store" in this doc.** The discussion is about **workload stores** — the operator-configured backends nodes claim regions on (filesystem, postgres-as-data-store, redis-as-claim-store, git, S3, etc.). Rimsky's own **platform state backend** — the database holding `rimsky_dispatch`, `rimsky_lock_holders`, `rimsky_node_attributes`, and the rest of the orchestrator's bookkeeping — is a separate concept and is not the subject of this doc.

The platform state backend is **pluggable by design**: `core/storage/StorageBackend` and `core/queue/DispatchQueue` are interfaces, with the postgres reference implementation under `core/storage/postgres/` and `core/queue/postgres/`. The intent is that future reference implementations (sqlite, redis, etc.) ship alongside postgres so operators can pick the backend that matches their deployment. The blessed invariants are about behavior (atomicity, claimant-guarded release, advisory-lock-equivalent coordination) — properties any backend must preserve, not postgres-specific implementations.

Two notes for whoever lands the second backend:

1. **`core/storage/StorageBackend` is cleanly abstracted** — opaque `Tx` / `TxMarker` pattern; all sub-stores deal in plain Go types; no postgres-specific types leak.
2. **`core/queue/DispatchQueue` has a known abstraction leak**: `SelectCandidates` and `ClaimDispatchRow` take `pgx.Tx` directly, and the package imports `github.com/jackc/pgx/v5`. A non-postgres backend can't implement the interface as written. Cleanup target: thread the same opaque `Tx` pattern through the queue, and refactor the supervisor's `runner.go` to pass that opaque type through the §13.3 atomic-acquisition flow instead of a concrete pgx type. This is a prerequisite to shipping a second backend.

The package-manager / registry distribution mechanism is for **workload artifacts** only (graphs, executors, workload stores). Platform binaries — including alternate state-backend implementations — ship via operator-managed channels (Docker, Helm, k8s); they are not third-party-shareable user artifacts. That ruling stands regardless of how many state backends Rimsky ships.

## 1. The ecosystem framing

Three artifact types Rimsky users might want to share or distribute:

1. **Graphs (templates)** — pure data, references executors and stores by name. Distribution is trivial; portability requires dependency declarations.
2. **Executors** — already process-isolated peers speaking gRPC+HTTP. Distribute as OCI images on existing registries; signing, regional mirrors, polyglot story all inherited.
3. **Stores** — the hardest. In the prior redesign, they're in-process Go interface implementations. An ecosystem requires either Go plugins (brittle), build-your-own-binary (high friction), or out-of-process bridges (architectural shift).

This doc focuses on stores; the executor and graph distribution stories are clean enough to defer.

The strategic call: **graph-centric package format**. A Rimsky package is fundamentally a graph (or graph fragment) with a dependency manifest. Executors and stores are distributed through their own channels (OCI for executors; bridge images or compiled-in for stores). The Rimsky package manager is a graph registry with a resolver.

## 2. Inlined vs standalone stores

Two store types, distinguished by where the implementation lives:

### 2.1 Inlined stores

- Ship with rimsky (filesystem, postgres, claim-store/postgres) or compiled in by power users.
- Run in-process inside `rimsky-supervisor` and `rimsky-control-api`.
- Today's model. The prior redesign's `core/store/` package is the implementation hook.

### 2.2 Standalone stores

- Run in a separate process. Rimsky talks to them over RPC (the bridge protocol).
- Same trust model as executors: process-isolated; cannot escalate into the orchestrator's address space.
- The bridge translates a substrate-agnostic RPC surface into substrate-native operations (filesystem, Redis, Mongo, git, S3, etc.).
- Distribution: an executable + a manifest + (optionally) a Docker image. Discoverable through the same registry as executors.

### 2.3 Why both, not just standalone

- Inlined is faster — no RPC overhead. Worth keeping for the blessed defaults that ship with rimsky.
- Standalone unlocks the ecosystem (third-party authors don't need to fork rimsky).
- The two paths can converge: blessed defaults could be implemented as in-process bridges (a no-op trampoline for postgres-backed stores running on the same connection). One protocol everywhere. The CHANGELOG already flagged the `"inline-jsonb"` hardcoded lookup as a post-v1 concern; a uniform bridge protocol resolves it cleanly.
- Open question whether to converge in v1 or keep two paths. Cheaper to ship two, but the duplication will eventually self-justify a unification.

## 3. Why stores earn their place in Rimsky's vocabulary

The orchestrator's reason to know about a store at all is **locking + sequencing + version-pinning**.

> **Revised per §19.1: drop "version-pinning."** The orchestrator has no version concept; GC is substrate's responsibility entirely. The reduced framing: the orchestrator's reason to know about a store is **locking + sequencing**.

Strip those out and stores become "external services that executors talk to," which is fine but isn't a story Rimsky needs to be in the middle of. The reason a graph names a store and the orchestrator tracks it is because Rimsky provides:

- Exclusion across nodes (claim coexistence rules).
- Ordering (cascade triggering, dispatch dependencies).
- ~~Version pinning (GC gating against active readers).~~ — Removed per §19.1.
- Implicit data-structure semantics (queues, ring buffers — fall out of the locking + sequencing primitives, with empty-selector unification per §19.2).

Without those, the bridge protocol doesn't earn its keep. With them, the bridge protocol is the membrane between Rimsky's coordination machinery and the substrate's physical storage.

## 4. Bridge protocol surface

> **Superseded by §19.3.** The verb signatures below carry a `version` parameter that was stripped in the 2026-04-26 conversation (no version concept anywhere; see §19.1). The consolidated verb set lives in §19.3.

Five control verbs handle the universal store contract:

```
ResolveRegion(region, version) → Address
Allocate(region, version) → StagingAddress
Commit(region, version, staging) → ()
Abandon(region, version, staging) → ()
Delete(region, version) → ()
```

Plus an optional read-lease pair for substrates that need it:

```
AcquireRead(region, version) → AddressLease
ReleaseRead(lease)
```

The optional pair is gated by the store's manifest (`read_during_write: async` on a non-MVCC substrate that needs lease-tracking). MVCC substrates don't need it — `ResolveRegion` returns a version-pinned address and the substrate's own version-tracking handles the rest.

### 4.1 Data path is direct executor↔substrate

The bridge is on the **control** path (lock acquisition, commit, cleanup). It is **not** on the data path. The executor receives an address from the bridge and connects directly to the substrate to read/write. This:

- Keeps bridges stateless and small.
- Prevents bridges from becoming throughput bottlenecks.
- Pushes the auth question to executor↔substrate (next session's topic).

### 4.2 Substrate-side fences are brief and applied at commit

The substrate's job is to make `Commit` atomic. Not to hold exclusion across the executor's run — that's Rimsky's job. The dominant pattern is staging + atomic swap:

- Filesystem: executor writes to temp file; bridge does atomic rename in `Commit`.
- SQL: executor writes to staging table; bridge does atomic table swap in `Commit`.
- S3: executor uploads to staging key; bridge flips a manifest pointer in `Commit`.
- Redis: executor writes to temp key; bridge does `RENAME` in `Commit`.
- Git: executor commits to a feature branch; bridge merges to main in `Commit`. Merge can fail (substrate-side conflict surfaces as commit error).

In all of these, the substrate-side fence is **brief and applied at commit**. Run-spanning exclusion is Rimsky's claim.

Bridges that hold long-lived substrate locks (open SQL transactions across an executor's whole run) are doing the orchestrator's job; let the orchestrator do it.

### 4.3 Substrate-side commit failures are normal errors

Git merge conflicts, S3 conditional-put failures, Postgres serialization errors, unique-key violations: all surface as `Commit` returning an error. The orchestrator's existing `retry / give_up / invalidate(targets)` vocabulary handles them like any executor error. No new concept required.

## 5. Claim and lock unification

This is the conceptual cleanup that simplifies everything else.

### 5.1 The vocabulary

> **Partially superseded by §19.1, §19.2.** Versions removed entirely (no claim-level version pin, no GC gating, no version-advance signal). The selector axis gains an explicit "empty" value (substrate auto-pick); see §19.2.

- **Region** = `(store, selector)`. One concept. Selectors range from fully static (file path, table name, explicit key) to fully dynamic (predicate over rows, glob over files) **to empty** (substrate runs its built-in pick policy — see §19.2). There is **no separate type** of region — selector dynamism is an axis, not a partition.
- **Claim** = "node X is operating on region R in mode M." Always present. (Per §19.1, claims do NOT pin a version against GC — Rimsky has no version concept; GC is substrate's responsibility entirely.)
- **Mode** = `none | sync | async-read | async-write`. Determines how this claim coexists with other claims on the same region. Internal vocabulary; not exposed to graph authors.
- **Commit** = action that resolves a write-mode claim. (Per §19.1, "advances the version" is removed — substrate-side state changes are observed via the substrate's own mechanisms, not via Rimsky-tracked versions.)
- **Resolve** = bridge verb that produces a substrate-native address from a region.

There is **no separate "lock" primitive**. Lock was just describing what a claim's mode does to other claims. The word is useful as exposition but not as a concept.

### 5.2 Mode coexistence

> **Notes column updated per §19.1: no version pinning, no per-version stable view.** The substrate may still provide a stable read view via its own MVCC / snapshot mechanism (§9 implementation strategies); the orchestrator just doesn't track versions. Mode coexistence rules below are unchanged.

| Mode | Coexists with | Notes |
|---|---|---|
| `none` | All others | No exclusion. Pure-MVCC reads use this. (The "version pin" framing in earlier drafts is removed per §19.1.) |
| `sync` | Nothing | Mutex against all other claims on the region. |
| `async-read` | Other async-read, async-write | Blocks against sync. Stable read view via the substrate's own snapshot/MVCC mechanism (orchestrator doesn't track versions). |
| `async-write` | async-read | Blocks against sync. Single-writer-per-region enforced by dispatch claim machinery, not by mode. |

### 5.3 Why this simplification holds

The prior redesign treats lock as a separate primitive with kinds (`region`, `named`, `claim`) and modes (`mutex`, `counting`). That's because exclusion, claim acquisition, and concurrency limits were three mechanisms that got unified into one **lock**.

The further simplification: **claim itself is the unifier**. Every store interaction is a claim. Some claims need exclusion (mode `sync`). Some need consistent-view semantics (mode `async-read`). Some need neither (mode `none`, on MVCC substrates). The mode is what was previously the lock.

Counting semaphores and named mutexes (the "named lock" cases in the prior redesign) are **not store-level** — they're cross-store concurrency budgets (e.g., model-call rate limits). Those remain as a separate concept (the prior redesign's `locks: [{name: ..., mode: counting, limit: N}]`). They live in `rimsky_lock_holders` alongside region claims but conceptually they're a different surface — they're not about region exclusion; they're about resource budgeting.

So the cleaned vocabulary has:

- **Claim** — store-region-version exclusion or consistency.
- **Concurrency budget** — cross-cutting named limit, independent of any store. (Keeps the prior redesign's named-lock mechanism; just renamed for clarity.)

These are orthogonal. A node can hold claims on three regions and consume one slot of a counting budget for "model calls."

## 6. Graph-author surface: r and rw

> **Still authoritative on the r/rw surface itself; the field name `read_during_write` referenced below was renamed to `write_semantics` per §19.4.** The collision matrix's "depends on store" row maps to: `staged_async` → no block; `staged_blocking` and `direct` → blocks.

The graph DSL exposes **two** claim types:

- `r` — read claim
- `rw` — read-write claim

That's it. The full mode set (`none | sync | async-read | async-write`) lives under the hood; the orchestrator picks between them based on the store's capability and the claim's intent.

### 6.1 Collision matrix

| Claim A | Claim B | Blocks subgraph (overlapping regions)? |
|---|---|---|
| r | r | Never |
| r | rw | Depends on store: `read_during_write: async` → no block; `block` → blocks |
| rw | rw | Always |

Three rows. That is the entire collision surface.

### 6.2 Why these two and only these

- `r-vs-r` is always parallel — multiple readers never need to coordinate.
- `rw-vs-rw` is always sequential — single-writer-per-region is enforced by the dispatch claim machinery regardless of any other choice.
- The only meaningful choice is **`r-vs-rw`**: can a read run while a write is in progress? That depends entirely on substrate capability.

The substrate is the entity that knows whether it can provide a stable read view during a write. The graph author doesn't have that information. So the choice belongs on the store, not on the claim.

### 6.3 Why not expose async-write as a graph-author primitive

It's a footgun:

- The dangerous case ("two concurrent writers stomping each other") is already prevented by the dispatch claim machinery — only one supervisor produces a given version.
- The remaining sense of "async-write" is "writer that doesn't block readers" — that's an orchestrator-internal optimization, not a graph-author choice.
- Exposing it tempts misuse ("go faster") on substrates that fake support and silently produce torn data.

So async-write exists as an internal mode the orchestrator picks when the store supports concurrent reader+writer. It is not a graph-author primitive. The graph author writes `rw` and gets the most parallelism-preserving mode the substrate honestly supports.

### 6.4 Why not also expose explicit `sync` on `r`

We considered "graph designer might want to force sync for reasons X." The candidates were all weak:

- Deterministic replay for debugging — better expressed as a deployment-time toggle on a debug instance.
- Race detection — better expressed as a separate primitive (`r-fenced`) if it ever earns its keep.
- Audit isolation — probably wants exclusion against other readers too, which means `rw`.

None justify the surface complexity. Removing the choice removes the footgun.

## 7. Store config: read_during_write

> **Superseded by §19.4.** The `read_during_write: async | block` toggle was collapsed with `SupportsDiscard` into a 3-value `write_semantics: direct | staged_blocking | staged_async` field. The YAML config patterns below should be read as illustrative only — the actual field name and value set differ. The per-region override pattern (§7.1) still applies.

The single store-level toggle that controls `r-vs-rw` collision behavior:

```yaml
stores:
  content:
    kind: filesystem
    read_during_write: block      # readers wait for writers; writers wait for readers
  app-data:
    kind: postgres
    read_during_write: async      # MVCC; readers see snapshot, writers proceed
```

Values: `async | block`. Verbose enough to be self-documenting; the field name makes the binary outcome unambiguous.

### 7.1 Per-region overrides

Operators sometimes need finer control:

```yaml
stores:
  app-data:
    kind: postgres
    read_during_write: async
    regions:
      audit_log:
        read_during_write: block    # serialize all reads of audit_log against writes
```

Same field name at every level. No vocabulary translation between store-level default, per-region override, and bridge manifest declaration.

### 7.2 Default behavior

The store's manifest declares its **maximum** capability. Operator config can downgrade (force `block` on an `async`-capable store) but not upgrade. A flat-filesystem store cannot be configured into `async` mode — the substrate genuinely cannot offer it.

If the operator doesn't specify, the default is the substrate's max capability: `async` if supported, `block` otherwise. This gives the most parallelism by default, with explicit opt-out for cases (audit, debug) that want serialization.

## 8. Versions are substrate's business

> **Superseded by §19.1.** The 2026-04-26 conversation eliminated the orchestrator-side version concept entirely. No change-signal per region, no outstanding-claim-count per active version, no GC gating, no `versioned` mode. Cascade is driven by node-state transitions ("node committed with `changed=true`"); GC is the substrate's responsibility entirely. The text below was preserved for historical context but does not describe the resolved design. §8.1 and §8.2 are obsolete.

The orchestrator needs **two tiny things** about versions:

1. **A change signal per region** — has the version advanced since I last observed it? (Cascade trigger.) Monotonic counter, content hash, opaque token — anything that flips on commit.
2. **An outstanding-claim count per active version** — for GC gating. The orchestrator must not let a version be deleted while some claim still references it.

That's the entire orchestrator-side version surface. **No history. No replay. No time-travel.** Earlier versions are entirely the substrate's business.

If git retains every commit forever, great. If a flat filesystem clobbers the previous file on rename, also fine. The orchestrator tracks "current version + who's still on older ones, for safe cleanup."

### 8.1 Advanced version use cases are extension verbs

Replay, audit, rollback-to-historical, pinning-to-specific-version are advanced features. Some stores can offer them (git, S3 with versioning); some can't (flat filesystem). They're substrate-specific extension verbs the bridge can expose, not part of Rimsky-protocol.

### 8.2 The prior redesign's `versioned` mode

The prior redesign defines a `versioned` mode with `Restore(target VersionRef)` semantics. This conversation's framing is consistent: `versioned` mode is a substrate-specific capability, gated by `SupportsRestore`. The orchestrator doesn't track historical versions itself; it just routes restore calls to substrates that advertise the capability.

## 9. Read consistency strategies (under the hood)

> **Field rename per §19.4.** `read_during_write: async` below is `write_semantics: staged_async`; `read_during_write: block` is `direct` or `staged_blocking`. The three implementation strategies (snapshot delegation / reader-lease serialization / native MVCC pass-through) and the §9-end paragraph about scheduler-mediated mutex still apply unchanged. Verb signatures here also carry the `version` parameter that was stripped per §19.1; read them without `version`.

For substrates that declare `read_during_write: async` on non-MVCC substrates, the bridge has three valid implementation strategies. The orchestrator doesn't need to know which the bridge picked:

1. **Snapshot delegation** — `AcquireRead` materializes a read-only copy. ZFS clone, Btrfs snapshot, file-tree copy, namespace COPY. Returns an address pointing at the snapshot. `ReleaseRead` deletes it. Writer never blocks.
2. **Reader-lease serialization** — `AcquireRead` registers a lease in bridge state; `Allocate` for a writer blocks until outstanding leases drain. Classic reader-writer lock. Effectively turns async into a substrate-side serialization point — generally not preferred because the orchestrator can do this more cheaply.
3. **Native MVCC pass-through** — substrate handles concurrency; bridge opens a snapshot transaction at `AcquireRead`, ends it at `ReleaseRead`. Cheap and clean.

The bridge picks based on substrate capability. Orchestrator only sees the uniform `AcquireRead/ReleaseRead` lifecycle.

For substrates that declare `read_during_write: block`, scheduler-mediated mutex is the right enforcement layer (not bridge-mediated stall). The orchestrator's existing dispatch-claim machinery extends naturally to region claims; the executor never starts running on a blocked region. No bridge work is required for this case.

## 10. Selector dynamism

> **Extended by §19.2.** A third selector value — **empty** — was added in the 2026-04-26 conversation. Empty selector triggers the substrate's configured auto-pick policy (queues, ring buffers). The two-value framing below is incomplete; the spec session uses three (static, dynamic, empty).

A selector defining a region can be:

- **Static-membership** — fixed set of bytes the selector resolves to (file path, key, table name, explicit row list).
- **Dynamic-membership** — set can shift as underlying data changes (predicate over rows, glob over files-being-created).

This affects what a store can offer in async-read mode. A store might support async-read for static-membership selectors but not for dynamic-membership ones, because predicate stability requires more substrate capability (full transactional MVCC, predicate locks).

Manifest field (provisional):

```
async_consistency:
  static: snapshot | serialized | none
  dynamic: snapshot | serialized | none
```

Scheduler validates per-selector at graph load. A graph that needs async-read on a predicate-shaped region against a store that only supports static cannot bind; validation fails with a clear error.

This is a sub-detail of `read_during_write` — same axis, more nuance. Probably worth deferring the granular form to v2 unless real workloads need it. A simpler v1 option: `async_supports_dynamic_selectors: yes | no` boolean.

## 11. Subgraph-level consistency

> **Resolved per §19.5.** The three modeling options below collapse to: **held-read-claim semantics extend naturally**. Source acquires `AcquireRead`; held-claim machinery propagates through the §11.4 holding-subgraph walk; terminal-leaves release. Frames are the natural envelope under serial_queue / coalesce; under parallel-buffered (post-v1), cross-frame writers compete with the held read claim through the standard eligibility predicate. Locks are substrate-scoped, not frame-scoped — `frame_id` on `rimsky_lock_holders` and `rimsky_claim_holders` is observability-only.

Locks (in the prior redesign's terminology) — and equivalently, claims (in this doc's terminology) — are **first-class graph objects**. They are passed node-to-node and must be resolved within the graph for the graph to be valid. The claim resolution system being implemented in the prior redesign handles this lifecycle.

This conversation surfaced one corner: **multi-node consistent reads**. If sibling nodes A and B both read region R and need to reflect the same point-in-time, per-node leases aren't enough — between A's release and B's acquire, a writer can slip through.

Three modeling options:

1. **Consistency-group lease** — graph declares a group of nodes sharing a lease; orchestrator acquires once, holds across all member dispatches.
2. **Lease delegation** — node A acquires; passes the lease handle to children; only the terminal child releases.
3. **Scheduler holds a logical claim** — for nodes flagged "consistent with X," scheduler refuses to dispatch any writer to R between their dispatches.

(3) is the most Rimsky-idiomatic — extends the existing claim machinery again. Already supported in the prior redesign through claim-hold semantics (`hold: true`, `claim_resolutions`).

The current claim resolution system being implemented covers this case for **claim** objects (queue items). The same mechanism should generalize to region claims for cross-node read consistency. Probably a v2 expansion; the v1 design should keep `AcquireRead/ReleaseRead` as the primitive so subgraph-scope leases can layer on top later without protocol changes.

## 12. Capability manifest (cumulative)

> **Superseded by §19.4.** The 13-field manifest below collapses to 6 fields after stripping version-related capabilities (§19.1) and the `SupportsDiscard`/`read_during_write` overlap (§19.4). See §19.4 for the consolidated set.

The manifest fields surfaced through this conversation, alongside the prior redesign's `Capabilities` struct:

### 12.1 From the prior redesign

- `SupportsRegionLock: bool`
- `SupportsClaim: bool`
- `SupportsDiscard: bool`
- `SupportsResume: bool`
- `SupportsRestore: bool`
- `SupportsAtomicMulti: bool`
- `KeepVersionsMax: int`

### 12.2 Added by this conversation

- `read_during_write: async | block` — the central r-vs-rw collision toggle. Per-store default; per-region overrides.
- `async_supports_dynamic_selectors: bool` (or richer: `async_consistency.{static,dynamic}: snapshot|serialized|none`) — whether async-read can preserve predicate-stable membership.
- `concurrent_writes: serialized | last-writer-wins | conflicts | unsafe` — what happens if two write claims target the same region across version boundaries. Most stores should be `serialized` or `conflicts`. `last-writer-wins` and `unsafe` should fail validation when graphs allow concurrent writers; the orchestrator restricts those stores to `mode: sync` regardless.
- `commit_atomicity_scope: single-region | multi-region | none` — how broad an atomic commit can be. Most substrates are single-region; SQL transactions can be multi-region; flat filesystem is single-file (single-region).
- `versioning_model: mvcc | mutable-current-only` — pre-summary of capability that determines whether `read_during_write: async` requires read-lease tracking.
- `commit_can_fail: bool` — whether `Commit` can return a substrate-side error (git merge conflict, S3 conditional-put mismatch, Postgres serialization). Bridges that say `false` give up retry-on-commit-conflict optimizations entirely.

The manifest is the contract surface. Operators read it to choose stores; the orchestrator reads it to validate graphs at load and to route claim modes at dispatch. Resist the temptation to add fields the orchestrator doesn't actually consume.

## 13. Relationship to the prior redesign

| Prior redesign element | This doc's relationship |
|---|---|
| `core/store/` package + `Store` interface | Compatible. Bridges are an additional implementation of the same interface, just with RPC plumbing. |
| `rimsky_lock_holders` table | Compatible. Region claims live here; concurrency-budget claims live here; same heartbeat/orphan-reap loop. |
| Three modes (direct/sidecar/versioned) | Compatible. Modes describe the substrate's commit shape; orthogonal to the read/write claim vocabulary discussed here. |
| `LockSpec { Region | Claim }` discriminated union | Surface refinement. The conceptual collapse is: every claim is on a region (claim-stores have store-picked regions); claim-vs-region-lock is one axis (who chose the region) and r/rw is another (intent). The two compose without conflict. |
| Lock kinds (`named`, `region`, `claim`) | The redesign keeps named locks for cross-cutting concurrency budgets (rate limits). This doc renames those to "concurrency budgets" for clarity but keeps the mechanism. Region locks become "claims with mode r/rw." ~~Claim locks (queue/ring-buffer item acquisition) stay distinct.~~ **Revised per §19.2:** queue/ring-buffer item acquisition is a region claim with **empty selector** — the substrate's pick policy is side-loaded at store registration. The verb set is uniform; no separate claim-store extension. |
| `Capabilities` struct | Extended with the fields in §12.2. |
| Six-commit implementation sequence | Bridge protocol is post-v1. The prior sequence is unaffected; the bridge is an additive extension after commit 6. |
| Userdata-is-opaque | Unchanged. |
| Attributes as the per-run typed data table | Unchanged. |

**Substantive refinements:**
- Surface the graph-author claim types as `r | rw` (currently the redesign exposes more granular mode controls).
- Add the `read_during_write` config as the explicit name for the only meaningful sync/async choice.
- Document async-write as an internal mode only.

**Additions:**
- Bridge protocol for standalone stores (post-v1).
- Selector dynamism as a manifest axis.
- `commit_can_fail`, `versioning_model`, `concurrent_writes` capability fields.

**No contradictions.** This doc is consistent with the prior redesign's architecture. The two should be readable together; the prior establishes the foundation and this captures the refinement directions.

## 14. Auth: Rimsky stays auth-blind

The auth landscape was the deferred topic at the end of the original conversation. The conclusion: **Rimsky's protocol does not understand auth, and never needs to.** Auth content lives entirely outside the orchestrator's awareness, transported through existing data-passing primitives.

### 14.1 The principle

Rimsky is **inert with respect to claim payload content.** The orchestrator may extract values from claim payloads via graph-declared `source: "{{claim.<store>.<field>}}"` directives, but does not validate, transform, log, normalize, or otherwise act on payload content. It reads the bytes; it has no opinion about them.

This is structurally enforced (no auth verbs in the protocol; no auth fields in any schema) and supplemented by an explicit blessed invariant on **claim payload inertness** (parallel to invariant 11 on userdata opacity, with a carve-out for substitution-time field extraction).

An earlier draft of this section called this property "opacity," which overstates it — Rimsky has the bytes and can read them. The accurate property is *inertness*: Rimsky reads, extracts named fields for substitution, and does nothing else with the content. The threat model that follows from this distinction, and the encrypt-before-pass practice that mitigates it, are in §14.5.

### 14.2 The mechanism

The prior redesign's `ClaimResult.Payload` and the attribute-substitution paths (`{{claim.<store>.<field>}}`, `{{deps.<node>.<field>}}`) are sufficient to deliver credentials end-to-end without any new protocol surface:

1. Bridge's `Allocate` (or `AcquireLock`) returns a `ClaimResult` with arbitrary structured payload — including, optionally, a credential blob that the bridge has minted, scoped, and (optionally) encrypted with a key only the consuming executor possesses.
2. The graph's attributes schema declares a field with `source: "{{claim.<store>.<credential_field>}}"`. At dispatch, Rimsky substitutes the payload into the attribute. The substitution is **content-blind** — Rimsky doesn't know it's a credential.
3. The executor receives the attribute alongside other dispatch inputs. It treats the credential as input data and uses it however its substrate requires (decrypt with its deployment-managed key; pass to substrate driver; etc.).
4. If the credential needs to flow to downstream nodes, it propagates via `{{deps.<node>.<field>}}`. Same substitution, same opacity.

No bridge verb dedicated to credentials. No protocol-level credential type. No Rimsky-side encryption or key management. **The mechanism is the simple addition of arbitrary opaque payload to claim/commit results — and that primitive already exists.**

### 14.3 The auth landscape, fully

Every auth boundary in a Rimsky deployment falls into one of four mechanisms, and only one is Rimsky's:

1. **Opaque claim/commit payloads (Rimsky's surface).** Bridges emit arbitrary data. Encryption is invisible to Rimsky.
2. **Attribute plumbing.** Existing mechanism. Used identically for credentials and for any other inter-node data.
3. **Operator deployment auth (not Rimsky's protocol).** Rimsky processes auth to each other and to operators via standard mechanisms (mTLS, service mesh, IAM, network policies). Wired in deployment config.
4. **Graph-manifest scope declarations (static authorization).** Graphs declare their store dependencies. Scheduler enforces — claims on undeclared stores fail validation. No credentials involved; this is access scoping, not auth.

The boundaries:

| Boundary | Mechanism | Rimsky's role |
|---|---|---|
| Operator → control-api | (3) operator deployment | None |
| End user → control-api | (3) operator deployment | None |
| Rimsky → state DB | (3) operator deployment | None |
| Supervisor ↔ executor (gRPC) | (3) transport-layer auth | None |
| Supervisor ↔ bridge (gRPC) | (3) transport-layer auth | None |
| Bridge → substrate | (3) bridge config | None |
| Executor → substrate (data path) | (1) opaque payload + (2) attribute plumbing | Transport bytes |
| Graph access scope | (4) manifest declaration | Validate at deploy; enforce at dispatch |
| Node access scope | (4) manifest declaration | Validate at deploy; enforce at dispatch |

### 14.4 Why this is correct, not just minimal

The shape isn't an under-design or a punt. It's what happens when you take the responsibilities of an orchestrator seriously:

- **Rimsky's job is coordination of dataflow, not credential management.** A scheduler that understood credentials would necessarily have an opinion about which credentials are valid, which are expired, which can be rotated, which need refreshing — and that opinion would compete with the substrate's, the operator's, and the executor's.
- **Substrates are the ground truth on auth.** Postgres knows when a role is revoked; S3 knows when a presigned URL expires; Redis knows when an ACL changes. A Rimsky-side auth model would be perpetually stale relative to the substrate's actual state.
- **Bridges are the right place to negotiate substrate auth** because they're already substrate-specific. STS for S3, role grants for Postgres, ACL+EXPIRE for Redis, OAuth refresh for cloud APIs — the bridge knows; Rimsky doesn't need to.
- **Executors handle their own consumption.** An executor that calls a substrate already has substrate-specific code. Adding "decrypt this credential first" is a small extension of that existing surface, not a new architectural concern.
- **Service-to-service auth between Rimsky processes is solved by the deployment.** mTLS, service mesh, network policy — these are infrastructure concerns the operator already handles for every other service in the stack. Rimsky inherits whatever they've already built.

### 14.5 Threat model and encrypt-before-pass

Inertness is a property of Rimsky's *processing*, not its *access*. Rimsky holds payload content in memory and persists it in `rimsky_node_attributes` for as long as downstream nodes need it. A compromised Rimsky deployment (DB exfiltration, memory dump, compromised supervisor process) can leak payload content. Inertness does not change that.

The recommended practice for credentials and other sensitive payload fields is **encrypt-before-pass**:

- Operator generates an asymmetric keypair scoped to the consuming executor.
- Bridge config holds the public key; executor config holds the private key.
- Bridge encrypts sensitive fields before returning them in `ClaimResult.Payload`.
- Rimsky transports the ciphertext. Substitution by field name still works (the value is just bytes; substitution is content-blind).
- Executor decrypts at the point of use with its private key.

With this practice, **Rimsky compromise alone does not leak the secret** — both Rimsky and the executor's key store must be compromised. Asymmetric is the recommended default because the bridge holds only the public key, so bridge compromise also doesn't leak the secret. Symmetric is a valid alternative when key distribution is simpler (single-deployment, single-tenant).

Encryption is **field-level**, not whole-payload. Rimsky needs to see the payload structure to substitute by name (`{{claim.<store>.access_token}}`), so the structure stays plaintext while individual sensitive values hold ciphertext.

Encryption happens entirely outside Rimsky. The protocol is unaware. The inertness invariant is preserved because Rimsky never decrypts.

#### Reference helper library (optional)

The reference store implementations may ship an optional encrypt-before-pass helper — a small Go library for bridge authors + a matching SDK helper for executor authors — so common cases are turnkey. Operators with existing crypto stacks bring their own. The helper is shipped *alongside* Rimsky, not *inside* it: no protocol surface, no Rimsky-side awareness of crypto state. A store instance MAY declare `payload_encryption: none | reference-helper | custom` informationally so operators know what they're getting; Rimsky doesn't act on the field.

#### Layered defense: policy first, inertness second

**Encrypt-before-pass is the primary defense** against secret exposure under Rimsky compromise. Where it's applied, secrets are ciphertext throughout Rimsky's surfaces; an attacker compromising Rimsky alone cannot decrypt them.

**Inertness is defense in depth** for cases the policy doesn't cover:

- **Non-secret-but-private content** (work item IDs, business parameters, user-visible strings) that the policy doesn't gate but that still shouldn't fan out from `rimsky_node_attributes` (gated access) to central log aggregators (often broader access).
- **Ciphertext accumulation.** If Rimsky leaks ciphertext widely, an attacker who later compromises the executor's private key can retroactively decrypt historical ciphertext from log archives or trace stores. Inertness keeps ciphertext from accumulating beyond `rimsky_node_attributes`. The policy bounds the present; inertness bounds what's recoverable from the past.
- **Third-party bridges that fail to encrypt.** Rimsky cannot verify that a bridge actually applied the policy. Inertness bounds the leak surface from a misbehaving or malicious bridge.
- **Compliance regimes** (PCI, HIPAA, SOC2) that require structural enforcement rather than declarative policy.
- **Field-design mistakes.** A graph designer pulls a field thinking it's not sensitive; turns out it is. The policy depends on humans recognizing sensitivity at design time. Inertness doesn't.

The policy is sufficient for the headline threat model (Rimsky compromise → secret exposure). The inertness invariant addresses the surrounding cases. Operators in regulated or third-party-bridge contexts benefit from both; simpler deployments may rely primarily on the policy. The doc treats both as first-class.

### 14.6 Edge cases the design handles

- **Audit identity in logs.** Rimsky logs graph/instance/node/dispatch identity. Operators correlate against control-api access logs and substrate audit logs for end-to-end identity. No Rimsky-side auth state needed.
- **Credential expiry mid-dispatch.** Executor sees auth failure, returns error, Rimsky retries; next dispatch's `Allocate` produces fresh credentials. Standard retry path.
- **Tenant rate limiting.** Concurrency budgets via named-lock counting semaphores. Tenant identity is graph-manifest declaration.
- **Identity propagation across services** (downstream Z needs to know original user). Bridge encodes user identity in the credential blob; executor extracts and propagates as substrate-call payload. Rimsky transports bytes.
- **Graph package signing.** Package manager verifies signature at install. Runtime Rimsky just executes the installed graph.

### 14.7 Why this is stronger than `MintAccess` as an extension verb

An earlier draft of this section considered a `MintAccess(region, mode, lifetime) → Credential` extension verb on the bridge protocol, gated by capability declaration. Treating credentials as opaque payload is strictly stronger:

- **Generalizes.** A `MintAccess` verb assumes "credentials" as a special category. Treating credentials as opaque payload means any future auth model the substrate world invents (federated identity, capability tokens, attribute-based encryption) works without protocol changes.
- **Composes with encryption.** A bridge can encrypt the payload with a key only the executor knows. Rimsky's DB compromise doesn't leak credentials. With `MintAccess` returning a structured `Credential`, that encryption story is harder to preserve.
- **Removes a Rimsky-specific verb.** Less surface to maintain; less to teach bridge authors; less to validate in conformance suites.
- **No protocol change.** The mechanism uses primitives that already exist (`ClaimResult.Payload`, attribute substitution). Implementation is documentation + an opacity audit, not protocol expansion.

### 14.8 Implementation requirements

The implementation is documentation-heavy and protocol-light:

1. **New blessed invariant: claim payload content is inert in Rimsky** (parallel to invariant 11 on userdata opacity, with a carve-out for substitution-time field extraction). Audit every code path touching `ClaimResult.Payload` and attribute values populated from claim payloads — confirm no logging of payload content, no schema validation against payload values, no transformation of fields, no leakage to event logs / debug endpoints / traces / metrics / error responses. **Priority: engineering hygiene, not security-critical.** Encrypt-before-pass (§14.5) is the primary defense against secret exposure under Rimsky compromise; the inertness audit hardens the surfaces that handle non-secret-but-private content, ciphertext accumulation, and third-party-bridge misbehavior (§14.5 Layered defense). Don't gate auth-design landing on it; do schedule it as part of normal hardening work.

2. **Auth-blind philosophy section in three guides**:
   - `store-author-guide.md` — bridge author can return arbitrary payloads, including encrypted blobs, with no Rimsky introspection.
   - `executor-author-guide.md` — credentials arrive via the attribute plumbing, same as any other input. Executor decrypts with its deployment-managed key.
   - `operator-guide.md` — Rimsky doesn't manage auth. Wire mTLS / service tokens / IAM between Rimsky processes via deployment. Configure bridges with substrate creds.

3. **Verify `ClaimResult.Payload` is genuinely flexibly structured.** No size limits, no shape constraints, no serialization that breaks encrypted-blob round-tripping. Should already be true from the prior redesign; the audit confirms.

4. **Operator deployment recipes** in `operator-guide.md`: patterns for service-to-service mTLS, per-tenant bridge instances, encrypted-credential pass-through. Reference patterns only — Rimsky doesn't ship the implementations.

5. **Verify graph manifest scope enforcement is end-to-end**: deploy-time validation that declared store deps match referenced stores; runtime check that claim regions are within declared scope. Mostly already in the prior redesign; confirm coverage.

6. **Pre-sweep code-shape hardening (2026-04-26 review found):** `ClaimResult.Payload` is typed `any` (`core/store/types.go:28`), and `core/attributes/substitution.go:walkPath` does eager type-asserted descent through the full payload tree (`cur.(map[string]any)` at each segment). Broad accidental-log surface — any handler of `ClaimResult` or `ClaimStoreHandle` could `slog.Any("payload", ...)` and pretty-print the whole structure. Recommended pre-sweep change: switch `Payload` to `json.RawMessage`, lazily unmarshal into a transient `map[string]any` inside `walkPath` only, discard after extraction. Permanently narrows the audit surface so future code can't accidentally pretty-print structured payloads via `slog.Any` or `%+v`. Same logic applies to the attributes-side `ResolveContext.Deps` shape (`map[string]map[string]any`) — same risk, same fix.

The bridge protocol itself stays at 5 control verbs + the optional read-lease pair, no auth verbs. The auth landscape is fully covered by primitives that already exist (claim payloads, attributes, scope declarations) plus the explicit philosophy that Rimsky is auth-blind.

## 15. Multi-tenant stores

> **Moved per §19.7 to `docs/2026-04-26-control-layer.md`.** The reframed conversation concluded multi-tenant store provisioning is a control-layer feature, not a workload-store feature; it has no effect on the runtime. The text below is preserved as historical context but should NOT be treated as design input when writing the workload-store spec from this doc. The control-layer doc is authoritative on the architecture and use cases.



The pluggable-store and bridge-protocol design assumes a 1:1 mapping between bridge instance and logical store: one filesystem bridge serves one filesystem; one postgres bridge serves one database. This works at small scale and for substrates with natural per-workload isolation (a dedicated postgres for one team, a dedicated S3 bucket).

At deployment scale, it under-supports a real workflow: ops provisions a heavy substrate (postgres cluster, S3 bucket, filesystem mount) and wants many graphs to share it with isolation between them. Without multi-tenancy, every graph would need its own provisioned substrate — which pushes Rimsky toward reinventing IaC at install time.

**Multi-tenant stores** address this with a strict scope: graphs self-service *logical sub-namespaces* within already-provisioned infrastructure. The package manager (or the control-api at install) carves the sub-namespace; ops still provisions the underlying substrate. The IaC boundary stays clean — Rimsky never allocates compute, storage, or IAM, only namespaces.

### 15.1 The model

Ops provisions a substrate and deploys a **multi-tenant bridge** pointed at it. The bridge holds admin-level credentials, allowing it to create and destroy isolated sub-namespaces within the substrate's native isolation primitive:

- Postgres: schemas (or separate databases)
- S3: key prefixes
- Filesystem: subdirectories under root
- Redis: keyspace prefixes or numbered DBs
- Git: per-graph branch namespaces

A graph package declares its sub-store needs:

```yaml
stores:
  - logical_name: data
    parent_kind: postgres
    capabilities_required:
      read_during_write: async
    quota:
      rows_max: 10_000_000
  - logical_name: workspace
    parent_kind: filesystem
    quota:
      bytes_max: 50GB
    auto_destroy: true   # ephemeral; destroyed on uninstall
```

At install time, the package manager:

1. Finds a registered multi-tenant bridge of each required kind.
2. Calls `ProvisionSubstore` on each bridge with the requested name, capabilities, and quota.
3. Registers the resulting sub-store as a regular store in the control-api's store registry, scoped to this graph instance.
4. Binds the graph's logical name (`data`) to the physical sub-store identifier the bridge returned.

From the graph's runtime perspective, the sub-store is just a regular store. The fact that it's namespaced inside a larger multi-tenant resource is transparent.

### 15.2 The admin verb set

Multi-tenant bridges support a small **admin verb set** beyond the standard runtime verbs:

```
ProvisionSubstore(name, config, quota) → SubstoreID
DestroySubstore(id) → ()
ListSubstores() → [SubstoreID]
GetSubstoreUsage(id) → UsageStats
```

These are control-plane only — never called by graphs at runtime, only by the control-api during install/uninstall. The standard runtime verbs (`Allocate`, `Commit`, `Resolve`, etc.) are unchanged; the bridge serves provisioned sub-stores through them like any single-tenant store.

Bridges that don't support multi-tenancy declare `supports_provisioning: false` and serve a single logical store per bridge instance. The model degrades gracefully — single-tenant bridges remain valid for substrates with natural isolation or for lightweight setups where the operator prefers one bridge per workload.

### 15.3 Lifecycle

**Provisioning at install time.** Deterministic, eager. If quota or capability matching fails, the install fails before any runtime state is created — fits the "fail-fast at install" pattern.

**Destruction is opt-in per graph package.** Default `auto_destroy: false`; data preservation is the safer default. Graph re-install on the same Rimsky deployment finds the existing sub-store and reuses it (idempotent by name). Manual destruction goes through a control-api admin endpoint that calls `DestroySubstore`.

### 15.4 Quotas

Per-substrate quotas declared in the graph manifest. Examples:

| Substrate | Quota dimensions |
|---|---|
| postgres | rows_max, schema_size_bytes_max, table_count_max |
| s3 | objects_max, bytes_max |
| filesystem | files_max, bytes_max |
| redis | keys_max, memory_bytes_max |

The bridge enforces:
- **At provision**: rejects if the parent doesn't have capacity for the requested quota.
- **At runtime**: writes that would exceed the quota fail through `Commit` returning an error. Routes through Rimsky's standard retry/give_up vocabulary; quota-exceeded is just another commit failure.

Quota enforcement is best-effort and substrate-specific; some bridges may only enforce at provision time and rely on substrate-level limits at runtime.

### 15.5 Naming and binding

Graphs reference sub-stores by **logical names** in their manifest (`data`, `workspace`). Names are scoped to the graph package — two graphs may both declare a `data` store without collision.

At install, the control-api **binds** each logical name to a physical sub-store identifier. The binding lives in the control-api's store registry, scoped per graph instance. Subsequent claim acquisitions resolve through the binding to the actual provisioned sub-store. Graphs never see physical identifiers; the binding is the only translation layer.

### 15.6 Security between tenants

Twofold isolation guarantee:

1. **Substrate-level namespace isolation.** Postgres schemas can't be queried across without explicit grant; S3 prefixes can't be listed across without explicit IAM; filesystem directories can't be escaped via path traversal. The bridge provisions each sub-store inside the parent's native isolation primitive.

2. **Credential scoping.** When a graph dispatches a node operating on a sub-store, the bridge mints credentials scoped to that sub-store only (per the encrypt-before-pass story in §14.5). Cross-tenant access requires both compromising substrate isolation AND compromising bridge credential minting — defense in depth.

### 15.7 What stays with ops vs. with the package manager

The IaC boundary holds. Ops still owns:

- **Sizing parent stores.** How big a postgres? How many replicas? What S3 region? How much filesystem capacity?
- **Provisioning new bridges.** Deploying a new multi-tenant postgres bridge for a new workload pool.
- **Managing substrate-level credentials.** The bridge's admin creds, key rotation, IAM role design.
- **Capacity planning across tenants.** Total quota allocations vs. parent capacity.

The package manager (or control-api at install) owns:

- **Logical sub-store carving** within ops-provisioned substrates.
- **Quota negotiation and validation** at install time.
- **Capability matching** between graph requirements and bridge capabilities.
- **Lifecycle binding** between graph instances and their provisioned sub-stores.
- **Cleanup orchestration** on uninstall (`DestroySubstore` when `auto_destroy: true`).

This is "logical provisioning" — namespacing inside already-provisioned infrastructure. Bounded, automatic, never crosses into IaC.

### 15.8 Capability manifest additions

Multi-tenant support adds these fields to the bridge's capability manifest (extending §12):

- `supports_provisioning: bool` — whether the bridge implements the admin verb set.
- `provisioning_unit: schema | prefix | directory | keyspace | branch_namespace | ...` — substrate-specific isolation primitive used to carve sub-stores.
- `quota_dimensions: [...]` — substrate-specific quota types the bridge can enforce.
- `default_isolation_grant_model: separate-creds | shared-creds-with-namespace-restriction` — how the bridge isolates sub-store access (informs the auth model in §14).

Operators consume these when registering bridges; the package manager's resolver consumes them when matching graph sub-store requirements to available bridges.

## 16. Key decisions and rationale

> **Multiple decisions revised per §19.** Specifically:
> - Decision #1 ("5 control verbs + 2 optional read-lease") — verb shape preserved per §19.3, but `version` parameter stripped (§19.1).
> - Decision #5 ("Claim is the only primitive; lock disappears") — softened: empty-selector unification (§19.2) means claim-as-only-substrate-side primitive holds; concurrency budgets (named locks) remain a separate primitive without substrate. So strictly: two primitives — store-claims and concurrency budgets.
> - Decision #6 ("Graph-author surface = `r` and `rw` only") — still authoritative on the surface; the rationale's `read_during_write` field is renamed `write_semantics` (§19.4).
> - Decision #8 ("`read_during_write: async | block` as the explicit name") — superseded by §19.4: collapsed into `write_semantics: direct | staged_blocking | staged_async`. Same self-documenting principle; richer value space.
> - Decision #9 ("Versions are substrate's business") — revised in §19.1 to "no version concept anywhere" (eliminated, not minimized).
>
> The decisions on auth (#10–#14), multi-tenant stores (#15–#17), and platform pluggability (#18–#19) are unchanged. The "considered alternatives + chose because X" rationale captured below is still valuable for future sessions to understand why these forks were closed.

A consolidated record of the substantive design decisions made during the conversation, with the alternatives that were considered. Captured so future sessions can read the rationale without re-litigating the choice.

### Store and bridge protocol

1. **Bridge protocol over in-process Go plugins.** Out-of-process bridges with 5 control verbs + 2 optional read-lease verbs, RPC-isolated like executors. Considered Go plugins (brittle, version-locked) and build-your-own-binary (high friction). Chose process isolation because it matches the trust model and the existing executor pattern.

2. **Data path direct executor↔substrate.** Bridge sits on the control plane (claim/commit/cleanup); executor connects natively to the substrate using addresses the bridge returned. Considered bridge-proxies-data-too. Chose direct because bridges stay stateless and small, with no throughput bottleneck.

3. **Brief substrate-side fences at commit, not run-spanning.** Bridges use staging + atomic-swap (rename, table swap, manifest pointer flip) rather than holding substrate-level locks for the executor's full run. Considered long-held transactions. Long-held substrate locks duplicate orchestrator's claim machinery and burn substrate resources unnecessarily.

### Locking semantics

4. **Region = (store, selector), not "region types".** Selectors range from static (paths, table names) to dynamic (predicates, globs); selector dynamism is a property axis, not a partition. Considered key-shaped vs predicate-shaped regions as distinct types. Chose unified concept because the dynamism is a property of the substrate's capability, not a separate primitive.

5. **Claim is the only primitive; lock disappears.** A claim has a mode (`none | sync | async-read | async-write`); the mode determines coexistence rules. Considered keeping lock as a separate concept. Chose collapse because "lock" was only describing what claim modes do to other claims. Five primitives where there had been eight.

6. **Graph-author surface = `r` and `rw` only.** Two claim types exposed to graph authors; sync/async lives in store config (`read_during_write`). Considered exposing sync/async per-claim. Chose store-config because the substrate is what knows whether it can provide stable reads during writes; graph author lacks that information.

7. **Async-write is an internal mode, not a graph-author primitive.** Orchestrator picks `sync-write` or `async-write` based on substrate capability. Considered exposing as graph DSL primitive. Chose internal-only: graph-author choice tempts misuse ("go faster" on substrates that fake support); dispatch claim machinery already enforces single-writer-per-region.

8. **`read_during_write: async | block` as the explicit name.** Considered `async_read`, `concurrent_read`, `r_during_rw`. Chose verbose-but-self-documenting: name encodes what the toggle actually controls (the r-vs-rw collision), removing ambiguity about read-vs-read or write-vs-write.

### Versions

9. **Versions are substrate's business.** Orchestrator tracks only "did it change" (cascade signal) + "outstanding-readers per active version" (GC gating). Considered Rimsky tracking version history. Chose minimal because anything more (replay, restore, time-travel) is substrate-specific capability, exposed via extension verbs.

### Auth

10. **"Inert" rather than "opaque".** Rimsky reads payload bytes for substitution but does not validate, transform, log, or otherwise act on content. Considered "opaque" — but substitution requires field extraction by path, so "opaque" overstated the contract. "Inert" admits substitution while banning introspection.

11. **Opaque payload, not `MintAccess` extension verb.** Bridges emit arbitrary structured payload via existing `ClaimResult.Payload`; credentials flow through attribute substitution. Considered a `MintAccess(region, mode, lifetime) → Credential` verb. Chose payload because: generalizes to any future auth model (federated identity, capability tokens, etc.); composes with encryption (Rimsky never sees plaintext); removes a Rimsky-specific verb; uses primitives that already exist.

12. **Encrypt-before-pass as primary defense, inertness as defense in depth.** Sensitive content is encrypted by the bridge before passing to Rimsky; executor decrypts at use. Considered policy-only or inertness-only. Chose layered: policy bounds the headline threat (Rimsky compromise → secret exposure); inertness covers non-secret-but-private content, ciphertext accumulation, third-party bridges, compliance regimes, field-design mistakes.

13. **Asymmetric encryption as the recommended default.** Bridge holds public key; executor holds private. Considered symmetric with shared secret. Asymmetric is more robust: bridge compromise alone doesn't leak secrets.

14. **Inertness audit is engineering hygiene, not security-critical.** Encrypt-before-pass is the primary defense for the headline threat; the audit hardens secondary concerns. Considered making it a security blocker. Hygiene priority lets it be scheduled as normal hardening work without gating other progress.

### Multi-tenant stores

15. **Logical sub-namespaces only, not real infrastructure provisioning.** Bridges expose admin verbs (`ProvisionSubstore` etc.); control-api invokes them at graph install to carve namespaces inside ops-provisioned substrates. Considered full-stack provisioning. Chose narrow scope: anything more reinvents IaC.

16. **Bridge admin verbs, not control-api orchestration.** The bridge knows how its substrate's isolation primitive works (postgres schemas, S3 prefixes, etc.). Control-api invokes; bridge implements. Considered control-api driving substrate-specific logic directly. Substrate-specific knowledge belongs in the bridge.

17. **`auto_destroy: false` as default for sub-stores.** Data preservation by default; opt-in to auto-destroy on uninstall. Considered defaulting to true. Data loss is more painful than namespace garbage; operators opt into auto-destroy for genuinely ephemeral workloads.

### Platform pluggability

18. **Pluggable platform state backend.** `core/storage/StorageBackend` and `core/queue/DispatchQueue` are interfaces; postgres is the v1 reference; sqlite/redis/etc. are planned future implementations. Considered locking to postgres permanently. Chose pluggable because the interfaces are mostly clean already (one known leak: `pgx.Tx` in `core/queue/`); shipping a second backend doesn't require concurrency-model redesign, just an interface implementation.

19. **Platform components distributed via operator IaC, not via package manager.** The package manager is for shareable user-level workloads (graphs, executors, stores); platform components (state backends, control-api framework, etc.) ship with Rimsky binaries via the org's existing IaC. Considered including platform components in the package manager. Chose separation: different audiences, different distribution channels.

## 17. Open / deferred

> **Several items resolved per §19; see annotations below.**

Items the conversation surfaced but did not settle. Each is worth its own focused discussion before implementation.

1. **Predicate stability** for dynamic-membership selectors. Sub-detail of `read_during_write` capability. Probably manifest field or validation-time check. — **Resolved per §19.4 as `async_supports_dynamic_selectors: bool`.** Granular form (the `async_consistency.{static,dynamic}: snapshot|serialized|none` shape) deferred to v2 if real workloads need it.

2. **Bridge framework.** A Go SDK or polyglot framework lowering the bar for new bridge implementations. Pattern parallels CSI drivers for k8s, Backstage plugins. Out of scope for v1, but the bridge protocol's small surface (5+2 verbs) is friendly to such a framework.

3. **Inlined-vs-standalone convergence.** Whether to migrate the blessed defaults onto the bridge protocol (one path everywhere) or keep two paths (cheaper to ship). ~~The CHANGELOG already flags the hardcoded `"inline-jsonb"` lookup as a post-v1 issue~~ (the resource layer was deleted in the stores-redesign; this reference is obsolete per §19.9). Decision: defer until after the prior redesign lands.

4. **Subgraph-level read consistency.** Multi-node consistent views. Three modeling options in §11. — **Resolved per §19.5: held-read-claim semantics extend naturally.** v1 keeps `AcquireRead/ReleaseRead` as the primitive; held-claim machinery layers on without protocol changes.

5. **Bridge-vs-scheduler enforcement of read leases.** For `read_during_write: block` substrates (now `direct` / `staged_blocking` per §19.4), scheduler-mediated mutex is preferable to bridge-mediated stall (no idle executor processes). For `read_during_write: async` (now `staged_async`) substrates with reader-lease serialization, bridge-side enforcement is unavoidable. Worth documenting which path each substrate takes in the bridge author guide. — **Still open.**

6. **Concurrent-writer safety.** ~~The `concurrent_writes` capability field. Stores that declare `unsafe` (e.g., flat filesystem with no fence) should be restricted to single-writer-per-region globally — the orchestrator already does this via dispatch claim; just need to make sure it's enforced for cross-graph contention too.~~ — **Removed per §19.4: `concurrent_writes` is gone (the field was about cross-version-boundary concurrent writers; without versions, the field has no consumer). Single-writer-per-region is enforced by dispatch claim machinery without an explicit capability.**

7. **Async-read lifecycle (new — §19.9 #8 pending).** Whether to ship `AcquireRead`/`ReleaseRead` in v1 vs post-v1. The implementation strategies in §9 are well-understood; the question is whether v1's first-shipped surface includes the read-lease pair or starts narrower.

8. **`core/queue/DispatchQueue` pgx leak (§19.9 #9 pending).** Already flagged in §0. Concrete refactor plan needed before shipping a second platform state backend.

9. **Stale `inline-jsonb` references (§19.9 #10 pending).** Sweep doc + code references to `inline-jsonb` and remove; the resource layer is deleted.

## 18. Picking up where we left off

> **Updated 2026-04-26 post-implementation.** The original "next session begins the audit" plan is revised. Per §19.6, the audit is preceded by pre-sweep type-shape hardening so the audit surface is structurally narrowed before the sweep. Per §19.10, the broader sequencing is:
>
> 1. Convert this doc into a formal spec, treating §19 as the authoritative resolution where it differs from in-line text. Specifically: adopt the consolidated verb set (§19.3) and capability struct (§19.4); drop versions everywhere (§19.1); make empty-selector first-class (§19.2); fix the §13 mapping table line; resolve naming hygiene (§19.8) before the spec text gets baked in.
> 2. Pre-sweep type-shape hardening (§19.6 step 1) — switch `ClaimResult.Payload` (`core/store/types.go:28`) and `ClaimStoreHandle.Payload` (line 81) from `any` to `json.RawMessage`. Update `core/attributes/substitution.go::walkPath` to lazy-unmarshal. Apply same to `ResolveContext.Deps`. Permanently narrows the audit surface.
> 3. Inertness audit (§19.6 step 2). Codify invariant 20: claim payload content is inert in Rimsky.
> 4. Documentation pass per §14.8 #2 (store-author-guide / executor-author-guide / operator-guide).
> 5. ~~Multi-tenant store admin verbs + control-api `packages/` subsystem + `rimsky_substores` table.~~ **Moved out of this doc per §19.7.** The control-layer doc (`docs/2026-04-26-control-layer.md`) covers provisioning-on-instantiation and control-layer auth as separate work.
> 6. Address remaining §19.9 pending items (async-read lifecycle decision, pgx leak refactor, stale `inline-jsonb` cleanup).
> 7. The bridge protocol implementation itself (out-of-process bridges) is post-v1 per §17 #3 — the in-process `Store` interface adopting the unified verb set comes first. Adoption is a third major rewrite of `core/store/`; pre-v1, that's expected.

The next session will begin the **claim payload opacity audit** — sweep every code path touching `ClaimResult.Payload` and attributes populated from claim payloads to confirm Rimsky never inspects, logs, validates, or otherwise introspects payload content. This is the load-bearing piece of the auth-blind philosophy in §14; everything else (documentation, deployment recipes, scope enforcement) follows.

### Settled across all sessions (revised 2026-04-26 per §19)

- **Two primitives: store-claims and concurrency budgets.** "Claim is the only primitive" was overstated — concurrency budgets (named locks for cross-cutting rate limits) are a separate primitive without a substrate. Within the substrate-facing primitive, claim is the only thing; lock disappears as a separate concept.
- **Region = (store, selector) where selector ∈ {static, dynamic, empty}.** Empty selector triggers the substrate's auto-pick policy — unifies regional stores and queue/ring-buffer stores under one verb set (§19.2).
- **Graph DSL exposes `r` and `rw` only.** Sync/async lives in store config, not graph syntax.
- **Store config: `write_semantics: direct | staged_blocking | staged_async`.** Same vocabulary at every level (store default, per-region override, bridge manifest). Replaces `read_during_write` per §19.4.
- **Three-row collision matrix.** r-vs-r never blocks; rw-vs-rw always blocks; r-vs-rw depends on `write_semantics` (only `staged_async` allows reads during writes).
- **No version concept.** Cascade is "node committed with `changed=true`"; GC is substrate's responsibility. No orchestrator-side version tracking, change-signal, or GC pins. Restore / replay / time-travel are substrate-specific extension verbs the orchestrator never sees. (Revised from "versions are substrate's business" per §19.1.)
- **Async-write is an internal mode**, not a graph-author primitive.
- **Single-writer-per-region** is enforced by dispatch claim machinery, regardless of mode.
- **Substrate-side fences** are brief and applied at commit; bridges that need run-spanning substrate locks are doing the orchestrator's job.
- **Substrate-side commit failures** surface as `Commit` errors and route through Rimsky's existing retry/give_up/invalidate vocabulary.
- **Bridge protocol surface**: 5 control verbs (`ResolveRegion`, `Allocate`, `Commit`, `Abandon`, `Delete`) + optional read-lease pair (`AcquireRead`, `ReleaseRead`). Data path is direct executor↔substrate.
- **Rimsky stays auth-blind.** No protocol surface for credentials. Auth content flows through opaque claim payloads via existing attribute plumbing. Bridges and executors handle substrate-side auth; operators handle service-to-service auth via deployment config (mTLS, service mesh, IAM).
- **Claim payload inertness** is a new blessed invariant parallel to userdata opacity (with a carve-out for substitution-time field extraction). Rimsky reads the bytes for substitution but does not log, validate, transform, or decrypt them. Sensitive content uses encrypt-before-pass (§14.5); Rimsky never holds plaintext for encrypted fields.
- **Multi-tenant stores moved out of this doc per §19.7.** Provisioning-on-instantiation (and control-layer auth) live in `docs/2026-04-26-control-layer.md`. The runtime is unchanged by this; control-layer features carve namespaces; runtime sees stores uniformly.
- **Sub-graph composability** is deliberately deferred — should first be supported as a local concept (graph fragments / templates within a single deployment) before being packaged. Out of scope for the package manager design until then.

### Watch out for

- **The doc is a third rewrite of `core/store/`, not a refinement.** Earlier text framed this as "compatible with the prior redesign's machinery." That was overstated: the consolidated verb set (§19.3) and capability struct (§19.4) require rewriting the in-process `Store` interface, the supervisor's runner code, and the existing store impls. Pre-v1 → break freely; rewrites are expected. Don't try to thread compat shims; the version-stripping (§19.1) and empty-selector unification (§19.2) are clean breaks from what shipped.
- **The transparency principle still applies.** Executors should not need special "rimsky-store" code. The bridge protocol preserves this — executors get a native-shape address from the bridge and use standard substrate tooling.
- **Resist over-specifying the manifest.** Add fields only when the orchestrator actually consumes them. Each capability flag is a contract the bridge author must honor and the orchestrator must validate; the cost compounds. (§19.4 already trimmed from 13 fields to 6 — keep it tight.)
- **Don't expose async-write as a graph-author primitive even if pushed.** The footgun analysis (§6.3) is the load-bearing argument. If a use case seems to require it, the answer is probably a separate primitive (e.g., explicit fencing-read) or an operator-level toggle, not surfacing the dangerous mode.
- **Don't add auth verbs or auth fields to the bridge protocol.** The auth-blind philosophy (§14) is load-bearing. If a use case seems to need Rimsky-side credential awareness, the answer is bridge-emitted opaque payload + attribute plumbing — not a new protocol surface.
- **Pre-sweep type-shape hardening before the audit.** §19.6 step 1: switch `ClaimResult.Payload` and `ClaimStoreHandle.Payload` from `any` to `json.RawMessage`; lazy-unmarshal in `walkPath`. Doing the audit first wastes work — anywhere that touches `Payload` directly will rewrite once for the audit, then again for the type change. Land the type change first.
- **The claim payload audit is engineering hygiene, not a security blocker.** Encrypt-before-pass (§14.5) is what prevents secret exposure under Rimsky compromise; the audit hardens inertness for cases the policy doesn't cover (non-secret-but-private content, ciphertext accumulation, third-party bridges, compliance regimes — §14.5 Layered defense). Any leak (a debug log of payload content, a schema validator that touches payload values, an event-log entry that includes payload, a span that attaches payload as an attribute) breaks inertness; the fix for each is local (redact / mark sensitive), but the discovery requires a careful sweep across every consumer of `ClaimResult.Payload`.
- **Don't conflate the two primitives.** Store-claims (always have a substrate, always lock a region — including the "empty-selector" case where the substrate auto-picks) and concurrency budgets (named locks for cross-cutting rate limits, no substrate) live in the same `rimsky_lock_holders` table but are conceptually different. The spec should disambiguate noun usage (§19.8).

### What's deliberately not in this doc

- **Implementation sequence for the bridge protocol.** ~~Premature; the prior redesign's six commits land first.~~ The stores-redesign and frame-resolution work has landed; the bridge protocol's implementation sequence is post-v1 per §17 #3. The in-process `Store` interface adopting the unified verb set (§19.3) comes first.
- **Specific manifest schema (YAML/proto).** Now ready — §19.4 settled the capability struct.
- **Migration path from inlined to standalone for existing stores.** Per the project's "pre-v1, break freely" rule, the answer is "rewrite, don't migrate" once the bridge protocol exists.
- **Specific bridge implementations.** Filesystem, postgres, redis, mongo, git, S3. Each gets its own design when prioritized.
- **Discovery / package registry mechanics.** OCI is the obvious answer; specifics deferred. The package install endpoint and manifest fields are in scope for v1 (§19.7); package distribution format is post-v1.
- **Per-language bridge SDK.** Worth doing when the bridge protocol stabilizes. Go and TypeScript at minimum (mirroring executor language coverage).

This doc is the conversational state of the design as of 2026-04-26 (post-implementation walkthrough complete; §19 added). Ready to inform conversion to a formal spec, with §19 as the authoritative resolution where it differs from in-line text.

---

## 19. Discussion outcomes — 2026-04-26 post-implementation walkthrough

This section captures decisions from a 2026-04-26 conversation that walked the doc end-to-end after the stores-redesign and frame-resolution implementations landed in `docs/specs/2026-04-25-stores-redesign-design.md` and `docs/specs/2026-04-26-frame-resolution-design.md`. Where the in-line text in §§4–15 contradicts these outcomes, this section is the authoritative resolution; the in-line text is preserved for context but should be read with §19 in mind.

### 19.1 Versions are gone, not minimized (revises §4 verb signatures, §5.1, §8)

The earlier framing in §5.1 and §8 retained a minimized version concept (change-signal + GC-pin). That's eliminated entirely:

- No version tracking by the orchestrator. No change-signal per region. No outstanding-claim-count per active version. No GC gating.
- Cascade trigger is "node committed with `changed=true`," driven by node-state transitions — same as rimsky's existing cascade. There is no version-advance signal.
- GC is the substrate's responsibility entirely. Restore / replay / time-travel are substrate-specific extension verbs the orchestrator never sees.
- The `version` parameter is stripped from every verb in §4.
- §8.2's reference to the prior redesign's `versioned` mode + `Restore` is removed — the as-shipped store interface never adopted `versioned` mode.

This simplifies the verb signatures (§19.3), the capability manifest (§19.4 — eliminates `KeepVersionsMax`, `SupportsRestore`, `versioning_model`, `concurrent_writes`), and the §5.1 claim definition (no version pin).

### 19.2 Empty-selector unifies regional and queue/ring-buffer stores (revises §5, §13)

The §13 mapping table line "Claim locks (queue/ring-buffer item acquisition) stay distinct" is wrong. Empty selector unifies them.

Region is `(store, selector)`. The selector axis has three values: **static, dynamic, empty**.

- **Static-membership** — file path, table name, explicit key, explicit row list.
- **Dynamic-membership** — predicate over rows, glob over files-being-created.
- **Empty** — substrate runs its built-in pick policy (FIFO for queues, ring rotation for ring buffers, LIFO, custom). The pick policy is **side-loaded at store registration**, not declared in the graph.

The substrate's identifier for the picked region (item ID, slot index, etc.) becomes the `region_data` on the lock-holder row — same shape as static-selector regions, just substrate-chosen.

Implications:
- The verb set in §4 applies uniformly to regional and policy-pick stores. **No separate claim-store extension verbs.**
- The action vocabulary (`release_to_back`, `release_to_head`, `delete`) is **substrate-internal** with optional per-template override at commit time. It doesn't surface as separate verbs at the protocol level.
- The capability flag `SupportsClaim` is renamed `SupportsEmptySelector` (clearer intent: the store has a configured auto-pick policy).
- Hybrid stores (postgres-as-data + claim-store-postgres on the same physical DB) become trivial: one store registration, two ways to be used (explicit selector for direct access, empty selector for queue/ring-buffer access).

### 19.3 Verb set after consolidation (revises §4)

After §19.1 (version-strip) and §19.2 (empty-selector unification):

```
ResolveRegion(region) → Address
Allocate(region) → StagingAddress
Commit(region, staging, policy_override?) → ()
Abandon(region, staging, policy_override?) → ()
Delete(region) → ()
AcquireRead(region) → AddressLease       # optional
ReleaseRead(lease) → ()                  # optional
```

5 + 2 optional. The convergence story (§2.3) holds: in-process Go `Store` interface ≡ bridge protocol RPC interface — same 5+2 verbs in both places. Adopting this is a third major rewrite of `core/store/` post-shipped; pre-v1, that's expected.

### 19.4 Capability struct collapsed to 6 fields (revises §12)

After version strip and the discard / `read_during_write` overlap collapse:

```
SupportsRegionLock: bool
   # store can declare regions for static/dynamic-selector locking

SupportsEmptySelector: bool
   # substrate has a configured auto-pick policy (queues, ring buffers)
   # (renamed from SupportsClaim, which was overloaded)

SupportsResume: bool
   # a partially-committed claim can be resumed (orchestrator routes
   # ReleaseClaim with preserve_for_resume action)

write_semantics: direct | staged_blocking | staged_async
   # how writes coordinate with readers, and whether Abandon is meaningful:
   #   direct          — writes hit live data; no Abandon; r-vs-rw blocks
   #   staged_blocking — writes go to staging; Abandon discards; r-vs-rw blocks
   #   staged_async    — writes go to staging; Abandon discards; r-vs-rw allows reads-during-writes
   # collapses prior `SupportsDiscard` + `read_during_write`
   # (one implies the other; staged_async ⟹ discardable)

async_supports_dynamic_selectors: bool
   # only meaningful when write_semantics = staged_async; whether the
   # async snapshot mechanism preserves predicate-stable membership
   # for dynamic-selector regions

commit_atomicity_scope: single_region | multi_region | none
   # how broad an atomic commit can be. Replaces SupportsAtomicMulti.
   # `commit_can_fail` is implicit: any non-`none` value implies commit
   # can fail with substrate-side errors (serializability, conditional-put,
   # merge conflicts, etc.).
```

13 fields → 6.

Optional informational manifest field for the encrypt-before-pass story:

```
payload_encryption: none | reference-helper | custom
```

Informational only; Rimsky doesn't act on it. Tells operators what they're getting from the bridge.

### 19.5 Frame-resolution interaction (extends §11)

Frames (per `docs/specs/2026-04-26-frame-resolution-design.md`) are a primitive the original doc didn't anticipate. Frames don't change the lock/claim model structurally:

- Locks are **substrate-scoped**, not frame-scoped or instance-scoped. The eligibility predicate counts holders across all frames; cross-frame contention is handled by the existing §13.3 acquisition transaction without modification.
- The `frame_id` column on `rimsky_lock_holders` and `rimsky_claim_holders` is **observability-only** — operators query "which frame held this claim," but the resolution algorithm and eligibility checks don't read `frame_id`.
- Under serial_queue / coalesce (Rules 3a / 1): frames don't overlap per instance; locks acquired in frame N are released by frame N's terminal nodes.
- Under parallel buffered (Rule 3b, post-v1): two frames can each acquire a different held claim from the same store via empty-selector pick (substrate gives each frame a different item); the existing eligibility predicate handles same-region contention via the standard mutex semantics.
- §11's three modeling options for multi-node consistent reads collapse to: **held-read-claim semantics extend naturally** for subgraph-level consistency. Source acquires `AcquireRead`; held-claim machinery propagates through the §11.4 holding-subgraph walk; terminal-leaves release. Frames are the natural envelope under 3a; under 3b, cross-frame writers compete with the held read claim through the standard eligibility predicate.

### 19.6 Auth-blind sequencing (extends §14)

§14 is largely unchanged. Concrete sequencing decisions for the implementation:

1. **Pre-sweep type-shape hardening (§14.8 #6) — before the audit.** Switch `ClaimResult.Payload` from `any` to `json.RawMessage` (`core/store/types.go:28`). Same for `ClaimStoreHandle.Payload` (line 81). Update `core/attributes/substitution.go::walkPath` to lazily unmarshal into a transient `map[string]any` only inside the function; discard after extraction. Apply the same to `ResolveContext.Deps` (`map[string]map[string]any` → `map[string]json.RawMessage`). Permanently narrows the audit surface: anywhere that touches `Payload` directly now sees opaque bytes; only `walkPath` deals with structured form. Side benefit: `slog.Any("payload", p)` no longer pretty-prints structure.

2. **Then audit.** Sweep every consumer of `ClaimResult.Payload` and attribute values populated from claim payloads. Specific targets:
   - `proto/v1/events.proto` and the supervisor's emit path — confirm no event payload includes claim payload content.
   - `core/attributes/substitution.go` error paths — confirm substitution failures don't log payload structure.
   - The `rimsky_events` `event_detail` JSON column — confirm payload-derived values don't land there.
   - Stub executor and harness — sweep test code for accumulated debug pretty-prints.

3. **Codify invariant 20: claim payload content is inert in Rimsky.** Annotate at `core/store/types.go` on `ClaimResult.Payload` and at `core/attributes/substitution.go::walkPath` (the only sanctioned introspection site). Update CLAUDE.md gotchas + blessed-invariant list. Carve-out language explicit: "Rimsky reads payload bytes for substitution by field name; does not log, validate, transform, normalize, or otherwise act on payload content."

4. **Documentation pass per §14.8 #2** — `docs/store-author-guide.md`, `docs/executor-author-guide.md`, `docs/operator-guide.md`.

5. **Asymmetric encryption is the recommended default** for encrypt-before-pass: bridge holds public key, executor holds private. Symmetric is a valid alternative for simpler deployments.

6. **Reference helper library is optional.** Ships alongside Rimsky, not inside it. Cheap addition; v1 is reasonable if the spec-author wants it.

### 19.7 Multi-tenant stores — moved to `docs/2026-04-26-control-layer.md`

> **Topic moved.** A 2026-04-26 follow-up conversation reframed this topic substantially. Key insights that came out:
>
> - The runtime sees a store uniformly. Whether a store was YAML-configured by ops or carved by control-api at instance creation is invisible to the runtime. **No new runtime concept; no `SupportsProvisioning` capability flag; no special dispatch-time handling.**
> - Provisioning is purely a control-layer concern, triggered on DAG instance creation. There's no separate "graph package install" gate — the prior framing was overcomplicated.
> - **Any writeable store can be provisioned.** It's a baseline expectation, not an opt-in capability. The substrate's isolation primitive (schema, prefix, directory, keyspace) is what gets carved.
> - The substantive design work is enumerating the use cases (per-instance ephemeral, per-instance persistent, per-DAG, per-tenant, global, external pre-existing) and the per-substrate "store rules" matrix (how each substrate expresses each shape).
>
> The full discussion + the design space lives in `docs/2026-04-26-control-layer.md`. That doc also covers control-layer auth as a sibling concern. **The spec session writing this stores-redesign doc should treat multi-tenant provisioning as out of scope — it's a control-layer feature, not a workload-store feature, and belongs to a different spec.**

The original §15 prose remains in this doc as historical context for the conversation that produced it; treat the control-layer doc as authoritative on the architecture and use cases going forward.

### 19.8 Naming hygiene

A few terms in the doc are overloaded; the spec session should disambiguate:

- **"Claim store"** — every store handles claims; the queue/ring-buffer case is "store with empty-selector support." Drop "claim store" as a noun in the spec. The kind name `claim-store-postgres` should rename (e.g., `policy-pick-postgres`, or just `postgres` with a `SupportsEmptySelector: true` capability flag).
- **"Claim"** — overloaded as both noun (a row in `rimsky_lock_holders` / `rimsky_claim_holders`) and action verb (acquire-an-item-from-empty-selector store). Spec should prefer disambiguated forms: "region claim" (claim on a regional store's selector), "item claim" (claim on a store via empty-selector pick), "budget claim" (consumption of a named concurrency budget).
- **"Region"** — invented term, but works fine. Continue using.

### 19.9 Pending — items not yet discussed

The following items from the 10-item gap analysis (informally tracked in `docs/2026-04-26-package-manager.md`) were paused before the discussion completed. Address when the doc is converted to a formal spec:

- **#8 Async-read lifecycle (`AcquireRead`/`ReleaseRead`).** Ship in v1 vs post-v1; how it composes with the held-read-claim semantics in §19.5; the read-lease serialization vs MVCC pass-through implementation strategies (§9).
- **#9 `core/queue/DispatchQueue` pgx leak.** Already flagged in §0 (Notes for whoever lands the second backend, item 2). Concrete refactor plan needed before shipping a second platform state backend.
- **#10 `inline-jsonb` hardcoded lookup.** Now obsolete — the resource layer was deleted in the stores-redesign. Any stale doc references should be removed.

### 19.10 Sequencing summary for the fresh session

When this doc converts to a formal spec, the sequencing that fell out of the 2026-04-26 conversation:

1. Adopt the consolidated verb set (§19.3) and capability struct (§19.4). Drop versions everywhere (§19.1). Make empty-selector first-class (§19.2). Fix the §13 mapping table line.
2. Resolve naming hygiene (§19.8) before the spec text gets baked in.
3. Pre-sweep type-shape hardening (§19.6 step 1) — before the audit.
4. Inertness audit (§19.6 step 2). Codify invariant 20 (§19.6 step 3).
5. ~~Multi-tenant store admin verbs + control-api `packages/` subsystem + `rimsky_substores` table.~~ **Moved per §19.7 to `docs/2026-04-26-control-layer.md`.** Out of scope for the workload-store spec.
6. Address the §19.9 pending items (async-read lifecycle decision, pgx leak refactor, stale `inline-jsonb` cleanup).
7. The bridge protocol implementation itself (out-of-process bridges) is post-v1 per §17 #3 — the in-process `Store` interface adopting the unified verb set comes first.
