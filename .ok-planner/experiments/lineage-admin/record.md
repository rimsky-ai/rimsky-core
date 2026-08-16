---
experiment: lineage-admin
commit: PENDING
---

# Pruning lineage records by a cutoff timestamp

## What it ran against

A `rimsky-all-in-one` container from this tree's own image tag, configured with
the bundled filesystem claim producer and with short trace and claim-handle
retention so the run tree ages out within seconds. The template runs two nodes:
one holds a claim, the other substitutes an attribute from it. Pruning is driven
by `rimsky lineage prune` from the repository's own CLI binary, and the malformed
inputs are posted to the prune route directly. `run.py` builds and removes the
container.

## What was observed

Ten checks, none failing. One workflow run left lineage records readable by run
id, by producing claim producer, and by substitution source. The probe then
waits for the retention sweep to empty the instance's frames while those lineage
records remain — the state a long-lived deployment reaches, and the state in
which the operator's prune has something to remove.

Pruning with a cutoff an hour older than every record deleted nothing, and every
record was still readable afterwards. Pruning with a cutoff an hour newer
deleted four rows, at least as many as were readable, and afterwards the run id
answered 404 and both the by-producer and by-source reads answered empty. A
second workflow run recorded lineage again, and `--older-than` accepted an age in
place of a timestamp and deleted those rows too. A cutoff that is not a timestamp
and a prune with no cutoff were each refused 400, the first naming the timestamp
format it wanted and the second naming the missing field.

The probe prunes only after the frames have emptied; this run took no separate
measurement of a prune issued before that point.
