---
assessment: rimsky-deployment-bootstrap--migrate-discipline
subject: story:rimsky-deployment-bootstrap
way: migrate-discipline
release: d977250c
outcome: held
warrant: experiment:rimsky-deployment-bootstrap
---
# Schema migrations run exactly once per deployment, whatever the role split

Migration ownership was measured on a real three-container split sharing one database, not inferred. Exactly one of the three containers ran the migrations; the other two reported skipping them, the schema arrived, and a node dispatched and settled successfully across the split roles — so the migrations neither raced nor were silently skipped. The override at `catalog:env-vars/RIMSKY_ENTRYPOINT_MIGRATE` moved ownership in both directions: off for a deployment that would otherwise own it, on for a role that would otherwise skip, which is what a dedicated one-shot init step needs. A value that is neither of the two legal ones failed startup naming the variable and the value it was given, so a typo cannot quietly change who migrates.

## Unverified remainder

The split was three containers against one database. The way does not establish ownership across several deployments sharing one database, nor under a rolling upgrade where old and new containers run together.
