---
concept: claim-tree
---

# Claim tree

## What it is

A claim tree is the tree-shaped relationship across claim handles. Each sub-claim points at the claim it was split from, and a root claim points at nothing (see `concept:claim-handle`). Fan-out creates the tree: rimsky opens one sub-claim per sub-scope the producer splits out (see `concept:fan-out`). The shape mirrors the run tree but sits at the claim layer rather than the dispatch layer; the run tree's own parent-child shape belongs to `concept:run-scope`.

## Purpose

The claim tree is what lets a fan-out parent resolve from its children. When a child claim resolves, the parent-resolution walk reads the parent's children and records the child's outcome. While any holder of the parent or any child claim is still active, the walk stops there. Once none of them is active, the walk computes the parent's aggregate verdict under the aggregation policy the parent snapshotted, then fires the parent's own terminal, which may in turn walk up to a grandparent (see `concept:terminal-resolution`, `concept:fan-out`, `concept:node-run`).

## Boundaries

The claim tree owns the parent link between claim handles, the listing of a claim's children, the recursive walk that resolves a parent from them, and the recursive walk that cancels a claim's descendants whenever a claim resolves to abandon, whatever the aggregation policy says. It does not own acquisition, which belongs to `claim` and `claim-handle`; the aggregation policy the walk applies, which belongs to `fan-out`; the run tree's structure or its use in aggregating run state, which belong to `run-scope` and `node-run`; or the cancellation of a resolving child's in-flight siblings, which belongs to `cancel-siblings`. See also: `claim-handle`, `fan-out`, `cancel-siblings`, `auto-terminal`, `terminal-resolution`, `node-run`, `run-scope`.
