---
audit: node-admin
artifact: story:node-admin
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:42:46Z
---

# An operator reads a node's whole state and clears a settled failure off it

Supported. Driven through the public surface against a released-image stack
paired with the released shape-check executor service, on a template whose two
nodes share one declared check and are fed clean and violating rows, so one
instance settles with one node successful and one failed. Twenty-seven checks,
none failing. The node read returned the node's whole state in one document of
ten fields — identity, instance, node type, executor, declared tags, cascade
mode, creation time, run tallies, the attributes the run left behind including
the offending row and the rejecting check, and the settled failure signal —
while the healthy node's identical read carried a success signal, so the marker
distinguishes the two. Clearing was refused on the node that never failed,
naming the condition, and succeeded on the failed one; the same read afterwards
carried no settled signal while identity, executor, run tallies and the check's
findings were unchanged, so the failure marker is gone and nothing else about
the readable state moved, and the clearing is on the instance's event log as an
operator override. The operator CLI renders a narrower seven-field projection
of the same document, which still carries the marker.
