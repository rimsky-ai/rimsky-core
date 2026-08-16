---
assessment: message-schema--unknown-type-refused-at-send
subject: story:message-schema
way: unknown-type-refused-at-send
release: d977250c
outcome: held
warrant: experiment:message-schema
---
# An undeclared or non-conforming message fails loud at the send

This is the half of the promise that could quietly not hold, and it holds. A send of an undeclared message type was refused at `catalog:http-routes/POST /v1/instances/{id}/messages` with a client error naming both the type it refused and the types the template does declare, so the failure lands at the point of sending rather than being discovered later. The typed contract binds the body as well as the name: three non-conforming bodies — a wrong field type, a missing required field, and an extra undeclared field — were each refused against the declared schema. Two independent reads confirmed nothing refused leaked into the system: the instance's history holds only the two accepted messages, and the deployment's event log carries no dead-letter record at all.

## Unverified remainder

Three non-conformance shapes were driven. The way does not enumerate every way a body can fail its schema.
