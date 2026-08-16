---
assessment: inproc-utility-executor--bundled-utility-kinds
subject: story:inproc-utility-executor
way: bundled-utility-kinds
release: d977250c
outcome: held
warrant: experiment:inproc-utility-executor
---
# Referencing a utility node kind with no executor service deployed

A `catalog:images/rimsky-all-in-one` container ran on its baked zero-config defaults — no mounted configuration, no executor block, no service containers — with one template declaring three nodes, one per bundled utility kind under `catalog:template-keys/nodes[].kind`, and no node naming an executor. Every executor the deployment knows about answers at an in-process address and none at a service address, read back through `catalog:http-routes/GET /v1/observability/executors`, so nothing external could have served these dispatches. All three kinds dispatched exactly once each and settled successfully, and each did its own work: the loop counter emitted its count, the message-sending kind put one message in the ledger, and the passthrough receiver carried that message's body into its own output attributes. One template, three kinds, no deployment beyond the stack itself. Eleven checks, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
