---
concept: run-scope
---

# RunScope

## What it is

A RunScope is the execution context for one graph instantiation inside a single frame. Rimsky persists every RunScope. Each RunScope owns a set of node-runs, which operators call the RunSheet. RunScopes form a tree through their parent-RunScope pointer, rooted at the frame's root RunScope. A RunScope lives inside exactly one frame and never spans frames, so a frame is exactly one RunScope tree.

A RunScope takes one of three kinds. The root RunScope is the frame's top-level execution context: a frame has one, it has no parent RunScope and no parent run, and the frame names it. A sub-graph RunScope holds one delegated sub-graph invocation; its parent is the calling node's RunScope, and its parent run is the calling node's run. A fan-out partition RunScope holds one partition a fan-out node emitted; its parent is the fan-out node's RunScope, its parent run is the fan-out node's run, and it carries a partition key. Kind is derivable rather than stored: a RunScope with no parent RunScope is the root, a RunScope carrying a partition key is a fan-out partition, and every other RunScope is a sub-graph.

Any RunScope spawns child RunScopes, whether by delegation or by fan-out, so a child's parent is whatever RunScope created it and not necessarily the root. A RunScope closes when its parent-run rendezvous fires, when the owning frame settles, or when an operator terminates the owning instance; terminating an instance closes a whole tree, children before parents.

## Purpose

A RunScope gives every execution context inside a frame one representation, so the runtime addresses a root graph, a delegated sub-graph, and a fan-out partition through the same structure. The persisted parent chain answers how a run was reached: it backs recursion gating at dispatch, behind the rejection `concept:sub-graph` makes at registration. It also gives an executor a context stable across dispatches, which is how a recovery handoff from a prior dispatch reaches the current one.

## Boundaries

A RunScope owns the node-run set inside it, its own lifecycle from creation to closure, and its parent-RunScope and parent-run relationships. It does not own claim semantics, whose parallel structure is `concept:claim-tree`. It does not own cascade-edge semantics: `concept:cascade` traverses subscription edges within and across RunScopes inside one frame. It does not own the frame; a RunScope lives inside one frame, and the frame owns the root of its tree (see `concept:frame`).

See also: `concept:frame`, `concept:node-run`, `concept:fan-out`, `concept:delegation`, `concept:sub-graph`, `concept:claim-tree`, `concept:cascade`.
