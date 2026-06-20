---
topic: lock-state-in-rimsky-not-producer
kind: invariant
---

# Lock state is the persistence layer's; producers persist only their own data state

## Description

Whether anyone is currently holding lock X is answered exclusively by the `rimsky_claim_handle` table on rimsky's side. ClaimProducer implementations may persist their own data state (an items-table row, a staging directory, MVCC snapshot metadata) but they do not persist lock state. This is encoded as the lock-state-ownership invariant at `foundation/locks/interface.go:46-52` (and on the proto interface comment in `protocols/claimproducer/claimproducer.go:17-21`).

The migration at `foundation/persistence/postgres/migrations/001-initial.sql:170-209` lays out `rimsky_claim_handle` as the lock primitive: per-row `lock_kind`, `lock_name`, `scope_data`, `holder_supervisor_id`, `expires_at`, `is_held`, `realized_write_semantics`. The byte-equal scope-conflict comparison (`foundation/locks/conflict.go:72`) walks this table only.

The no-internal-serialization rule (`foundation/locks/interface.go:54-57`) extends the rule: **producers must not internally serialize on lock-shaped predicates either**. The reader-lease serialization pattern is forbidden for `staged_async` write-semantics — honest support requires snapshot delegation or native MVCC pass-through. The reasoning at `docs/concepts/write-semantics.md`: a producer that fakes `staged_async` by serializing readers internally would undermine the conflict matrix that rimsky's claim-handle table guarantees.

The rationale lives in the invariant docstring at `foundation/locks/interface.go:46-57`: this is the construct that lets rimsky guarantee single-writer-per-scope at the rimsky layer (`rimsky_claim_handle` is the conflict predicate's only input), independent of how each producer canonicalizes scope or whether it has its own internal locks. Pulling responsibility into rimsky lets producers concentrate on their data-state side, which is what makes a producer producer-shaped rather than database-shaped.

`docs/concepts/claim-handle.md` reinforces this: "There is exactly one ledger of acquired claims — rimsky's. Producers that try to mirror the ledger reinvent eligibility-checking and inevitably drift."

The price of the rule: producer state and lock state can diverge under failure (producer commits writes but rimsky-side claim row was reaped). The verify-before-run guard compensates: after the acquisition tx commits, the runner re-reads ownership before invoking the executor and bails if it moved. The orphan reaper does NOT call `Store.Abandon` on lock-handle sweep (the no-producer-abandon discipline at `foundation/integration/orphan_reaper.go`); producer-side state is recovered by the producer's own TTL/sweep.

A new producer needs to expose a way to canonicalize scope bytes such that two acquisitions that should conflict produce byte-equal scope output — that's the only mechanism rimsky has. Per `docs/concepts/scope.md`: "Producers that try to mirror the [lock] ledger reinvent eligibility-checking and inevitably drift."

## Code surface

- `foundation/locks/interface.go:46-57` — lock-state-ownership and no-internal-serialization annotations.
- `protocols/claimproducer/claimproducer.go:17-21` — proto-level invariant comment.
- `foundation/persistence/postgres/migrations/001-initial.sql:170-209` — `rimsky_claim_handle` schema.
- `foundation/locks/conflict.go:64-77` — `ScopesByteEqual` (the predicate that walks `rimsky_claim_handle` only).
- `foundation/integration/orphan_reaper.go` — no producer `Abandon` on sweep.
- `modeling/scheduler/scheduler.go:28-32` — orphan-cutoff scheduling.
- `stores/postgres/` — reference impl that respects 9a (state in producer's own DB, lock state in rimsky).

## Prose surface

- `CLAUDE.md` "Blessed invariants" — lock-state-ownership and no-internal-serialization clauses.
- `docs/concepts/claim-handle.md` — "the sole authority on lock state."
- `docs/concepts/claim-producer.md` — "the producer never persists lock state."
- `docs/concepts/write-semantics.md` — reader-lease forbidden for staged_async.
- `.ok-planner/specs/2026-05-04-service-protocol-contract.md` — the lock-state-ownership clause in the contract.

## Adjacent topics

- `2026-05-10-byte-equal-scope-conflict` — the only mechanism rimsky has to detect overlap.
- `2026-05-10-verify-before-run-guard` — compensates for divergence under failure.
- `2026-05-10-orphan-reaper-no-producer-abandon` — orphan reaper's no-Abandon policy.
- `2026-05-10-write-semantics-envelope-handshake` — staged_async honesty.

## Observations

- The no-internal-serialization rule forbids reader-lease serialization specifically; the more general "producers don't serialize on lock-shaped predicates" is harder to verify conformance against. The conformance binary (`cmd/rimsky-claim-producer-conformance`) checks lock-state-ownership (no lock-state persistence visible across `Open` → `Open` for byte-equal scope) but does not exhaustively probe the internal-serialization variants.
- The "lock state vs data state" split is asymmetric: rimsky reads producer-supplied address/payload at substitution-leaf only (per the claim-inertness invariant in `concept:claim-handle`), but rimsky writes nothing back to the producer's data state. The producer's `Commit` / `Abandon` verbs are the only producer-side state writes rimsky originates.
- A producer that drifts (e.g. a producer that locally caches "this scope is busy" with a stale view) would silently allow concurrent acquisitions; `docs/concepts/claim-handle.md` flags this as "inevitable drift."
- The CLAUDE.md hint about Phase-5 consolidation ("rimsky_claim_handle replaces the legacy rimsky_lock_holders") is the schema-level evidence that lock-state ownership has stayed with rimsky across the v2 → v3 redesign.
