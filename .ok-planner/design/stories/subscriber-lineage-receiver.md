---
story: subscriber-lineage-receiver
status: as-is
---

# Operator emits lineage events to an external receiver

## Role

As an operator running rimsky in a data-platform environment, I can use the bundled lineage-receiver subscriber — a Postgres poller over the `concept:lineage` projection (`leaf_run` and `claim_terminal` records only; it implements no `LifecycleSubscriber` RPC and runs no gRPC server) — to translate those two record kinds into a lineage-event JSON payload posted to an external lineage receiver, so that rimsky's run DAG and data lineage surface in my governance platform without writing a custom subscriber.

## Capability

Bundled lineage-receiver subscriber: polls the `rimsky_lineage` table (Postgres-backed rimsky deployments only) and translates `leaf_run` and `claim_terminal` records into a lineage-event JSON payload; posts them to a configured external lineage receiver. It does not subscribe to template-deploy or other lifecycle events — there is no dataset-version-on-deploy emission, and none is planned; `concept:lineage` documents exactly these two record kinds as the projection's full surface.

## Business value

Operators see rimsky's run DAG and data lineage in their governance platform without writing a custom subscriber.

## Acceptance

A running lineage-receiver subscriber configured to post to a real external lineage receiver, pointed at a Postgres-backed rimsky deployment: when a leaf-run record is written (leaf-run terminal), the subscriber emits a job-run event; claim-terminal records translate into lineage events; the receiver actually receives well-formed lineage-event JSON in the external-receiver wire format.

## Falsifier

Subscriber posts to receiver but with malformed lineage-event JSON, OR a `leaf_run` or `claim_terminal` record the subscriber should emit on is skipped, OR the emitted event's IDs don't correspond to the rimsky-side IDs, OR the subscriber implements a `LifecycleSubscriber` RPC it does not actually serve.

## Proof

Executable proof.
