---
experiment: claim-producer-postgres
commit: PENDING
---

# Postgres-backed claims: pick policies, atomic staging, verifier checks

## What it ran against

A Postgres container, two containers of the bundled postgres claim producer
from this tree's image over the same database, and a `rimsky-all-in-one` stack
from the same tree pointed at both through rimsky.yml. One producer is
configured `sync` with a pick policy over a seeded items table; the other is
configured `staged_async` with its verifier executor enabled, and rimsky
declares that same endpoint as an executor named `pg-verifier`. Nodes that must
write SQL run on the bundled http-node executor against a service on the host,
which executes the statement inside the database container, so the writing side
is ordinary node work rather than harness privilege.

## What was observed

The seeded pick policy handed a distinct row to each of two claimants:
`job-alpha` to the first and `job-beta` to the second, each with its row's
payload substituted into the node's dispatch, and the claim handles record
realized write semantics `sync`. A third claim against the drained policy
settled the node on `terminal/error/pg/claim_unavailable`, the class this
producer declares, and a node subscribed to `terminal/error/pg/*` ran on that
signal.

A staged-async claim on the canonical schema `analytics_production` resolved to
the address `rimsky_stg_…`, a staging schema distinct from the canonical one,
and its claim handle records `staged_async` rather than a downgrade to `sync`.
The node wrote ten rows into that staging schema. A co-holding node running on
the producer's own executor ran a `row_count_ratio` check over the staged
schema against a baseline of ten and passed. After commit the canonical schema
held the ten staged rows in place of the one it had, and the staging schema no
longer exists: the commit swapped it in.

A second staged claim wrote only two rows and a checking node ran the same
`row_count_ratio` check against a baseline of ten. The node settled on
`terminal/error/pg/verifier_check_failed/row_count_ratio`, the producer's
per-check class, and a node subscribed to `terminal/error/pg/*` ran on that
signal.

The run does not exercise the swap-failed class.
