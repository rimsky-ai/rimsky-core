---
assessment: verifier-shape-checks--declared-checks-pass
subject: story:verifier-shape-checks
way: declared-checks-pass
release: d977250c
outcome: held
warrant: experiment:verifier-shape-checks
---
# Declaring checks in node config and having a claim's data satisfy them

The audit ran the bundled shape-checks verifier, reachable by name with no service wiring of any kind, with both the rows and the declared checks arriving as node attributes (`catalog:executor-attribute-keys/verifier-shape-checks: rows`, `catalog:executor-attribute-keys/verifier-shape-checks: checks`). Rows satisfying all three declared checks settled the node fresh, and the verifier reported how many checks it ran and how many rows it read. The author enforced data shape without writing or deploying a verifier.

## Unverified remainder

Three check kinds over a small row set were exercised. The demonstration does not establish behaviour over large data volumes.
