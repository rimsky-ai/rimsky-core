---
assessment: api-key-management--rotate
subject: story:api-key-management
way: rotate
release: d977250c
outcome: held
warrant: experiment:api-key-management
---
# Replacing a key while its holder is still using the old one

`catalog:cli-verbs/rimsky auth rotate` with `catalog:cli-flags/--grace` printed a new plaintext and the old key's revoke time in one act. Both keys were then checked against the control API: the new key worked immediately, and the old key kept working inside the grace window. Once the window closed, the old key stopped being accepted while the new key kept answering. Rotation therefore gives the holder of a credential a bounded interval to pick up the replacement, rather than breaking them at the moment the operator rotates.

## Unverified remainder

None: the passing run demonstrates the way as promised.
