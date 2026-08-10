---
experiment: claim-producer-scopes-conflict
commit: PENDING
---

# A producer's own overlap rule decides who may hold a claim

## What it ran against

A `rimsky-all-in-one` stack from this tree's image pointed at a claim producer
written for this experiment. That producer advertises the scopes-conflict
capability and defines overlap as "the two selectors end in the same path
segment", so two scopes can be byte-unequal and still overlap. The producer
logs every call it receives, including the scope pair of each conflict query
and the answer it gave, and serves that log over HTTP. Templates declare
selectors from an instance parameter, so one template drives every scope in the
run.

## What was observed

One instance took a durable claim on `/west/reports`; its claim handle reads
`lifetime: durable` and `state: committed`, so the scope stays occupied after
the node settles.

A second instance asked for `/east/reports` — byte-unequal to the held scope,
neither a prefix nor a suffix of it, and overlapping under the producer's rule.
The producer's log shows rimsky putting the pair to it and the producer
answering `true`, and the node settled
`terminal/error/acquire/unavailable`: the two writers could not both hold
claims.

A third instance asked for `/east/invoices`, byte-unequal and non-overlapping
under the same rule, and settled fresh with its claim.

A fan-out then asked for two sub-claims, `/inbox/a/p1` and `/inbox/b/p1`, which
are byte-unequal and overlapping under the producer's rule. The producer's log
shows rimsky putting that sub-claim pair to it on the fan-out path and the
producer answering `true`. Neither sub-claim has a claim handle and the fan-out
settled no partition: the overlapping partitions are not both held.
