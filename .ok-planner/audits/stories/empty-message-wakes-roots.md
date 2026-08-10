---
audit: empty-message-wakes-roots
artifact: story:empty-message-wakes-roots
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T05:30:00Z
---

# One untyped send wakes all three structural roots

Supported. A template with three structural roots, one node downstream of a root,
and one node holding a declared upstream was driven through the control API by a
single message send whose request body was `{}` — no type, no envelope fields.
All three roots dispatched exactly once, the downstream node ran by cascade, and
the node with a declared upstream never dispatched, so the wake reached the roots
and only the roots. The send returned the same message identity every typed send
returns, the row sits in the same ledger carrying the empty type, and it opened a
frame as its triggering message, so the untyped send is the ordinary delivery
path rather than a side entrance.
