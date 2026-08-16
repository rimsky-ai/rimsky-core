---
audit: cascade-send
artifact: story:cascade-send
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:11:27Z
---

# A declared node-type whose dispatch is to send a message

Supported: the sender is a real graph object and its dispatch really sends. The
declared send-node appears in the instance's node list as an ordinary node type
alongside the counter it subscribes to and the message type it targets, which is
the "graph object I can point at" the story asks for. Its dispatch put exactly
one message in the ledger — not zero and not a duplicate — attributed with the
instance as sender kind rather than an operator, carrying the body the node
composed from the attribute it held. The cross-frame coupling was observed rather
than inferred: that message opened a frame of its own, the frame names the
instance as sender, and the downstream node subscribing to the message type ran
in it on the composed body. Eight checks, none failing.
