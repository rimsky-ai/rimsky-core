---
topic: atomic-acquisition-decoupled-tx
kind: invariant
---

# Acquisition transaction is rimsky-side atomic; ClaimProducer.Open runs in its own decoupled transaction

## Description

The supervisor's acquisition flow has to do three things atomically from rimsky's bookkeeping perspective: claim the `rimsky_worker_request` row, INSERT one `rimsky_claim_handle` row per declared lock or scope, and record the address bytes the producer returns from `ClaimProducer.Open`. The producer in turn does its own state mutation (filesystem staging directory, postgres items-table flip, MVCC snapshot transaction) in its own transaction.

`@blessed-invariant 10` (annotated at `foundation/integration/runner.go:41-51` and `foundation/integration/supervisor.go:40-55`) defines the rule: rimsky opens one transaction that wraps (a) the dispatch-row claim, (b) all claim-handle row INSERTs, (c) the in-tx invocation of `ClaimProducer.Open` over the wire, (d) writing the producer-supplied address back into the just-inserted claim-handle row, and (e) inserting the holding-subgraph `rimsky_claim_holders` rows. Either rimsky's tx commits as a whole or it rolls back as a whole. The producer's own transaction is decoupled — if rimsky rolls back after a successful `Open`, the producer's state is recovered by its own TTL/sweep, not by a rimsky-issued `Abandon`.

This is a deliberate v3 departure from the older v2 model (`locks.WithTx` + `TxFromContext`) where the producer was an in-process implementation that shared the same `*pgx.Tx` as rimsky. The current model is incompatible with that by construction: producers run as separate gRPC peers (`2026-05-10-out-of-process-claim-producers`), and depguard's `pgx-isolation` rule (`.golangci.yml:14-30`) forbids any package outside the allow-list from holding a `pgx.Tx` at all. Cross-process tx sharing isn't a sound primitive over gRPC anyway.

The single-writer-per-scope guarantee (cited in CLAUDE.md as invariant 4b) still holds: rimsky's conflict predicate (`evaluateScopeConflict` at `foundation/integration/runner_acquire.go:670-705`) gates new claim-handle INSERTs against `rimsky_claim_handle` only. Producer-side orphan state is invisible to the conflict predicate, so a producer's leftover staging directory after a rimsky rollback does not cause a phantom conflict on the next acquisition.

The acquisition tx walks the per-spec lock list in deterministic `(lock_kind, sort_key)` order (`@blessed-invariant 3` at `foundation/integration/runner.go:20-29`); the per-scope advisory lock (`TakeScopeLockInTx`) is what closes the READ COMMITTED window between conflict-check and claim-handle INSERT.

## Code surface

- `foundation/integration/runner.go:41-51` — annotated invariant block.
- `foundation/integration/supervisor.go:40-65` — companion annotation.
- `foundation/integration/runner_acquire.go:228-330` — `acquireAllLocks` body.
- `foundation/integration/runner_acquire.go:538-557` — per-scope advisory lock.
- `foundation/integration/runner_acquire.go:670-705` — `evaluateScopeConflict`.
- `foundation/integration/auto_terminal.go:43-50` — comment on the verb-then-tx-fail leak path.
- `protocols/proto/v1/claim_producer.proto` — `OpenRequest`/`OpenResponse` shape, including `claim_id` (the rimsky-generated UUID that bridges the decoupled tx pair).

## Prose surface

- `CLAUDE.md` "Blessed invariants" §10 — atomic acquisition vs decoupled tx.
- `CLAUDE.md` "Non-obvious gotchas" — "Atomicity is decoupled."
- `.ok-planner/specs/2026-05-04-foundation-contract.md` §4 — acquisition-transaction contract.
- `.ok-planner/specs/2026-05-04-service-protocol-contract.md` — claim_id correlation.
- `docs/concepts/claim-producer.md` — five-method protocol; `Open` is invoked over the wire.

## Adjacent topics

- `2026-05-10-out-of-process-claim-producers` — the topology that forces decoupling.
- `2026-05-10-advisory-locks-tick-and-migrate` — the per-scope advisory lock is one half of the atomicity story.
- `2026-05-10-claimant-guarded-release` — release predicates assume the row was successfully INSERTed by an acquisition tx.
- `2026-05-10-verify-before-run-guard` — the post-commit read that catches the cross-tx handoff race.
- `2026-05-10-orphan-reaper-no-producer-abandon` — handles the producer-state-without-rimsky-row leak.

## Observations

- The verb-then-tx-fail leak (producer's `Commit`/`Abandon` succeeds, rimsky's containing tx fails) is acknowledged inline at `foundation/integration/auto_terminal.go:43-50` and mitigated by requiring terminal verbs to be idempotent in `claim_id`. There is no automated detection of this leak; it relies on producer cooperation.
- `Open` happens **inside** rimsky's acquisition tx (per @blessed-invariant 15 at the proto comment); this means the in-tx HTTP/gRPC call holds a row-level lock on the `rimsky_worker_request` row for the duration of the producer's response. A slow producer therefore widens the contention window — visible at `foundation/integration/runner.go:46-51` as a deliberate choice.
- The legacy `locks.WithTx` / `TxFromContext` names are no longer in the source tree (`grep -r WithTx foundation modeling` returns no hits), but the migration comment is preserved in `runner.go:41-51`. The historical mode is described in `.ok-planner/archive/` (per CLAUDE.md "Where to look first") for context only.
