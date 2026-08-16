---
assessment: forensic-last-attribute--observability-read
subject: story:forensic-last-attribute
way: observability-read
release: d977250c
outcome: held
warrant: experiment:forensic-last-attribute
---
# Reading the same latest bag from the observability node surface

The observability node read at `catalog:http-routes/GET /v1/observability/nodes/{instance_id}/{node_type}` was asked for the same node's latest resolved attribute bag and answered with the same bag the node read gave — the third dispatch's, including the resolved input value no delta carried. The two read surfaces agree, so an operator debugging from a dashboard and one debugging from the node route reconstruct the same state and neither has to fold the event feed by hand.

## Unverified remainder

None: the passing run demonstrates the way as promised.
