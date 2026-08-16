---
audit: lineage-exploration
artifact: story:lineage-exploration
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:48:35Z
---

# An operator walks lineage both directions and pivots by claim, source and producer

Supported. Driven through the public surface against a container of the released
all-in-one image wired to the bundled filesystem claim producer, on one workflow
whose producing node holds a claim and fans out over two partitions while its
consuming node substitutes an attribute from it — so one run leaves a claim
split into sub-claims and two runs joined by a substitution. Fourteen checks,
none failing. All four pivots the story names answered: a run's own record read
by run id; the walk backward from the consumer reaching the producer and the
walk forward from the producer reaching the consumer, with a caller-given depth
honoured; the claim-handle read returning the producer's name and outcome, its
forward walk reaching both sub-claims and a sub-claim's backward walk reaching
the claim it was split from; and the pivots by substitution source and by named
producer, the latter returning that producer's three committed claims while a
producer that committed nothing returned none. A run id with no lineage answered
not-found rather than an empty walk.
