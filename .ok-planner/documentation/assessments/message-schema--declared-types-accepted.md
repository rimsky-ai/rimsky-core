---
assessment: message-schema--declared-types-accepted
subject: story:message-schema
way: declared-types-accepted
release: d977250c
outcome: held
warrant: experiment:message-schema
---
# Messages of a declared type are accepted and reach the node that reads them

The audit registered a template declaring two message types with body schemas through `catalog:http-routes/POST /v1/templates` and drove an instance of it on an all-in-one deployment (`catalog:images/rimsky-all-in-one`). Both declared types were accepted at `catalog:http-routes/POST /v1/instances/{id}/messages`, and each reached the node that reads it. The instance's history at `catalog:http-routes/GET /v1/instances/{id}/messages` afterwards held exactly the two accepted messages and nothing else. A declared type is therefore a working contract, not merely a validation hurdle. Eleven checks across this way and its siblings, none failing.

## Unverified remainder

Two message types with body schemas were declared and driven. The way does not enumerate every schema construct a declaration can use.
