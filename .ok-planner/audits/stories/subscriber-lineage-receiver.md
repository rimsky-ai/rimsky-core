---
audit: subscriber-lineage-receiver
artifact: story:subscriber-lineage-receiver
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:55:31Z
---

# The bundled lineage subscriber delivers run lineage to an external receiver

Supported. Driven through the public surface on a private network carrying a
Postgres database, a released-image orchestrator, the released OpenLineage
subscriber configured only by environment variables, and a receiver that records
every delivery it takes. Eleven checks, none failing. The receiver held nothing
before the subscriber started, and one workflow run produced four deliveries:
one per graph node, one for the message that woke the graph, and one for the
claim the producing node committed. Every delivery was a well-formed run event
with event type, event time, producer URI, schema URL, run id and job name,
carried the namespace the operator configured, and arrived with the configured
bearer credential. The run DAG and the data lineage both surfaced: the node
events carry rimsky run facets naming the frame, the consuming node's event
carries the substitution reference naming the upstream run its input came from,
the committed claim appears as an output dataset in the producer's namespace and
the same producer as an input dataset on the producing node's event. Restarting
the subscriber and running a second workflow added four more deliveries, eight
distinct in total, so a restart resumes at the cursor rather than replaying —
and nothing in the run required writing a subscriber.
