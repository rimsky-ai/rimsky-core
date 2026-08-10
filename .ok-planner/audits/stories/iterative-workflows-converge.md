---
audit: iterative-workflows-converge
artifact: story:iterative-workflows-converge
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Cycles declared in the template, terminating on a declared predicate

Supported. Both graph shapes the story names ran to rest on a zero-config
all-in-one deployment. A node subscribed to its own success signal under a
predicate over its own output ran three rounds and stopped; a two-node cycle
whose back edge carried the same predicate ran three rounds and stopped. In
both templates the iterating node's round cap was set to 50, far above the
three rounds observed, so the declared predicate and not a count ceiling is
what terminated each cycle. A downstream node subscribed under the complementary
predicate ran exactly once on the converged output in both templates, so
iteration composes with the rest of the graph. Each whole cycle is one frame in
the observability record, in state completed, and the two-node instance came to
rest with no live node runs.
