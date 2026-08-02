---
story: claim-producer-postgres
---

# Operator uses postgres-backed staged-async claim-producer

## Story

As an operator wiring a workflow whose claims persist in PostgreSQL, I can use the bundled postgres claim-producer to acquire row-locking claims via configurable pick policies, opt into atomic staging (staging schema swap at commit), declare verifier checks including a row-count-ratio check over aggregate-only queries, and subscribe to declared error classes (postgres-side claim-unavailable, swap-failed, and per-check verifier-failed classes), so that I have a postgres-backed claim-producer that delivers staged-async semantically rather than as a no-op.
