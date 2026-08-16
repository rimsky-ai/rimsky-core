---
assessment: lineage-admin--prune-by-cutoff
subject: story:lineage-admin
way: prune-by-cutoff
release: d977250c
outcome: held
warrant: experiment:lineage-admin
---
# Pruning lineage records older than a cutoff

A deployment with short retention was driven until one workflow's run tree had aged out while its lineage records remained — the state a long-lived deployment reaches, and the state in which the operator's prune has something to remove. A cutoff older than every record deleted nothing and left every record readable at `catalog:http-routes/GET /v1/lineage/runs/{run_id}`; a cutoff newer than the records deleted four rows, after which the run id answered not-found and both `catalog:http-routes/GET /v1/lineage/by-producer/{executor_name}` and `catalog:http-routes/GET /v1/lineage/by-source/{source_type}/{source_id}` answered empty. Work run after the prune recorded lineage again, so pruning does not disable the record, and `catalog:cli-verbs/rimsky lineage prune` with `catalog:cli-flags/--older-than` accepted an age in place of a timestamp and deleted those rows too. Both malformed inputs were refused rather than guessed at: a cutoff that is not a timestamp came back naming the format it wanted, and a prune with no cutoff came back naming the missing field. Ten checks, none failing.

## Unverified remainder

The prune was issued only after the instance's frames had aged out. This run took no separate measurement of a prune issued while the run tree is still live, so nothing is established here about what such a prune removes or leaves.
