---
assessment: claim-producer-postgres--pick-policy
subject: story:claim-producer-postgres
way: pick-policy
release: d977250c
outcome: held
warrant: experiment:claim-producer-postgres
---
# Handing each claimant a different row from a queue

The audit ran a database, two instances of `catalog:bundled-services/claim-producer-postgres` over it, and a deployment of `catalog:images/rimsky-all-in-one` pointed at both. One producer was configured with a pick policy over a seeded items table. Two claimants took claims through it and each received a distinct row, with that row's payload reaching its own node's dispatch, and the claim handles record synchronous write semantics. Two workers pulling from one queue therefore do not collide on the same item, and the operator declares that behaviour as configuration rather than building it into the template.

## Unverified remainder

None: the passing run demonstrates the way as promised.
