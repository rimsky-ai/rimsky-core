---
experiment: messages-as-nodes-substitution
commit: d977250c
---

# The messages directive and the nodes directive over one lookup

## What it runs against

`run.py` boots a `rimsky-all-in-one` container at `RIMSKY_IMAGE_TAG` (or reuses
the control API named by `RIMSKY_CONTROL_API_URL`). One template declares a
message type and an ordinary node, and a third node sources one attribute
through `{{messages.<type>.<field>}}` and another through
`{{nodes.<node-type>.attribute.<field>}}`. The run sends one message, reads the
node list and event log, then registers four further templates to see what
registration accepts and refuses.

## What was observed

The declared message type appears in the instance's node list as an ordinary
node. One node resolved both directives in the same dispatch and settled with
both values. The value the messages-directive read is an
`attribute/<key>/changed` write on the message type's own node, so the two
directives read the same attribute store.

Registration keeps the two namespaces apart: a messages-directive naming an
undeclared message type is refused, and a nodes-directive naming a message type
is refused as an unknown node. The subscription-coverage check treats both
directives the same way — an uncovered reference of either form is refused with
the `substitution_ref_uncovered` finding naming the subscribes entry to add. The
subscription side is not uniform: an `attribute/<key>/changed` edge on a message
type is refused at registration, because message delivery only ever manifests as
`terminal/success`.

The interchangeability was exercised in the attribute-source context, where
one node resolved both directives in a single dispatch; the run does not
enumerate every context in which a nodes-directive is legal.

Eight checks, none failing.

RESULT: PASS
