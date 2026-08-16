---
assessment: http-node--rate-limit-park
subject: story:http-node
way: rate-limit-park
release: d977250c
outcome: held
warrant: experiment:http-node
---
# Opting a node into parking on an upstream's rate-limit response

Against an upstream route that answers 429 once and then clears, the node emitted one park tagged as rate-limited and carrying a resume time derived from the upstream's own retry-after directive, rather than failing. It then resumed by itself and succeeded against the cleared upstream on exactly one further run, so the wait is the upstream's number and the retry is not the template author's to schedule. The behaviour is opt-in and bounded by the node's own declaration: a node listing the rate-limit status under `catalog:executor-attribute-keys/http-node: expect_status` did not park and settled successfully, treating the status as an expected answer.

## Unverified remainder

None: the passing run demonstrates the way as promised.
