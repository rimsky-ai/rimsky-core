---
story: store-postgres
status: as-is
---

# Operator uses postgres-backed staged-async store

## Role

As an operator wiring a workflow whose claims persist in PostgreSQL, I can use the bundled postgres store to acquire row-locking claims via configurable pick policies, opt into atomic staging (staging schema swap at commit), declare verifier checks including a row-count-ratio check over aggregate-only queries, and subscribe to declared error classes (postgres-side claim-unavailable, swap-failed, and per-check verifier-failed classes), so that I have a postgres-backed store that delivers staged-async semantically rather than as a no-op.

## Capability

The bundled postgres store (see `concept:claim-producer`): row-locking claims with configurable pick policies; atomic staging-schema swap at commit; row-count-ratio verifier check on aggregate-only queries; declared error classes covering postgres-side claim-unavailable, swap-failed, and per-check verifier-failed conditions.

## Business value

Operators get a postgres-backed store delivering staged-async semantically — not as a no-op — with declared error classes that route through error-policy and verifier checks that catch out-of-bounds outputs.

## Acceptance

A template referencing the bundled postgres store: an open call with staged write-semantics creates or reserves a staging schema queryable through the store's observability; the executor writes rows to staging; a commit call performs an atomic schema swap; a swap collision emits a swap-failed error class routable through the error-type policy; a verifier check declaring row-count-ratio with bounds compiles and executes as an aggregate-only query, surfacing the per-check verifier-failed error class on out-of-bounds; an empty pick policy queue emits a claim-unavailable error class.

## Falsifier

Atomic-staging schema is created but commit doesn't atomically swap, OR the row-count-ratio check runs a non-aggregate query, OR the swap-failed condition surfaces as a generic error class, OR the claim-unavailable condition doesn't fire on a real empty-queue open.

## Proof

Executable proof.
