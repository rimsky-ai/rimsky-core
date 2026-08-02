---
story: subscriber-lineage-receiver
---

# Operator emits lineage events to an external receiver

## Story

As an operator running rimsky in a data-platform environment, I can use the bundled lineage-receiver subscriber — a Postgres poller over the `concept:lineage` projection (`leaf_run` and `claim_terminal` records only; it implements no `LifecycleSubscriber` RPC and runs no gRPC server) — to translate those two record kinds into a lineage-event JSON payload posted to an external lineage receiver, so that rimsky's run DAG and data lineage surface in my governance platform without writing a custom subscriber.
