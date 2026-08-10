---
audit: template-sub-graph-delegation
artifact: story:template-sub-graph-delegation
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:20:00Z
---

# A node delegating to a named sub-graph dispatches it and settles on its outcome

Supported. Against a zero-config all-in-one deployment, a template whose main
graph holds one node delegating to a named sub-graph ran to completion: the event
log recorded the sub-graph dispatch on the calling node, the sub-graph's declared
entry had no run of its own because it runs inside the caller, both remaining
sub-graph nodes ran, the exit carried its outcome back, and the caller's settling
signal followed that carry in event order. A second template whose sub-graph exit
fails settled the caller failed on the sub-graph's outcome rather than
successfully, so the caller's verdict is the sub-graph's in both directions.
