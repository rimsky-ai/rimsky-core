---
experiment: template-sub-graph-delegation
commit: PENDING
---

# A node that delegates to a named sub-graph

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image. The template
declares a `main` graph whose single node delegates to a named sub-graph with
a declared entry, an internal node, and a declared exit, all running the
in-process `verifier-shape-checks` executor. A second template makes the
sub-graph's exit fail. `run.sh` boots and removes the container.

## What was observed

The event log carried `subgraph.dispatched` on the calling node, so the
sub-graph is what the node dispatched as its execution unit. The sub-graph's
entry had no run of its own — it runs inside the calling node — while the
internal node and the exit each ran. `subgraph.exit_carry` carried the exit's
outcome back, and the caller's settling signal came after that carry in event
order: the caller settles once the sub-graph settles. When the sub-graph's exit
failed, the caller settled failed too, with an aggregate error signal rather
than a success.
