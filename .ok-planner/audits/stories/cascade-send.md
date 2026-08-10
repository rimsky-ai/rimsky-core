---
audit: cascade-send
artifact: story:cascade-send
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:30:00Z
---

# A template author declares a send-node and gets a message the graph can point at

Supported. A template registered through the control API declares a node whose
dispatch field names a message type instead of an executor; the instance's node
list carries that node like any other, its dispatch put exactly one message in
the ledger attributed to the instance and carrying the body it composed from an
upstream attribute, and that message opened a frame of its own that a downstream
node ran in. Both of the story's clauses were exercised on one run: the send is a
graph object (it is a node in the node list) and it is the coupling that crosses
frames (the second frame's triggering message is the one the node sent).
