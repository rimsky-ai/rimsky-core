---
story: dry-run-mode-floor
status: as-is
---

# Operator mints attempt-only key

## Story

As an operator delegating control-plane access to an autonomous agent, I can mint an api-key whose grant pins a write action to dry-run mode — provided the key holds no other grant that authorizes execute-mode on that same action — so that I have attempt-only credentials that are safe to hand out.

Identity-bound dry-run floor: permission evaluation is set membership, so a key's effective mode on an action is the floor of whichever matching grant governs the request and the request's own dry-run flag. A key whose only grant matching an action declares dry-run mode pins that action to dry-run regardless of the request flag; a key that also holds a separate grant authorizing execute-mode on the same action is not pinned — the floor is per matching grant, not an absolute override across every grant the key holds.

Operators can hand out attempt-only credentials safely — to autonomous agents or untrusted tooling — without trusting the caller to set a per-request flag, as long as they don't also grant that same key unrestricted access to the action.
