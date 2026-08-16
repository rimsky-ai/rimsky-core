---
assessment: claim-producer-protocol--terminal-verbs-close-the-claim
subject: story:claim-producer-protocol
way: terminal-verbs-close-the-claim
release: d977250c
outcome: held
warrant: experiment:claim-producer-protocol
---
# All four write-semantics close their claims correctly

The population the story enumerates is the four advertised write semantics, and all four were covered: the persisted claim handles record each producer's realized semantics, one each of the four, and all four reached committed. The write claim was closed with a commit, and so was the read-intent claim. An author picking any of the four therefore gets the same lifecycle guarantee rather than one that is honoured for the common case and improvised for the rest.

## Unverified remainder

None: the passing run demonstrates the way as promised across all four write-semantics the protocol advertises.
