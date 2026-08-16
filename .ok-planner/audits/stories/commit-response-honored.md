---
audit: commit-response-honored
artifact: story:commit-response-honored
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:55:00Z
---

# The base Commit response's version id and producer metadata land where the contract says

Supported. A producer speaking only the base protocol — advertising no
data-processing capability — returned the same version id and metadata blob on
every commit. The claim handle for an ordinary claim carries that version id and
reads committed, and each of the three sub-claim handles from a fan-out carries
it too. On the writeback side the fan-out parent carries the metadata blob keyed
by partition key, and the values a downstream node read back are the exact bytes
the producer returned. The run reads the parent through a node dispatched by the
writeback's own change signal, and no dispatch follows the last partition's
commit, so what it establishes is that a partition's commit metadata reaches the
parent's writeback under that partition's key — two partitions were visible at
the reader's last dispatch — rather than how many entries the row holds at rest.
