---
audit: upstream-pull-on-invalidate
artifact: story:upstream-pull-on-invalidate
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:46Z
---

# A subscription can declare that its sender be brought current before the receiver dispatches

Supported: a run through the control API of an all-in-one deployment registered
the same template twice, differing only in the force-refresh value on one
subscription. The pulled node's only other subscription is to a message type
nothing sends, so the declaration is the sole thing that can run it. With the
declaration on, the operator message woke the trigger, the pulled node was
brought into the same frame and ran exactly once, and the receiver dispatched
afterwards carrying the value the pulled node had just produced. With the
declaration off, the pulled node never ran and the receiver settled with a
template-resolution error because its source had no value, so the declaration —
and no incidental invalidation order — is what pulls the sender current. Four
checks, none failing.
