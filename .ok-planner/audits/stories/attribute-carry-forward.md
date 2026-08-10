---
audit: attribute-carry-forward
artifact: story:attribute-carry-forward
determination: supported
compliance: noncompliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# A node's executor-written attribute is present on its next dispatch in the same run-scope, and absent in a new one

Supported. Against an all-in-one deployment driven through the control API, a
node whose executor writes a `count` attribute and reads the incoming one
dispatched three times inside a single frame and emitted 1, 2, 3 — each dispatch
saw what the previous dispatch wrote — and the node read surface then answered
with the resolved bag `{count: 3, max: 3}`. The story names three kinds of new
run-scope and two were measured: a second operator message opened a second frame
in which the same node emitted 1, 2, 3 again rather than 4, 5, 6, and a fan-out
over three partitions produced count 1 in every partition rather than a chain of
1, 2, 3 across them. The third kind, a sub-graph invocation, was not measured:
every probe placing a bundled node kind inside a delegated sub-graph left that
sub-graph's child run enqueued and never dispatched, so no probe reached a state
that settled the clause either way.

## Compliance

- The body's closing sentence states where cross-frame state travels instead of
  what a user needs; a story states the need only, so the compliant text ends at
  the benefit clause and leaves the boundary to the concept catalog.
- The parenthetical justifying that carry-forward is intra-frame explains the
  mechanism rather than the need; the compliant text drops it.
