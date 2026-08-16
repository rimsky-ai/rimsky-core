---
experiment: cascade-send
commit: PENDING
---

# Declaring a message-sender node-type

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`) and drives it entirely
through the control API: register template, deploy template, create instance,
send one operator message, then read back the message ledger, the frame list,
the node list, and the event log.

The template declares one message type and three nodes. A counter node wakes on
the operator message. A `sends_message` node subscribes to the counter and
composes the message body from the counter's attribute. A third node subscribes
to the message type as a node-type and reads the body.

## What was observed

The send-node appears in the instance's node list as an ordinary node type. Its
dispatch put exactly one message in the ledger, attributed with sender kind
`instance` and carrying the body the node composed. That message opened a second
frame whose sender kind is `instance`, and the downstream node ran on the body.

Eight checks, none failing.

RESULT: PASS
