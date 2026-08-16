---
assessment: iterative-workflows-converge--self-cycle
subject: story:iterative-workflows-converge
way: self-cycle
release: d977250c
outcome: held
warrant: experiment:iterative-workflows-converge
---
# Declaring a node that re-runs against its own output until it converges

A node subscribing to its own success under a predicate written in `catalog:template-keys/nodes[].subscribes[].when`, carrying `catalog:template-keys/nodes[].cascade_mode` set to sequenced, iterated three rounds and stopped. The round ceiling available to the author was set far above the rounds actually run, so what stopped the cycle was the declared stop condition and not a count. The converged output was left for a downstream node subscribed under the leaving predicate, which ran exactly once, so iteration composes with the rest of the graph. The whole iteration reads back through `catalog:http-routes/GET /v1/instances/{id}/frames` as a single completed frame. Nine checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
