---
audit: subscriber-lineage-receiver
artifact: story:subscriber-lineage-receiver
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:25:00Z
---

# Run lineage reaches an external receiver as OpenLineage events

Supported. The bundled lineage subscriber came up on environment configuration
alone against a Postgres-backed deployment and delivered to a receiver that
records what it takes; no subscriber code was written in the run. One workflow
produced four deliveries, all to the receiver's OpenLineage route: one per graph
node, one for the message that woke the graph, and one for the claim the
producing node committed. All four are run events carrying an event type, an
event time, a producer URI, a schema URL, a run id and a job name; all four carry
the configured namespace and the configured bearer credential. The run DAG and
the data lineage both travel: the node events carry rimsky run facets naming
their frame, the consuming node's event carries the substitution reference naming
the upstream run its input came from, the committed claim appears as an output
dataset in the claim producer's namespace, and the held claim appears as an input
dataset on the producing node. Restarting the subscriber and running a second
workflow added four more deliveries with no run id and job name repeated, so the
restart resumed at the cursor. One limit the story does not state: the subscriber
refuses to start against anything but a Postgres DSN, so a SQLite-backed
deployment has no export path.
