---
concept: child-execution
---

# Child execution

## What it is

Child execution is the umbrella term for the two mechanisms by which a parent node-run dispatches work into child execution contexts. **Fan-out** clones the calling node once per partition and fans in by aggregating the partitions' sub-claims. **Sub-graph delegation** substitutes into an absorbed entry, dispatches the sub-graph's distinct internal nodes into one shared child context, and fans in on a designated exit node's writeback. The two are structurally different rather than variants of one shape (see `decision:fan-out-and-delegation-are-distinct-mechanisms`).

What they share is a thin dispatch helper that takes a matrix of partitions by children and creates the corresponding child run scopes and child runs. Fan-out hands it many partitions and one child, the cloned calling node. Delegation hands it one partition and many children, the sub-graph's internal nodes other than the absorbed entry. Settlement is not shared, because the two fan-in shapes differ, so each mechanism has its own settle primitive:

- **Carry**, delegation's settle, fires once, when the sub-graph's designated exit terminates. It copies the exit's writeback verbatim onto the calling node's attributes, closes the child run scope, and fires the parent-settlement cascade.
- **Aggregate**, fan-out's settle, fires once per clone, as each sub-claim resolves, and records that child's outcome on the parent claim handle. It computes the aggregate verdict only once no claim holder and no child claim handle on the parent is still active. In that one firing it closes every partition context together, settles the parent's claim, and fires the parent-settlement cascade. A clone's own attribute writeback never merges into the parent's attribute bag.

Carry and aggregate are stable names for the two settle primitives. Which primitive settles a child execution follows from the dispatch path that created the children, never from a setting the author picks.

## Purpose

Child execution gives callers and reviewers stable names — fan-out, sub-graph delegation, carry, aggregate — for shapes that are easy to conflate, and names the one helper the two dispatch paths genuinely share. The umbrella makes the distinction explicit; it claims no common shape beyond that helper.

## Boundaries

Child execution owns the names of the two mechanisms and of their two settle primitives, and the thin dispatch helper that creates child run scopes and child runs from a matrix of partitions by children. The execution contexts themselves, their tree structure, their lifecycle, and every path that closes one belong to `concept:run-scope`; this concept claims only what a settle primitive does as it closes one. The template surfaces belong to `concept:delegation` and `concept:fan-out`. The aggregation policy, a fan-out-only choice among a closed set of verdict rules, belongs to `concept:fan-out`, and so does sub-claim acquisition: the dispatch helper takes already-acquired sub-claims and never splits a claim itself. Entry absorption is a property of delegation rather than of child execution, and the delegate template surface carries it (see `concept:delegation`).

see also: `run-scope`, `delegation`, `fan-out`, `claim-tree`, `node-run`, `cascade`
