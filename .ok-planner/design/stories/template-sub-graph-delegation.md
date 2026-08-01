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

