---
concept: delegation
---

# Delegation

## What it is

Delegation is an invocation pattern over `concept:child-execution`: a node that names a sub-graph instead of declaring its own executor dispatches that sub-graph's internal nodes as one shared child execution context under the carry settle primitive, with the sub-graph's entry absorbed. Absorption makes the calling node stand in for the entry. At canonicalization the calling node inherits the entry's executor, claim-producer bindings, holds bindings, and attribute schema, merged with what the calling node declared for itself; the entry's other declared fields do not carry over. The entry keeps its own identity in the template and in the persisted graph, but nothing ever dispatches that identity, and no run of the entry is ever created. An internal node that subscribes to the entry alias resolves to the calling node's run instead of to a separate entry run. The calling node's run stays in the parent execution context (see `concept:run-scope`), while the sub-graph's other internal nodes — the exit and any intermediates — run in the child execution context the dispatch allocates. The exit node is not absorbed: it is its own node, shared declaratively across every invocation of this sub-graph in this instance, and it runs inside the child context. When the exit reaches its terminal, settlement copies the exit's writeback verbatim onto the calling node's attribute bag and closes the child context — the carry settle primitive of `concept:child-execution`. Entry absorption is structural, and exit carry-up is that primitive, so delegation configures no aggregation policy: the invocation pattern picks the settle primitive.

## Purpose

Delegation lets a template author compose one graph out of another. An author writes a sub-graph once and invokes it from any node that names it. The invocation reads to the rest of the graph as a single node: upstreams cascade into the calling node, the sub-graph runs beneath it, and the sub-graph's result lands on the calling node's own attributes. The caller needs no knowledge of the sub-graph's internal shape, and the sub-graph needs no knowledge of its callers.

## Boundaries

Delegation owns the template surface that points a node at a sub-graph, entry absorption at canonicalization — the genuine asymmetry against `concept:fan-out` — and the entry-success cascade that starts the sub-graph's internal walk. It does not own the dispatch and settlement shape, the closure of the child context, or the atomicity of the carry; those belong to `concept:child-execution`. It does not own the sub-graph's own template surface (see `concept:sub-graph`), nor per-run state aggregation (see `concept:node-run`). Delegation and fan-out are independent mechanisms (see `decision:fan-out-and-delegation-are-distinct-mechanisms`). A delegating node may itself be a fan-out clone: each clone absorbs the entry on its own and opens its own child execution context, so many partitions produce as many independent sub-graph invocations, each carrying up its own exit rather than sharing one.

see also: `child-execution`, `sub-graph`, `fan-out`, `node`, `node-run`, `run-scope`, `cascade`
