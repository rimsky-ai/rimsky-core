---
experiment: typed-message-substitution
commit: PENDING
---

# Addressing each message type by name, and carrying a body to the next frame

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`) and deploys two templates.
The first declares two message types and one node that subscribes to both and
sources one attribute from each type's field, each directive carrying a fallback
literal. The second declares a counter node, a `sends_message` node composing
its body from the counter's attribute, and a node reading that body. The run
sends one message of each type to the first instance and one wake to the second,
then reads the event log, the message history and the frame list.

## What was observed

In the frame the first type opened, the node resolved that type's field and fell
back on the other; in the frame the second type opened, the same node resolved
the other way round. One node that could react to either type never mixed them.
A directive reading a field the declared body schema does not carry is refused
at registration.

In the second instance the sender composed a body from the attribute it held and
put one message in the ledger attributed to the instance. That message opened
the second of the instance's two frames, and the node there read the value
through the same grammar, so the value crossed a frame boundary in a message
body.

Seven checks, none failing.
