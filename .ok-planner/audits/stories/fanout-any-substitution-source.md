---
audit: fanout-any-substitution-source
artifact: story:fanout-any-substitution-source
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:24:46Z
checked: 5
unaccounted: 0
---

# A fan-out partition request resolves from every source kind it can take

Supported: a run through the control API of an all-in-one deployment, with the
bundled filesystem claim producer over a throwaway workspace, registered the same
fan-out node five times differing only in where its partition request reads from.
Each run partitioned exactly as its source named, with no resolution error
anywhere: three partitions from an upstream node's attribute, two from an
instance param, two from the claim's own payload interpolated into the keys, three
from a typed message body, and two from a host-environment variable. In every case
the number of work units reporting a partition key equalled the number of
partitions the source named. The story names four sources; the substitution
grammar carries six source kinds, of which five are testable here — the sixth, the
per-child partition identifier, is the fan-out's own output and cannot be an input
to the request that creates the partitions. Ten checks, none failing.
