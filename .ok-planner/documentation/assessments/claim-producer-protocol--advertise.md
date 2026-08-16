---
assessment: claim-producer-protocol--advertise
subject: story:claim-producer-protocol
way: advertise
release: d977250c
outcome: held
warrant: experiment:claim-producer-protocol
---
# A producer's startup advertisement reaches the deployment

A claim producer written against the published protocol was started five times — one per advertised write semantics, plus one that always refuses to open — and a deployment of `catalog:images/rimsky-all-in-one` was pointed at all five. All five appeared in `catalog:http-routes/GET /v1/observability/claim-producers`, each carrying the error class it declares. What the author puts in the capabilities handshake is therefore what an operator sees on the deployment, without any second registration step.

## Unverified remainder

None: the passing run demonstrates the way as promised.
