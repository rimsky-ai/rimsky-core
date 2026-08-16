---
audit: attribute-carry-forward
artifact: story:attribute-carry-forward
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T09:40:00Z
checked: 3
unaccounted: 1
---

# Attributes carry forward within a run-scope, and reset in two of the three new-scope kinds

Unsupported by coverage: the carry-forward half of the promise holds, two of the
three new-run-scope kinds the story enumerates were demonstrated, and the third
cannot be reached at all at this tree. Driven through the control API of an
all-in-one deployment, a stateful node cascading to itself inside one frame saw
every dispatch carry the value the previous dispatch's executor had written, and
the node's read surface answered with that value; a second operator message
opened a second frame and the same node began again at the schema's default; a
fan-out over three partitions gave every partition a dispatch starting at the
default, with no partition continuing a sibling's. The third kind, a sub-graph
invocation, is unaccounted. A sub-graph whose internal node names an executor
settles normally, with the caller, the internal node and the exit each
dispatching in turn. Change only that node to a stateful node declared by
builtin kind and the internal node is never dispatched: its run sits waiting
indefinitely, the exit node runs anyway without its upstream having produced
anything, the caller's run stays running, and the frame never settles. That was
reproduced on three separate runs and with two different builtin kinds, and the
same node declared the same way in a flat graph runs normally — so the obstacle
is the sub-graph, not the node. No run therefore reaches a state that would show
the incoming bag either way in a sub-graph run-scope, and the hang is product
behaviour in its own right.

## Unaccounted

- Sub-graph invocation: no run demonstrates a node starting a sub-graph run-scope from the schema defaults, because a stateful node declared by builtin kind inside a delegated sub-graph is never dispatched at this tree.
