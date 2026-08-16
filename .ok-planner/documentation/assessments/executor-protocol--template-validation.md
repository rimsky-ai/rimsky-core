---
assessment: executor-protocol--template-validation
subject: story:executor-protocol
way: template-validation
release: d977250c
outcome: held
warrant: experiment:executor-protocol
---
# Having templates held to the schema my executor advertises

Rimsky validated registered templates against the peer's own advertisement on four counts, so the author's declarations govern what template authors may write. A node declaring a property as an integer where the executor declares it a string was rejected, the executor being authoritative on types. A node declaring a property the peer's closed schema does not carry was rejected as undeclared. An entry under `catalog:template-keys/nodes[].error_types.<class>.action` naming a class outside the peer's vocabulary came back as a warning. A subscription filtering on a tag the peer never declared was rejected, the refusal naming both the sending node and its executor. The author's schema, error classes and tags are therefore enforced at registration rather than discovered at run time.

## Unverified remainder

None: the passing run demonstrates the way as promised.
