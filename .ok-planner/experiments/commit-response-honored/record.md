---
experiment: commit-response-honored
commit: PENDING
---

# The base Commit response's version id and producer metadata

## What it ran against

A `rimsky-all-in-one` stack from this tree's image pointed at a claim producer
written for this experiment against the base claim-producer gRPC protocol. That
producer returns the same two fields on every Commit: version id `v-42` and
producer metadata `{"rows":7}`. It advertises no data-processing protocol, so
everything observed comes from the base protocol alone. One template takes an
ordinary claim. A second declares a fan-out into three partitions run one at a
time, and a downstream node reads the fan-out parent's writeback by attribute
reference and posts it to a recorder on the host. Re-run unchanged at this tree.

## What was observed

The claim handle for the ordinary claim carries `version_id: "v-42"`, the value
the producer returned on Commit, and reads `committed`. Each of the three
sub-claim handles carries the same version id.

The fan-out parent's writeback carries the producer-metadata blob keyed by
partition key: the reader received `{"p1": "eyJyb3dzIjo3fQ==", "p2":
"eyJyb3dzIjo3fQ=="}`, whose values are the base64 of the exact bytes the
producer returned.

The run does not observe the third partition's entry. The reader is dispatched
by the writeback's own change signal, and no dispatch follows the last
partition's commit, so the reader's last snapshot predates that write. What the
run establishes is that a partition's Commit metadata reaches the parent's
writeback under that partition's key; how many entries the row holds at rest is
outside what this instrument can see.
