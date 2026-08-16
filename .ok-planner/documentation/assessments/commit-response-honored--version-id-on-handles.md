---
assessment: commit-response-honored--version-id-on-handles
subject: story:commit-response-honored
way: version-id-on-handles
release: d977250c
outcome: held
warrant: experiment:commit-response-honored
---
# The version id a producer returns on Commit lands on the claim handle

The audit drove a deployment of `catalog:images/rimsky-all-in-one` against a producer speaking only the base claim-producer protocol — advertising no data-processing capability — which returns the same version id and metadata blob on every commit. The claim handle for an ordinary claim carries that version id and reads committed, and each of the three sub-claim handles from a fan-out carries it too. The field the wire contract documents is therefore real for the base protocol, not only for producers that also implement the typed-data mix-in.

## Unverified remainder

None: the passing run demonstrates the way as promised.
