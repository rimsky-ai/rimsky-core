---
assessment: permissive-peer-build--exchange-protocol-verbs
subject: story:permissive-peer-build
way: exchange-protocol-verbs
release: d977250c
outcome: held
warrant: experiment:permissive-peer-build
---
# The permissively-built peer exchanges the protocol's verbs with a real deployment

A peer that builds but cannot talk would not settle the story, so the audit ran the same service against a live deployment (`catalog:images/rimsky-all-in-one`) that declared it as an ordinary gRPC executor. The verbs were exchanged in both directions: the discovery probe's capabilities call returned the peer's own declared error class, and two dispatches settled — one node fresh, carrying the peer's success delta on the record, and one node failed, carrying the peer's own error class — with the peer's own log showing both executions. The peer is therefore a working participant, not merely a compiling one.

## Unverified remainder

The peer implemented the executor protocol. The way does not exercise a permissively-built peer on the other peer protocols.
