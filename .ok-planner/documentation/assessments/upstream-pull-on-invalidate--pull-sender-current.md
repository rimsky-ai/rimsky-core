---
assessment: upstream-pull-on-invalidate--pull-sender-current
subject: story:upstream-pull-on-invalidate
way: pull-sender-current
release: d977250c
outcome: held
warrant: experiment:upstream-pull-on-invalidate
---
# Declaring on a subscription that the sender be refreshed before the receiver runs

The audit registered the same template twice, differing only in the force-refresh value on one subscription, so the declaration is the only variable. The pulled node's only other subscription is to a message type nothing sends, so nothing incidental can run it. With the declaration on, the operator message woke the trigger, the pulled node was brought into the same frame and ran exactly once, and the receiver dispatched afterwards carrying the value the pulled node had just produced. With the declaration off, the pulled node never ran and the receiver settled with a template-resolution error because its source had no value. The declaration — and no incidental invalidation order — is therefore what refreshes the input, so an author expresses "refresh this input first" in the template rather than standing up a separate trigger pathway.

## Unverified remainder

One pulled sender behind one receiver was exercised. The demonstration does not establish a chain where the pulled sender itself declares a force refresh on its own upstream.
