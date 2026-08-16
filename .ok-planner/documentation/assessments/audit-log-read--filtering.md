---
assessment: audit-log-read--filtering
subject: story:audit-log-read
way: filtering
release: d977250c
outcome: held
warrant: experiment:audit-log-read
---
# Narrowing the audit to the records that matter

All nine filters `catalog:http-routes/GET /v1/audit` accepts were exercised and each narrowed as claimed: record kind, key name, exact action, action prefix, target path, response status, mode, timestamp lower bound, and page size with a cursor that paged to a different record. Filtering is checked rather than trusting: a record kind outside the auth set and a timestamp that is not RFC3339 were both rejected with 400 rather than quietly matching nothing, so an operator who mistypes a filter learns it instead of reading a misleadingly empty log.

## Unverified remainder

None: the passing run demonstrates the way as promised across the whole filter population the route accepts.
