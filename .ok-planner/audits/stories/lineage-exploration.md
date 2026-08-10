---
audit: lineage-exploration
artifact: story:lineage-exploration
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# Walking run lineage both ways, by claim handle, and by source or producer

Supported. One workflow run left two runs joined by an attribute substitution and
one claim split into two sub-claims, and all four traversals the story names were
taken over it. Backward: walking the consuming run reached the producing run its
input came from. Forward: walking the producing run reached the consuming run. By
claim handle: reading the parent claim returned its record with the producer's
name and its outcome, walking it forward reached both sub-claims, and walking a
sub-claim backward reached the claim it was split from. By pivot: querying by the
substituted attribute and by the upstream run each returned the consuming run's
record, and querying by the named claim producer returned its three committed
claim records while a producer that committed nothing returned none. A depth
given on a walk was honoured in the answer, and a run id with no lineage answered
404 rather than an empty walk. The chain from the producing run through the
substitution to the consuming run, and from the parent claim to its sub-claims,
is the trace of how the data flowed.
