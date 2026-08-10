---
experiment: idempotent-mode-dedupes
commit: PENDING
---

# Re-runs with identical inputs never reach the executor under an idempotent mode

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` and drives
it through the control API. A sender node cascades to itself four times. A
receiver subscribes to every attribute change the sender emits, so it is
cascaded four times in each run. Two receiver variants differ only in what the
receiver reads: a sender attribute that never changes (so all four rounds
resolve to a byte-identical bag) or one that changes every round (so all four
differ). Each variant is run under `sequenced`, `idempotent-queue` and
`idempotent-settled`. The count of `work_started` events on the receiver is the
number of times its executor was reached.

## What was observed

With identical inputs and no idempotent mode, all four cascade rounds reached
the executor, each carrying the bag `{snapshot: 4}`. Under `idempotent-queue`
and again under `idempotent-settled`, the same four rounds produced exactly one
dispatch: the three re-runs whose inputs equalled a predecessor were dropped
before the executor.

With inputs that differ each round, both idempotent modes dispatched all four
rounds, and the four dispatches carried four distinct bags. The drop therefore
follows from the inputs being equal, not from cascade rounds being coalesced.

Eight checks, none failing.
