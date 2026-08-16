---
assessment: validation-author--conformance-check
subject: story:validation-author
way: conformance-check
release: d977250c
outcome: held
warrant: experiment:validation-author
---
# A service author checks their validator against the protocol before wiring it in

The audit ran `catalog:cli-verbs/rimsky conformance validation` with `catalog:cli-flags/--role` against a service that serves the validation protocol alongside its primary one — the shipped `catalog:images/rimsky-executor-verifier-shape-checks`, taken as a worked example of the story's role. All four of the verb's checks passed against that service, including the case where the validator is asked about a role it does not serve. A service author therefore has a self-contained way to know their validator conforms before any deployment declares it.

## Unverified remainder

The verb was run for one role against one service. The demonstration does not establish what the verb reports against a service that conforms only partly.
