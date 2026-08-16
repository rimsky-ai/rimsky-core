---
assessment: verifier-http--error-route
subject: story:verifier-http
way: error-route
release: d977250c
outcome: held
warrant: experiment:verifier-http
---
# A refusing check service fails the node, carrying the service's own class

On the client-error route the node settled with one failed run and no fresh run. The terminal class carried the upstream's own class appended to the verifier's `catalog:error-classes/verifier/check_failed` family, and the recorded payload named the actual status, the expected status (`catalog:executor-attribute-keys/verifier-http: expected_status`) and the upstream's class. The server-error route settled failed the same way with that route's class, so both error families route to error and both carry the upstream's own class through to the template author's error policy.

## Unverified remainder

None: the passing run demonstrates the way as promised.
