---
audit: timeout-flag
artifact: decision:timeout-flag
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:44:00Z
---

# The wall-clock timeout's opt-in default across the run-to-terminal verbs

Supported. Both run-to-terminal verbs register a duration timeout flag defaulting to zero and document zero as unbounded, and both honour it: the one-shot wait arms a timer only for a positive value and otherwise selects on a nil channel that can never fire, and the remote cleanup loop computes a deadline only for a positive value and otherwise never compares against one. No default cap is applied anywhere, so absence means as long as it takes, and the rejected raise-or-disable default does not exist.
