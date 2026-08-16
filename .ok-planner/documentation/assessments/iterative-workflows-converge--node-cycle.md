---
assessment: iterative-workflows-converge--node-cycle
subject: story:iterative-workflows-converge
way: node-cycle
release: d977250c
outcome: held
warrant: experiment:iterative-workflows-converge
---
# Declaring a cycle of nodes that walks back to its start

A two-node cycle — one node subscribing to the other under the back-edge predicate, and that other subscribing back — iterated three rounds and stopped on the declared condition, with the back-edge node running once per round below the condition. As with the self-cycle, the round ceiling was set far above the rounds run, so convergence and not a ceiling ended it. The downstream node ran exactly once on the converged output, the whole cycle read back as one completed frame, and the instance came to rest with no live runs. A template author therefore expresses a multi-node loop as an ordinary declared graph shape.

## Unverified remainder

None: the passing run demonstrates the way as promised.
