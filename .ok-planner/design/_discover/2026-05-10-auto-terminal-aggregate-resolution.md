---
topic: auto-terminal-aggregate-resolution
kind: invariant
---

# Held-claim resolution is auto-terminal, single, and aggregate-outcome-driven

## Description

A held claim's lifetime spans more than the acquirer's run: it covers the acquirer plus every directly-declared inheritor (the "holding subgraph" — see `docs/concepts/holding-subgraph.md`). The producer verb (`Commit` / `Abandon`) must fire exactly once at the end of the subgraph's combined execution, after every member has reached a terminal state. The choice of verb depends on the aggregate outcome of every member: all-success → `Commit`; any-failure → `Abandon`.

The held-claim auto-terminal invariant (annotated at `foundation/integration/auto_terminal.go:5-17`) implements this. The algorithm at `CheckAndFireResolution` (lines 55-123):

1. `SELECT … FOR UPDATE` on the `rimsky_claim_handle` row — race-safe against concurrent terminations of sibling members in the same subgraph.
2. List `rimsky_claim_holders` rows for the claim_handle; if any row has `state='active'`, return (subgraph incomplete).
3. Compute aggregate outcome: if any row is `state='failed'` → fire `Abandon`; else (all `state='completed'`) → fire `Commit`.
4. Delegate to the unified `ResolveClaimHandleTerminal` engine (`foundation/integration/terminal_decision.go`), which calls the producer verb over gRPC and then deletes the `rimsky_claim_handle` row claimant-guarded (`AND holder_supervisor_id = $1`).

`rimsky_claim_holders` is the per-(claim_handle, holder_node) state ledger (`foundation/persistence/postgres/migrations/001-initial.sql:221-232`). The acquirer plus inheritors are inserted as `rimsky_claim_holders` rows at acquire time (`foundation/integration/runner_acquire.go:706-743`); each member updates its row at its own terminal. The FK column is `claim_handle_id` (renamed from the legacy `lock_holder_id`).

The producer verb fires **before** the surrounding rimsky tx commits. This means there is a verb-then-tx-fail leak path: the producer's `Commit` could succeed and rimsky's containing tx could roll back. The foundation contract requires terminal verbs to be idempotent in `claim_id` so that a retry from the next supervisor run is harmless (annotated at `auto_terminal.go:43-50`).

The aggregate-outcome rule is deliberately simple. `docs/concepts/holding-subgraph.md` is explicit: "Rimsky does not orchestrate partial commits, partial rollbacks, or first-delete-wins reconciliations. The aggregate-outcome rule is the rule." Producers decide what `Commit` and `Abandon` mean for their own state (atomic flip of an items-table row, deletion of a staging directory, MVCC commit, etc.) — rimsky's job is to tell them which one fired and when.

`docs/concepts/parked.md` notes that the held-claim auto-terminal mechanism continues to fire correctly across the park boundary: a parked node remains an `active` row in `rimsky_claim_holders` and the resolution waits for it to complete or fail. The orphan-claim reaper skips `phase='parked'` rows, so a long-parked node doesn't accidentally trigger resolution by aging.

## Code surface

- `foundation/integration/auto_terminal.go` — entire file (~150 lines).
- `foundation/integration/terminal_decision.go` — unified `ResolveClaimHandleTerminal` engine.
- `foundation/persistence/postgres/migrations/001-initial.sql:221-232` — `rimsky_claim_holders` schema.
- `foundation/persistence/claim_holders.go` — Go-side CRUD.
- `foundation/persistence/postgres/claim_handles.go:159-200` — claim-handle deletion sites.
- `foundation/integration/runner_acquire.go:706-743` — initial `rimsky_claim_holders` INSERT on acquire.
- `foundation/integration/runner_held_claims.go` — held-claim post-terminal flow.

## Prose surface

- `CLAUDE.md` "Blessed invariants" §13.
- `docs/concepts/holding-subgraph.md` — the consumer-facing concept doc.
- `docs/concepts/claim-handle.md` — "Held subgraph's resolution (all-success → Commit; any-failure → Abandon) deletes the handle claimant-guarded."
- `docs/concepts/inheritance.md` — direct-only inheritance widens the subgraph by one member.
- `.ok-planner/specs/2026-05-04-foundation-contract.md` §4.4 — terminal-verb idempotency requirement.

## Adjacent topics

- `terminal-resolution` — the end-to-end flow from executor wire to claim-handle DELETE; this entry is Stage 5 (held branch).
- `2026-05-10-claimant-guarded-release` — the delete predicate auto-terminal uses.
- `2026-05-10-out-of-process-claim-producers` — defines the verb-then-tx-fail leak.
- `2026-05-10-parked-state-and-resume` — auto-terminal still works across park.
- `2026-05-10-worker-request-phase-lifecycle` — `ON DELETE SET NULL` keeps held handles alive past parent active terminal.

## Observations

- The rule "any failed → Abandon; else Commit" treats `failed` as poison. A `discard_then_retry` policy that flips a held subgraph member from `failed` back to `active` is not in scope: once a holder is `failed`, the aggregate is `failed`. The `rimsky_claim_holders.state` CHECK constraint forbids it explicitly (`migrations/001-initial.sql:228`).
- The terminal-decision engine (`terminal_decision.go`) was extracted to be the single site for producer-verb firing across multiple call sites (auto-terminal, orphan-reaper bail path, error-policy `pass` / `error` resolutions on already-Opened claims). CLAUDE.md "Held vs. failed states" mentions the unification but the unified engine's design rationale is not externally documented beyond `terminal_decision.go`'s package comment.
- "Exactly one resolution per held claim" relies on the `SELECT … FOR UPDATE` plus the row-existence check. A producer verb that's already been fired but whose tx failed to commit would re-fire on the next pass — the idempotency-in-claim_id requirement bridges this gap from the producer side.
- `foundation/integration/runner_held_claims.go` is a sibling but distinct from `auto_terminal.go`; it handles the holding-subgraph-side bookkeeping when an inheritor reaches terminal. The two files together implement the invariant; either alone is incomplete.
