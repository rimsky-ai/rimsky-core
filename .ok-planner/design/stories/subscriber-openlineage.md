---
story: subscriber-openlineage
status: as-is
---

# Operator emits OpenLineage to data-platform

## Role

As an operator running rimsky in a data-platform environment, I can use the bundled `openlineage` subscriber to translate rimsky lifecycle events and claim terminal records into OpenLineage 1.x JSON events posted to a backend (Marquez / DataHub / Collibra / etc.), so that rimsky's run DAG and data lineage surface in my governance platform without writing a custom subscriber.

## Capability

Bundled `openlineage` subscriber: translates rimsky lifecycle events + claim terminal records into OpenLineage 1.x JSON; POSTs them to a configured receiver.

## Business value

Operators see rimsky's run DAG and data lineage in their governance platform without writing a custom subscriber.

## Acceptance

A running `openlineage` subscriber configured to post to a real OpenLineage receiver: when a rimsky template is deployed, the subscriber emits a dataset-version event; when a run-scope reaches terminal, the subscriber emits a job-run event; claim terminal records translate into lineage events; the receiver actually receives well-formed OpenLineage 1.x JSON.

## Falsifier

Subscriber posts to receiver but with malformed OpenLineage JSON, OR a lifecycle event the subscriber should emit on is skipped, OR the emitted event's IDs don't correspond to the rimsky-side IDs.

## Proof

Executable proof.
