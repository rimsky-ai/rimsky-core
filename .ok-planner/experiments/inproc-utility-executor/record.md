---
experiment: inproc-utility-executor
commit: PENDING
---

# Utility node kinds run with no executor service deployed

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` with its
baked zero-config defaults — no mounted config, no executor block, no service
containers — and drives it through the control API.

The template declares three nodes, one per bundled utility kind: a
`loop_counter`, a node whose `sends_message` reaches the `send_message` kind,
and an `attribute_passthrough` receiver of the sent message. No node names an
executor.

## What was observed

Eleven checks, none failing. Every executor the deployment knows about answers
at an `inproc://` address; none is reachable at a service address. The template
registered, deployed and ran: the loop-counter emitted `count: 1`, the send node
put one `util/ping` message in the ledger, and the passthrough receiver carried
the message body into its own output attributes as `got: 1`. Each of the three
kinds started exactly once and settled `terminal/success`.
