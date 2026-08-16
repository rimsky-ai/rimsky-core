---
assessment: cascade-send--message-sender-node
subject: story:cascade-send
way: message-sender-node
release: d977250c
outcome: held
warrant: experiment:cascade-send
---
# Declaring a node whose job is to send a message

The audit registered a template declaring one message type and three nodes — a counter, a node whose dispatch sends a message composed from the counter's attribute, and a node subscribing to that message type — and drove it through the control API on `catalog:images/rimsky-all-in-one`. The sender is a real graph object: it appears in the instance's node listing as an ordinary node type alongside the counter it subscribes to, which is the thing the author wanted to be able to point at. Its dispatch put exactly one message in the instance's message ledger — not zero, not a duplicate — attributed to the instance as sender rather than to an operator, carrying the body the node composed. The cross-frame coupling was observed rather than inferred: that message opened a frame of its own, the frame names the instance as sender, and the downstream node subscribing to the message type ran in that frame on the composed body. Eight checks ran and none failed.

## Unverified remainder

None: the passing run demonstrates the way as promised.
