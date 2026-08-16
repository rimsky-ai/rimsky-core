---
assessment: executor-protocol--error-class-routing
subject: story:executor-protocol
way: error-class-routing
release: d977250c
outcome: held
warrant: experiment:executor-protocol
---
# Having the errors my executor raises routed by the class it declared

Error handling followed the class the peer raised rather than a generic failure path. The node whose policy under `catalog:template-keys/nodes[].error_types.<class>.action` mapped one declared class to giving up was dispatched exactly once and then failed. The node whose policy mapped the other declared class to retrying was dispatched three times — once plus its two retries — before failing. Both classes are the executor author's own vocabulary, advertised at discovery, so retry policy is written against the author's names and not against rimsky's. A template author gets the routing the executor author intended without either of them agreeing on anything beyond the protocol.

## Unverified remainder

None: the passing run demonstrates the way as promised.
