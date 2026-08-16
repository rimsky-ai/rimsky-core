---
issue: dispatcher-claims-by-enqueue-time-not-sequence
kind: audit
category: conflicting
artifacts:
  - concept:wait-set
  - concept:cascade-mode
  - decision:non-cascade-direct-to-stale
status: verified
opened: 2026-08-16T09:05:08Z
---

# The dispatcher claims stale rows by enqueue time, not by the sequence three artifacts promise

Three artifacts — the wait-set concept (twice), the non-cascade-direct-to-stale decision, and the cascade-mode concept's sequenced row — say the dispatcher claims stale rows in sequence order (the per-scope monotonic number a decision exists to make deterministic). Both backends order candidates by enqueue timestamp then row id and never read the sequence column. The tie is reachable: two unclaimed stale rows for the same node and scope can coexist (the child-creation guard excludes running, held and parked siblings, not stale ones), and two rows written in one transaction share a timestamp, so the tiebreak falls to a random id. The ruling makes the query honour the promise.

## Options

- Add sequence to the candidate ordering as the tiebreaker (and to the keyset cursor) in both backends; cost: none beyond the change.
- Narrow all three artifacts to enqueue-time order; cost: a real regression of the sequenced-rounds guarantee in the reachable tie case.
- Assert sequence and enqueue order cannot disagree; cost: false — they demonstrably can.

The ruling makes claim order deterministic where the corpus already says it is.

## Ruling

> Generated ruling (/verify-issues): Order candidate selection by enqueue time then sequence (then row id), in both backends, and carry sequence in the paging cursor, so same-timestamp rows claim in creation order. Forced by three agreeing artifacts and the sequence-monotonic decision that exists for exactly this tiebreak; the code is the only outlier. Verified against the tree as it stands; nothing was applied.

<!-- Owner: this is a generated ruling, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
