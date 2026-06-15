---
story: template-sub-graph-delegation
status: as-is
---

# Template author composes via sub-graphs

## Role

As a template author composing larger workflows, I can declare a node that delegates to a named sub-graph and have it dispatch the sub-graph as its execution unit, with the calling node settling once the sub-graph settles, so that I compose workflows from reusable units.

## Capability

A sub-graph delegation declaration on a node binding it to a named sub-graph: the runtime dispatches the named sub-graph as that node's execution unit and propagates the sub-graph's terminal outcome back to the parent on settle.

## Business value

Template authors compose workflows from reusable units; a complex pipeline can be expressed as a top-level template that delegates to library sub-graphs.

## Acceptance

A template declaring a node with a sub-graph delegation referencing a named sub-graph, plus a separate template providing the named sub-graph; when the parent instance runs, rimsky dispatches the sub-graph (with its own entry and exit nodes) as the delegating node's execution; once the sub-graph settles, the delegating node settles with the sub-graph's terminal outcome propagated back.

## Falsifier

The delegate node settles before the sub-graph does, OR the sub-graph's terminal outcome doesn't propagate to the parent.

## Proof

Executable proof.
