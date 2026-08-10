---
audit: claim-producer-scopes-conflict
artifact: story:claim-producer-scopes-conflict
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:45:00Z
---

# The producer's own overlap rule decides who may hold a claim

Supported. A producer written against the published protocol advertised its own
overlap rule — two selectors overlap when they end in the same path segment —
and a stack was pointed at it. One instance took and kept a durable claim on
`/west/reports`. A second instance asked for `/east/reports`, which is
byte-unequal to the held scope and overlapping under that rule: the producer's
log shows rimsky putting the pair to it and the producer answering that they
overlap, and the node settled on an acquisition failure, so the two writers
could not both hold claims. A third instance asked for `/east/invoices`,
byte-unequal and non-overlapping, and settled fresh with its claim. A fan-out
then asked for two sub-claims that are byte-unequal and overlapping under the
same rule: rimsky put that pair to the producer on the fan-out path, the
producer answered that they overlap, neither sub-claim has a claim handle, and
no partition settled.

## Compliance

The body prescribes mechanism in two places: it names the capability
advertisement a producer must make, and it names an internal code path ("the
fan-out sub-claim path"). Compliant text: "As an operator running templates
whose claims overlap non-trivially, I can have my claim producer decide what
counts as overlap, and trust that two writers whose scopes overlap by that
decision never hold claims at the same time — including when one of them is a
partition of a larger claim — so that the no-overlapping-writers rule holds for
my own definition of overlap and not only for identical scopes."
