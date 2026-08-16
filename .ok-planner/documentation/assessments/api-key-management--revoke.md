---
assessment: api-key-management--revoke
subject: story:api-key-management
way: revoke
release: d977250c
outcome: held
warrant: experiment:api-key-management
---
# Withdrawing a key so it stops being accepted

`catalog:cli-verbs/rimsky auth revoke` was applied to a minted key, and the effect was checked with that key against the control API: its very next request answered 401. The revocation is also visible in the record rather than silent — the key left the default listing and remained readable under `catalog:cli-flags/--include-revoked`. An operator can therefore withdraw one credential without touching the others or taking the deployment down.

## Unverified remainder

None: the passing run demonstrates the way as promised.
