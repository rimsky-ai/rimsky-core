---
assessment: commit-response-honored--metadata-on-fan-out-writeback
subject: story:commit-response-honored
way: metadata-on-fan-out-writeback
release: d977250c
outcome: held
warrant: experiment:commit-response-honored
---
# Producer metadata reaches the fan-out parent's writeback under the partition's key

The fan-out parent's writeback carried the producer's metadata blob keyed by partition key, and the bytes a downstream node read back are the exact bytes the producer had returned — passed through, not re-encoded or summarised. A template author can therefore route a producer's own commit metadata to a downstream node without the producer implementing anything beyond the base protocol.

## Unverified remainder

The reader is dispatched by the writeback's own change signal and no dispatch follows the last partition's commit, so the run establishes that a partition's commit metadata reaches the parent's writeback under that partition's key — two partitions were visible at the reader's last dispatch — rather than how many entries the writeback holds at rest.
