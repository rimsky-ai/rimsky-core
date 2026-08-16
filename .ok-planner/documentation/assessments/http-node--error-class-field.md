---
assessment: http-node--error-class-field
subject: story:http-node
way: error-class-field
release: d977250c
outcome: held
warrant: experiment:http-node
---
# Configuring which field of an upstream error body carries the error class

Three upstream error routes differing in which key names the class were driven. The field the operator configured through `catalog:env-vars/RIMSKY_EXECUTOR_HTTP_NODE_ERROR_CLASS_FIELD` produced an error under `catalog:error-classes/http/request_invalid/*` named by the upstream's own value; a per-node `catalog:executor-attribute-keys/http-node: error_class_field` overrode that deployment-wide setting for one node; and a body naming no class at all fell back to a stable unspecified class rather than to nothing. A template author can therefore write retry policy against the upstream's own error vocabulary, and still has a class to route on when the upstream supplies none.

## Unverified remainder

None: the passing run demonstrates the way as promised.
