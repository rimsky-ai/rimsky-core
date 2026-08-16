---
assessment: peer-auth-mtls-mutual--default-unchanged
subject: story:peer-auth-mtls-mutual
way: default-unchanged
release: d977250c
outcome: held
warrant: experiment:peer-auth-mtls-mutual
---
# A deployment that has not been hardened keeps working, unconfigured

The second stack in the same run was left on the untouched default of `catalog:config-keys/peer_auth`, with no CA and no certificates anywhere. It answered plaintext, reported its peer at transport off, and drove a node to a terminal state unchanged. An operator who has not chosen to harden the internal plane therefore pays nothing for the feature — no keys to generate, no certificates to place, no configuration to write — which is the half of the promise that makes the other half adoptable.

## Unverified remainder

The default was exercised on one stack from the same image with one peer. The way does not enumerate every deployment shape that leaves the setting untouched.
