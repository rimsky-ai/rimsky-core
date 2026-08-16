---
audit: template-sub-graph-delegation
artifact: story:template-sub-graph-delegation
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:06:40Z
---

# A delegating node dispatches its sub-graph and settles on the sub-graph's outcome

Supported. Driven through the public surface against a container of the released
all-in-one image, on a template whose single main-graph node delegates to a named
sub-graph with a declared entry, an internal node and a declared exit, and on a
second template that makes the sub-graph's exit fail. Ten checks, none failing.
The event log showed the calling node dispatching the sub-graph as its execution
unit; the sub-graph's entry had no run of its own while the internal node and the
exit each ran; the exit's outcome was carried back to the caller, and the
caller's settling signal followed that carry in event order, which is the "settles
once the sub-graph settles" the story promises. With the exit made to fail, the
caller settled failed carrying the sub-graph's outcome rather than succeeding, so
the composition propagates in both directions.
