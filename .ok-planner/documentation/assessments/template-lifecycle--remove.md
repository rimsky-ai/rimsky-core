---
assessment: template-lifecycle--remove
subject: story:template-lifecycle
way: remove
release: d977250c
outcome: held
warrant: experiment:template-lifecycle
---
# Removing a definition once nothing is using it

Removal through `catalog:cli-verbs/rimsky template rm` was refused while an instance record still referenced the definition, and succeeded once that record was gone; the catalogue then no longer listed the definition. The refusal is therefore real protection rather than a warning, and the operator can only remove what nothing points at.

## Unverified remainder

That last refusal arrives as a raw storage-constraint error rather than a conflict naming the referencing record — the operation is correctly refused, but its diagnosis does not tell the operator which record is holding it. Removal with several referencing records was not exercised.
