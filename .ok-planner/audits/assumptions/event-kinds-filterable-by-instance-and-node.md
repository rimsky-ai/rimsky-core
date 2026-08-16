---
assumption: event-kinds-filterable-by-instance-and-node
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every event row carries instance and node identifiers, so the feed can be narrowed to one node for any kind.

As operator debugging one node, I would take it that every event row carries instance and node identifiers, so the feed can be narrowed to one node for any kind.

## Source

published-concept — `concept:event-log` ("Indexed for lookup by node, by instance, and by kind")

## What a run would observe

filter `/v1/events` by node id and check whether `auth.*` and other instance-less kinds vanish or error.

## Measured

The experiment `assumption-event-kinds-filterable-by-instance-and-node` drove a
node to settle, terminated its instance, then grouped the whole feed by kind
and counted identifiers. Coverage is not uniform. `work_started`,
`work_completed`, `attributes_substituted` and `terminal/success` carried both
identifiers on every row. `instance_terminated` carried an instance id and no
node id. `auth.access_attempted` — nine rows in a short run — carried neither.
Narrowing the feed to one node succeeds and drops those rows without saying so:
`GET /v1/events?kind=auth.access_attempted&node_id=<node>` returned HTTP 200
with zero rows while the same kind unfiltered returned nine, and the same
filter returned the node's own `work_started` rows. Narrowing by instance
dropped them just the same. An operator debugging one node can narrow the feed
for graph-scoped kinds, but the auth family — the record of who touched the
deployment — silently leaves the view, and nothing in the response says a kind
was excluded rather than absent.
