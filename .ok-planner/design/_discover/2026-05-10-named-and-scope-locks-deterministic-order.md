---
topic: named-and-scope-locks-deterministic-order
kind: invariant
---

# Two lock primitives (named, scope); acquisition walks `(lock_kind, sort_key)` in deterministic order

## Description

A node can require multiple coexisting acquisitions: zero or more **named locks** (mutex / counter semaphores configured at deployment scope) and zero or more **scope-keyed claims** (producer-managed). Acquiring them in arbitrary order admits a deadlock: supervisor A holds N1 and asks for S1; supervisor B holds S1 and asks for N1. Deterministic ordering closes this window.

Two primitives, two types, no common interface:

- **`locks.NamedLockSpec{Name}`** (`foundation/locks/types.go:79`) — producer-independent. The capacity (`mutex` or `counting` with limit) lives in operator config (`named_locks:` block in `rimsky.yml`). Tracked by `lock_kind='named'` rows in `rimsky_claim_handle`.
- **`locks.ClaimSpec{StoreName, Selector, Intent, Alias}`** (alias for `claimproducer.ClaimSpec`) — producer-bound. The producer parses `Selector`; rimsky persists the resolved `Scope` bytes. Tracked by `lock_kind='scope'` rows in `rimsky_claim_handle`.

The two types do not share an interface. The comment at `foundation/locks/types.go:16-17` is explicit: "Two types, no common interface: ClaimSpec and NamedLockSpec are distinct. Callers dispatch by type." This avoids the polymorphism trap where named locks and claims look interchangeable but differ in disposition semantics (named-lock disposition is rimsky-internal — increment/decrement the holder count; claim disposition is producer-driven — `Commit` / `Abandon` / `Release`).

The deterministic lock-ordering rule (`foundation/integration/runner.go:20-29`) requires that all per-spec lock acquisitions for a candidate are walked in `(lock_kind, sort_key)` order. Three sites walk the same sorted slice:

- Named-lock advisory locks (`TakeNamedLockInTx` per `2026-05-10-advisory-locks-tick-and-migrate`).
- Scope re-evaluation (`evaluateScopeConflict` at `runner_acquire.go:670`).
- The per-spec `ClaimProducer.Open` + claim-handle INSERT loop.

The sort is implemented at `foundation/integration/runner_locks.go::sortLockSpecs` (stable lexical ordering on `(kind, key)`). Removing or reordering the sort in any of those three sites reintroduces the deadlock — the rule is annotated and tested at `test/scenarios/locks/`.

`ModeCoexists` (`foundation/locks/conflict.go:44-62`) implements the `(write_semantics, intent)` coexistence matrix used by the scope-conflict predicate. The matrix is parameterized over the realized write-semantics returned by the producer's `Open` — it lets `sync`+`rw` claims serialize, `staged_async`+`r` claims coexist with a writer, etc.

`docs/concepts/named-lock.md` and `docs/concepts/claim.md` are the consumer-facing presentations of the two primitives. The named-lock doc emphasizes "scalar capacity counter at the deployment level"; the claim doc emphasizes producer-driven semantics.

## Code surface

- `foundation/locks/types.go:73-110` — `NamedLockSpec`, `ClaimSpec`, two-types-no-interface comment.
- `foundation/locks/conflict.go:44-77` — `ModeCoexists` + `ScopesByteEqual`.
- `foundation/integration/runner.go:20-29` — deterministic lock-ordering annotation.
- `foundation/integration/runner_locks.go` — sort + acquisition walk.
- `foundation/integration/runner_acquire.go:670-705` — `evaluateScopeConflict`.
- `foundation/persistence/postgres/migrations/001-initial.sql:170-209` — `rimsky_claim_handle.lock_kind` CHECK.

## Prose surface

- `docs/concepts/named-lock.md` — concept doc.
- `docs/concepts/claim.md` — concept doc.
- `docs/concepts/write-semantics.md` — `ModeCoexists` matrix.
- `CLAUDE.md` "Blessed invariants" §3.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` §4 — multi-lock atomicity contract.

## Adjacent topics

- `2026-05-10-advisory-locks-tick-and-migrate` — per-kind advisory locks taken in the same sorted order.
- `2026-05-10-byte-equal-scope-conflict` — scope conflict predicate sites.
- `2026-05-10-atomic-acquisition-decoupled-tx` — acquisition tx wraps the walk.
- `2026-05-10-write-semantics-envelope-handshake` — `ModeCoexists` matrix input.

## Observations

- The "two types, no common interface" rule is observable in the call site: `runner_acquire.go` dispatches by type assertion rather than calling a `Lock` interface method. This is structurally enforced (the compiler accepts both shapes) only by convention — a future `interface { Acquire(...) }` would be backwards-fitable but is explicitly resisted.
- Adding a third lock kind requires extending the sort, the conflict matrix, and the per-spec acquisition loop. There is no enumerative table for `lock_kind` in code; the CHECK constraint at the schema level is the only single source.
- `sortLockSpecs` returns a stable order (the sort key is "`name` for `NamedLockSpec`, `store_name + ':' + scope_or_selector_hash` for `ClaimSpec`" — verify in code) so two supervisors with identical lock-spec lists always agree on the walk order. Stability under repeated sort calls is required.
- CLAUDE.md "Vocabulary" notes `ClaimProducer` is the canonical protocol name and `Store` the colloquial bundled-services term. `ClaimSpec` lives in `claimproducer/` (protocols module) but is aliased into `foundation/locks/` for caller convenience.
