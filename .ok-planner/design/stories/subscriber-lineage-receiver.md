---
story: subscriber-lineage-receiver
status: as-is
---

# Operator emits lineage events to an external receiver

## Story

As an operator running rimsky in a data-platform environment, I can use the bundled lineage-receiver subscriber — a Postgres poller over the `concept:lineage` projection (`leaf_run` and `claim_terminal` records only; it implements no `LifecycleSubscriber` RPC and runs no gRPC server) — to translate those two record kinds into a lineage-event JSON payload posted to an external lineage receiver, so that rimsky's run DAG and data lineage surface in my governance platform without writing a custom subscriber.

Bundled lineage-receiver subscriber: polls the `rimsky_lineage` table (Postgres-backed rimsky deployments only) and translates `leaf_run` and `claim_terminal` records into a lineage-event JSON payload; posts them to a configured external lineage receiver. It does not subscribe to template-deploy or other lifecycle events — there is no dataset-version-on-deploy emission, and none is planned; `concept:lineage` documents exactly these two record kinds as the projection's full surface.

Operators see rimsky's run DAG and data lineage in their governance platform without writing a custom subscriber.
