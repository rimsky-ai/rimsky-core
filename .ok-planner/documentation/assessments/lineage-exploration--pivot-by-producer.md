---
assessment: lineage-exploration--pivot-by-producer
subject: story:lineage-exploration
way: pivot-by-producer
release: d977250c
outcome: held
warrant: experiment:lineage-exploration
---
# Listing everything a named producer committed

The audit queried `catalog:http-routes/GET /v1/lineage/by-producer/{executor_name}` by the name of the producer the workflow used, and it returned that producer's three committed claim records — one of which names the two sub-claims the fan-out created. Asked for a producer that had committed nothing, the same pivot returned none rather than an error or an unrelated set, so an empty answer is meaningful. Together with the claim pivot this lets an operator move from "this producer" to "these claims" to "these runs" without knowing a run id to start from.

## Unverified remainder

Only the bundled filesystem claim producer and one producer name that committed nothing were pivoted on. The pivot was not exercised against a deployment carrying many producers of different kinds.
