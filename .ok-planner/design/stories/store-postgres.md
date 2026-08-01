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

