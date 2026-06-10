---
story: store-postgres
status: as-is
---

# Operator uses postgres-backed staged-async store

## Role

As an operator wiring a workflow whose claims persist in PostgreSQL, I can use the bundled `store-postgres` claim-producer to acquire row-locking claims via configurable pick policies, opt into atomic staging (staging schema swap at Commit), declare verifier checks including `row_count_ratio` over aggregate-only queries, and subscribe to declared error classes (`pg/claim_unavailable`, `pg/swap_failed`), so that I have a postgres-backed store that delivers staged-async semantically rather than as a no-op.

## Capability

Bundled `store-postgres` claim-producer: row-locking claims with configurable pick policies; atomic staging-schema swap at Commit; `row_count_ratio` verifier check on aggregate-only queries; declared error classes (`pg/claim_unavailable`, `pg/swap_failed`, `pg/verifier_check_failed/*`).

## Business value

Operators get a postgres-backed store delivering staged-async semantically — not as a no-op — with declared error classes that route through error-policy and verifier checks that catch out-of-bounds outputs.

## Acceptance

A template referencing `store-postgres`: `Open` with staged write-semantics creates/reserves a staging schema queryable through the store's observability; the executor writes rows to staging; `Commit` performs an atomic schema swap; a swap collision emits `pg/swap_failed` routable through `error_types`; a verifier check declaring `row_count_ratio` with bounds compiles and executes as an aggregate-only query, surfacing `pg/verifier_check_failed/row_count_ratio` on out-of-bounds; an empty pick policy queue emits `pg/claim_unavailable`.

## Falsifier

Atomic-staging schema is created but Commit doesn't atomically swap, OR `row_count_ratio` runs a non-aggregate query, OR `pg/swap_failed` is emitted as a generic error class, OR `pg/claim_unavailable` doesn't fire on a real empty-queue Open.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
