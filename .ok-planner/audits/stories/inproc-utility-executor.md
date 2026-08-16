---
audit: inproc-utility-executor
artifact: story:inproc-utility-executor
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:01:18Z
---

# Utility node kinds dispatch with no executor service deployed

Supported. Driven through the public surface against a released-image stack on
its baked zero-config defaults — no mounted configuration, no executor block, no
service containers — with one template declaring three nodes, one per bundled
utility kind, and no node naming an executor. Eleven checks, none failing. Every
executor the deployment knows about answers at an in-process address and none at
a service address, so nothing external could have served these dispatches. All
three kinds dispatched exactly once each and settled successfully, and each did
its own work: the loop counter emitted its count, the message-sending kind put
one message in the ledger, and the passthrough receiver carried that message's
body into its own output attributes — one template, three kinds, no deployment
beyond the stack itself.
