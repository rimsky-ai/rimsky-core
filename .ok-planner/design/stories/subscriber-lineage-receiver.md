---
story: subscriber-lineage-receiver
status: as-is
---

# Operator emits lineage events to an external receiver

## Role

As an operator running rimsky in a data-platform environment, I can use the bundled lineage-receiver subscriber to translate rimsky lifecycle events and claim terminal records into a lineage-event JSON payload posted to an external lineage receiver, so that rimsky's run DAG and data lineage surface in my governance platform without writing a custom subscriber.

## Capability

Bundled lineage-receiver subscriber: translates rimsky lifecycle events plus claim terminal records into a lineage-event JSON payload; posts them to a configured external lineage receiver.

## Business value

Operators see rimsky's run DAG and data lineage in their governance platform without writing a custom subscriber.

## Acceptance

A running lineage-receiver subscriber configured to post to a real external lineage receiver: when a rimsky template is deployed, the subscriber emits a dataset-version event; when a run-scope reaches terminal, the subscriber emits a job-run event; claim terminal records translate into lineage events; the receiver actually receives well-formed lineage-event JSON in the external-receiver wire format.

## Falsifier

Subscriber posts to receiver but with malformed lineage-event JSON, OR a lifecycle event the subscriber should emit on is skipped, OR the emitted event's IDs don't correspond to the rimsky-side IDs.

## Proof

Executable proof.
