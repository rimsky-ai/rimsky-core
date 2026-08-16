---
assessment: api-key-management--create-key
subject: story:api-key-management
way: create-key
release: d977250c
outcome: held
warrant: experiment:api-key-management
---
# Minting a scoped key that can do less than the admin key

`catalog:cli-verbs/rimsky auth create-key` with `catalog:cli-flags/--role` set to `catalog:bundled-roles/read-only` minted a key whose reach was then checked against the control API with that key in hand: it read instances and was refused with 403 when it tried to register a template. The role bound rather than being recorded and ignored. A second mint with `catalog:cli-flags/--expires` produced a working key with a lifetime on it, so an operator can hand out a credential that is both narrower than admin and time-bounded.

## Unverified remainder

The run confirmed that an expiring key works when minted; it did not run the clock out to observe the key stop being accepted at expiry.
