---
audit: claim-producer-postgres
artifact: story:claim-producer-postgres
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# Postgres-backed claims that stage for real and check what was staged

Supported. A Postgres database, two containers of the bundled postgres claim
producer over it, and a stack pointed at both. The pick policy handed a distinct
row to each of two claimants, each with its row's payload substituted into the
node's dispatch, and a third claim against the drained policy settled the node
on the producer's own claim-unavailable class, with a node subscribed to that
producer's error classes running on the signal. A staged-async claim on a
canonical schema resolved to a separate staging schema, its claim handle
recorded staged-async rather than a downgrade to synchronous, the node wrote ten
rows into the staging schema, a check declared as a row-count ratio against a
baseline of ten ran over that staged content and passed, and after commit the
canonical schema held the ten staged rows in place of the one it had while the
staging schema no longer exists. A second staged claim wrote two rows against
the same baseline: the checking node settled on the producer's per-check
error class and a node subscribed to that producer's error classes ran on it.
Two of the three error-class families the story names were driven end to end;
the swap-failed family was not.

## Compliance

The body prescribes mechanism — the staging schema swap, the aggregate-only
shape of the verifier's queries, and the three error-class families by name —
and its closing clause states a property of the implementation ("delivers
staged_async semantically rather than as a no-op") rather than a user need.
Compliant text: "As an operator wiring a workflow whose claims persist in
PostgreSQL, I can use the bundled postgres claim producer to hand each claimant
its own row, to write a whole new version of a dataset that only becomes visible
once it is complete, to declare checks that must pass before it does, and to
route on the failures the producer reports, so that my postgres workflows get
the same claim guarantees as any other backend."
