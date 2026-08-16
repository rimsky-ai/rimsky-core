---
audit: claim-producer-scopes-conflict
artifact: story:claim-producer-scopes-conflict
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:55:00Z
---

# The producer's own overlap rule decides who may hold a claim, on the ordinary path and the fan-out path

Supported. A producer advertising the overlap capability defined overlap as two
selectors ending in the same path segment, so scopes can be byte-unequal and
still overlap, and it logged every conflict query put to it with the answer it
gave. One instance took a durable claim whose handle reads durable and
committed, so the scope stayed occupied after its node settled. A second
instance asked for a byte-unequal scope that is neither a prefix nor a suffix of
the held one but overlaps under the producer's rule: the log shows the pair
being put to the producer and the producer answering that they overlap, and the
node was refused at acquisition, so the two writers could not both hold claims.
A third instance asked for a byte-unequal, non-overlapping scope and got its
claim straight away, so the rule discriminates rather than refusing everything.
The same holds on the fan-out sub-claim path: a fan-out asking for two
byte-unequal but overlapping sub-claims had that pair put to the producer, which
called them overlapping, and neither sub-claim ended up with a handle and the
fan-out settled neither partition.
