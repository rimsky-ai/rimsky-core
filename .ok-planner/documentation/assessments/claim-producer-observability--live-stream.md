---
assessment: claim-producer-observability--live-stream
subject: story:claim-producer-observability
way: live-stream
release: d977250c
outcome: held
warrant: experiment:claim-producer-observability
---
# Streaming claim-state changes as they happen

A stream opened on a claim first replayed that claim's state, then carried the commit as a live event while the stream stayed open, after which the claim read committed. A dashboard therefore does not have to choose between a snapshot and a subscription: one stream gives it where the claim stands now and every change after that, so no state transition falls into the gap between an initial read and the start of polling.

## Unverified remainder

None: the passing run demonstrates the way as promised.
