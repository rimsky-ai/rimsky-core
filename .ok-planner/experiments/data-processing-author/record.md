---
experiment: data-processing-author
commit: PENDING
---

# The typed-data mix-in, as written and as driven

## What it ran against

`peer/` is a third-party claim producer carrying the typed-data mix-in: its own
Go module whose only rimsky requirement is the protocols module, built the way
the permissive-peer-build experiment's executor is. It serves the claim-producer
protocol including SplitScope, the data-processing protocol's seven verbs, and
an executor so the fan-out children have something to dispatch to. The run has
two halves against two separate peer processes, so the conformance kits' own
calls never enter the counts the second half reads: the shipped `rimsky
conformance` verbs against one, and a `rimsky-all-in-one` stack from the tree's
own image tag against the other, whose config declares `protocols:
["claim_producer", "data_processing"]` on a single entry.

## What was observed

The protocol as written: `rimsky conformance data-processing` passed all ten of
its checks against the peer — capabilities, begin-then-commit per
materialization, begin idempotency, the three abandon checks, the list-versions,
list-partitions and get-version-schema smokes, and concurrent writes — and
`rimsky conformance claim-producer` passed its suite too.

The protocol as driven: two fan-out nodes over the same producer made rimsky
call SplitScope twice and stage one candidate per partition — five
BeginCandidate calls for a three-way and a two-way split, with the partition
keys the producer itself had handed back. When the successful fan-out's children
settled, its three candidates were committed; when the failing fan-out's children
errored, its two candidates were abandoned. Nothing was left staged, and exactly
three versions existed afterwards, one per committed partition, keyed
`part-0`, `part-1`, `part-2`.

The version history is reached through the producer's own listing surface: the
fan-out's claim appears as an asset, and reading that asset's versions through
the control API — and through `rimsky asset versions` — called the producer's own
ListVersions verb. What that call returned here was empty, because this peer
records versions against the sub-claim handles rather than the parent the asset
route names; that is this peer's data model, not something the route decides.

Seventeen checks, none failing.
