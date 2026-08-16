---
assessment: mandatory-instantiation-gate--every-referenced-service
subject: story:mandatory-instantiation-gate
way: every-referenced-service
release: d977250c
outcome: held
warrant: experiment:mandatory-instantiation-gate
---
# The gate checks against every service the template references, not just the first

The story's universal is over every referenced service, so the audit took it on a two-service template whose second service's attribute carried a type violation while the first service's configuration was clean. The create was refused through `catalog:http-routes/POST /v1/instances`, and the refusal named the node bound to the second service — so the gate is not limited to the first of the services a template references, and a violation cannot hide behind a well-formed neighbour. The same two-service template with both schemas satisfied created cleanly, confirming the refusal followed the violation rather than the service count.

## Unverified remainder

The universal was demonstrated at two referenced services, with the violation on the second. It was not enumerated over templates referencing more than two services, nor over every service kind a template can reference.
