---
audit: data-processing-author
artifact: story:data-processing-author
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# The typed-data mix-in is implementable and rimsky drives its whole lifecycle

Supported. A third-party claim producer built against the protocols module alone
advertised data_processing beside claim_producer on one config entry, and both
halves of the story held. As written, the shipped conformance verbs drove every
verb the story names and passed: all ten data-processing checks — capabilities,
begin-then-commit per materialization, begin idempotency, the three abandon
behaviours, and the list-versions, list-partitions and get-version-schema
smokes — plus the claim-producer suite. As driven, two fan-out nodes made rimsky
split the claim twice and stage one candidate per partition, five in all across
a three-way and a two-way split, keyed by the partition keys the producer itself
returned; the successful fan-out's three candidates were committed, the failing
fan-out's two were abandoned, none was left staged, and exactly three versions
remained, one per committed partition. Reading the fan-out's claim as an asset
routed to the producer's own ListVersions verb, through both the control API and
the CLI.
