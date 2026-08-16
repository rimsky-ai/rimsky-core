---
assessment: api-key-management--list
subject: story:api-key-management
way: list
release: d977250c
outcome: held
warrant: experiment:api-key-management
---
# Listing the outstanding keys without exposing their plaintext

`catalog:cli-verbs/rimsky auth list` named all three keys the run had minted. The listing carried no field matching "plaintext" and did not reproduce the live plaintext of any key it named — the plaintext is shown once, at mint, and the listing is not a second copy of it. Listing also respects revocation state: a revoked key dropped out of the default listing and came back under `catalog:cli-flags/--include-revoked`, so the operator can see either the credentials currently in force or the whole history.

## Unverified remainder

None: the passing run demonstrates the way as promised.
