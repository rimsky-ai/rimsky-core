---
audit: data-processing-author
artifact: story:data-processing-author
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:26:27Z
---

# A third-party producer carrying the typed-data mix-in, proved and then driven

Supported. A claim producer written for this audit as its own Go module,
depending on the published protocols module alone, advertises the data-processing
protocol alongside the claim-producer protocol, and all three ways the story
names were taken against a stack and CLI from this tree. As written: the shipped
conformance verb for data-processing passed all ten of its checks — capabilities,
begin-then-commit, begin idempotency, the three abandon checks, the three listing
smokes and concurrent writes — and the claim-producer conformance verb passed
alongside it. As driven: two fan-out nodes over that producer made rimsky split
twice and open one staging candidate per partition, five in all, keyed by the
partition names the producer itself returned; the fan-out whose children settled
had its three candidates committed, the fan-out whose children errored had its
two abandoned, nothing was left staged, and exactly three versions existed
afterwards, one per committed partition. Reading version history goes through the
author's own listing surface: the fan-out's claim appears as an asset, and both
the control API and the CLI asset-versions verb call the producer's own
list-versions verb, so what a reader gets back is whatever the author's data
model holds.

## Compliance

- The benefit clause restates the capability rather than saying why the author
  wants it: after a capability clause that already names per-partition staging,
  finalization, garbage collection and version-history surfacing, "so that I
  support typed-data version lifecycle with partition-aware staging" adds nothing.
  The compliant text names what the author gains — that readers of their store
  see a partition's data only once it is complete, and a failed write leaves
  nothing behind.
