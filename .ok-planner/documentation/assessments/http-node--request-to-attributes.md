---
assessment: http-node--request-to-attributes
subject: story:http-node
way: request-to-attributes
release: d977250c
outcome: held
warrant: experiment:http-node
---
# Issuing a request to an upstream API and reading the response into the node

A node using the bundled `catalog:bundled-services/http-node (executor)` with `catalog:executor-attribute-keys/http-node: url` pointed at a controlled upstream issued the request and the response body became the node's output attributes verbatim, nested values included. No custom executor was written or deployed for it — the executor runs in-process on the released image. The deployment had to opt the upstream's private address back in through `catalog:env-vars/RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST`: without it the same request was refused as `catalog:error-classes/http/network_error`, so reaching a private endpoint is an operator's deliberate act. Eleven checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
