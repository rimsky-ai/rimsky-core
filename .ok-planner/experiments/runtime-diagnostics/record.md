---
experiment: runtime-diagnostics
commit: d977250c
---

# A wedged instance explains itself through the product

## What it ran against

A `rimsky-all-in-one` stack from the tree's own image tag on a port the script
picks free at start, the bundled `rimsky-claim-producer-filesystem` service, and
`peer/` — the permissive-peer-build experiment's third-party executor, built for
the run — so a node can be told to park while holding a producer claim. The
released CLI binary from this tree drives the CLI half. The template wedges an
instance deliberately: a claim-holding node parks and does not come back, a
second node co-holds its claim, and a receiver declares a force-refreshed
dependency on the parked node. Everything is then read through the control API
and the CLI; the store is never opened.

## What was observed

The park roster named exactly one node for the instance, that node was the claim
holder, and the row carried both when it parked and when it is due back;
`rimsky parked list` returned the same single row naming the same node.

The held-frame roster reported one frame for the instance, naming the parked
node, reporting its state as parked and how long the frame has been held; the
same frame id appears on the instance's own frame listing.

The frame's wait-set carried three sender/receiver edges, every one naming both
runs and what it is waiting for, and the receiver had not run while its
dependency was pending. Asked without a frame, the route refused with a 400
rather than guessing.

The claim's holder list named one holder — the parked node's run — in state
active, which is why the claim has not come back.

Twenty-three checks, none failing.

RESULT: PASS
