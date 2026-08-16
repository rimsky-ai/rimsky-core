---
assessment: subscriber-lineage-receiver--delivery-to-receiver
subject: story:subscriber-lineage-receiver
way: delivery-to-receiver
release: d977250c
outcome: held
warrant: experiment:subscriber-lineage-receiver
---
# Run records reach an external lineage receiver with no subscriber written

The audit ran `catalog:images/rimsky-subscriber-openlineage` configured only by environment variables — the receiver address, the namespace and the bearer credential (`catalog:env-vars/RIMSKY_OPENLINEAGE_BACKEND_URL`, `catalog:env-vars/RIMSKY_OPENLINEAGE_NAMESPACE`, `catalog:env-vars/RIMSKY_OPENLINEAGE_BEARER_TOKEN`) — beside an orchestrator and a receiver that records every delivery it takes. The receiver held nothing before the subscriber started, and one workflow run produced four deliveries: one per graph node, one for the message that woke the graph, and one for the claim the producing node committed. Every delivery was a well-formed run event with an event type, an event time, a producer identifier, a schema reference, a run id and a job name; every job carried the configured namespace, and every delivery arrived with the configured credential. Nothing in the run required writing a subscriber.

## Unverified remainder

One receiver and one credential form were exercised. The demonstration does not establish what the subscriber does when the receiver refuses a delivery or is unreachable.
