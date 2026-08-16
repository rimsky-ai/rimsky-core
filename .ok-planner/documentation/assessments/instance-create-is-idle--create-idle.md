---
assessment: instance-create-is-idle--create-idle
subject: story:instance-create-is-idle
way: create-idle
release: d977250c
outcome: held
warrant: experiment:instance-create-is-idle
---
# Creating an instance of a deployed template and having nothing happen

`catalog:cli-verbs/rimsky instance create` against a container of `catalog:images/rimsky-all-in-one` returned an instance id and materialized the node graph — the root node was listed — while every node run counter read zero, the instance's event log was empty, and its message queue was empty. The negative is anchored to a live scheduler rather than to elapsed time: a sibling instance was created, woken and driven to completion, proving the deployment was running work, and the untouched instance was still event-free and still at zero counters when re-read afterwards. Creating an instance therefore has no side effect an operator has to undo or wait out.

## Unverified remainder

None: the passing run demonstrates the way as promised.
