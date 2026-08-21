---
concept: advisory-lock
---

# Advisory lock

## What it is

An advisory lock is a named exclusion one rimsky process takes so that no other process enters the same section of work at the same time. Rimsky uses five of them: a scheduler-tick lock, a migration lock, a per-name lock, a per-claim-scope lock, and a per-lifecycle-scope lock. The last three are transaction-scoped — the exclusion lasts as long as the transaction that took it. A process holds the first two for the whole span of the work they guard. The client-server persistence backend takes all five as the database's own advisory locks. The embedded file backend takes the scheduler-tick and migration locks as file locks placed beside the database file, which excludes every process sharing that file on one host; its three transaction-scoped locks are no-ops, because the immediate-mode transaction's writer hold already closes the same window across processes (see `decision:sqlite-multiproc-safety`).

## Purpose

Advisory locks give rimsky cross-process coordination out of the persistence backend it already runs, with no separate coordination service (see `decision:advisory-locks`). The scheduler-tick lock makes the periodic tick safe to run on several replicas, because only the replica holding it sweeps. The migration lock serializes migration runs. The per-name and per-claim-scope locks close the race window the acquisition transaction's isolation level leaves open. The per-lifecycle-scope lock serializes the lifecycle fan-out's check-deliver-mark section across replicas, so racing fan-outs for one scope converge on a single delivery.

## Boundaries

Advisory locks own the five primitives, the two long-lived pinned keys the scheduler-tick and migration locks take, and the difference between an exclusion scoped to a session and one scoped to a transaction. They do not own the conflict matrix that decides which claim intents coexist, the cutoffs that decide when a sweep acts, or the claim-handle ledger. The scheduler-tick lock guards the sweep that drives queued work forward, the migration lock guards the persistence database's schema migration, and the per-claim-scope lock guards the supervisor's claim-acquisition transaction.

see also: `claim-handle`, `message`, `persistence-database`, `supervisor`
