---
decision: write-semantics-reader-lease-forbidden
---

# Staged-asynchronous support requires snapshots, not reader leases

## Choice

A producer advertising the staged-asynchronous write-semantics value serves readers a point-in-time snapshot, either by staging writes and serving the pre-stage snapshot or by passing through the substrate's own multi-version reads. Serializing readers behind an internal lease is not support for that value. A producer that stages writes but cannot offer readers a snapshot advertises the blocking-asynchronous value instead (see `concept:write-semantics`).

## Rationale

The staged-asynchronous value tells rimsky's coexistence matrix that a reader and a writer may hold the same scope at once, and rimsky acts on that by dispatching both runs. A producer that serializes readers internally still answers the coexistence question with yes, so the reader blocks inside the producer where rimsky observes nothing. The result is a stalled run with no conflict recorded and no error policy consulted. The blocking-asynchronous value exists for exactly that producer and reports its blocking to the matrix.

## Alternatives

- Admit reader-lease serialization as staged-asynchronous support — rejected: the matrix says the two runs coexist while the producer blocks one of them, so the wait is invisible to rimsky and no policy applies to it.
