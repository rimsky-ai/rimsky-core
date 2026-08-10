---
experiment: subscriber-lineage-receiver
commit: PENDING
---

# Run lineage reaches an external receiver as OpenLineage events

## What it ran against

A private docker network carrying a Postgres database, a `rimsky-all-in-one`
orchestrator configured onto that database, a `rimsky-subscriber-openlineage`
container configured only by environment variables, and a receiver container
running `receiver.py`, which records every POST it takes and serves the record
back on `/_events`. The template runs two nodes: one holds a claim on the
bundled filesystem claim producer, the other substitutes an attribute from it.
`run.py` builds and removes everything.

## What was observed

The receiver held nothing before the subscriber started. The subscriber came up
on environment configuration alone. One workflow run produced four deliveries to
the receiver's `/api/v1/lineage` route: one per graph node, one for the message
that woke the graph, and one for the claim the producing node committed.

Every delivery is a run event carrying an event type, an event time, a producer
URI, a schema URL, a run id and a job name. Every job carries the namespace the
operator configured, and every request carries the configured bearer credential.
The two node events carry rimsky run facets naming the frame, and the consuming
node's event carries the substitution reference naming the upstream run its
input came from, so the edge between the runs travels with the event. The claim
event names the committed claim as an output dataset in the claim producer's
namespace, and the producing node's event names the same claim producer as an
input dataset.

Restarting the subscriber container and running a second workflow added four
more deliveries. The receiver's record after the restart still begins with the
same four deliveries in the same order and contains no run id and job name
twice, so the restarted subscriber resumed at its cursor rather than replaying.
