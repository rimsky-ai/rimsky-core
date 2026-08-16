---
assessment: validation-mixin-uniform--executor-peer
subject: story:validation-mixin-uniform
way: executor-peer
release: d977250c
outcome: held
warrant: experiment:validation-mixin-uniform
---
# A service that plays the executor role gets to vet the templates that use it

The audit ran a purpose-built service speaking only the published protocols, declared to the deployment as an ordinary peer, and validated one template that names it as a node's executor. The peer's validation mix-in was consulted for the executor role and its finding named the node it was called about. The deployment discovered the mix-in through the peer's own capabilities handshake, so the author advertises it from their service rather than registering it anywhere in the product.

## Unverified remainder

One executor peer over one template was exercised. The demonstration does not establish behaviour when the same peer executes several nodes in one template.
