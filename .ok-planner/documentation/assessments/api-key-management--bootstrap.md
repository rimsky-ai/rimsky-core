---
assessment: api-key-management--bootstrap
subject: story:api-key-management
way: bootstrap
release: d977250c
outcome: held
warrant: experiment:api-key-management
---
# Minting the first admin key on a fresh deployment

`catalog:cli-verbs/rimsky auth init` minted the admin key on the fresh deployment and printed its plaintext once. The effect was confirmed independently against the control API with the key itself: status moved to authenticated with one key and one admin. Bootstrapping is a one-time act — a second `catalog:cli-verbs/rimsky auth init` against the same deployment refused and exited non-zero, so an operator cannot quietly mint a second first-admin over an established deployment.

## Unverified remainder

None: the passing run demonstrates the way as promised.
