---
assessment: sensor-webhook--redelivery-deduplicated
subject: story:sensor-webhook
way: redelivery-deduplicated
release: d977250c
outcome: held
warrant: experiment:sensor-webhook
---
# A caller that retries the same delivery does not double the work

The audit redelivered a body under a delivery id the sensor had already seen. The call was accepted rather than refused, and it produced no second message on the target instance. An external system that retries on its own — as webhook senders commonly do — therefore triggers the node once, and the operator does not have to make the graph tolerate duplicates.

## Unverified remainder

One redelivery of one id was exercised. The demonstration does not establish how long a delivery id stays known to the sensor, nor whether that memory survives a restart.
