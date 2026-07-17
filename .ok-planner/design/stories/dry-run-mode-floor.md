---
story: dry-run-mode-floor
status: as-is
---

# Operator mints attempt-only key

## Role

As an operator delegating control-plane access to an autonomous agent, I can mint an api-key whose grant pins a write action to dry-run mode — provided the key holds no other grant that authorizes execute-mode on that same action — so that I have attempt-only credentials that are safe to hand out.

## Capability

Identity-bound dry-run floor: permission evaluation is set membership, so a key's effective mode on an action is the floor of whichever matching grant governs the request and the request's own dry-run flag. A key whose only grant matching an action declares dry-run mode pins that action to dry-run regardless of the request flag; a key that also holds a separate grant authorizing execute-mode on the same action is not pinned — the floor is per matching grant, not an absolute override across every grant the key holds.

## Business value

Operators can hand out attempt-only credentials safely — to autonomous agents or untrusted tooling — without trusting the caller to set a per-request flag, as long as they don't also grant that same key unrestricted access to the action.

## Acceptance

An operator mints an api-key whose only grant matching a write action declares dry-run mode; using that key, an operator or agent issues a write request without the per-request dry-run flag and receives the synthetic envelope back; no row is persisted; the audit log records the attempt with executed-false. A second ordinary write-capable key issued by the same operator performs the same request and creates a real row — proving the floor is carried by key identity, not by the request flag. A key holding both a dry-run grant and a separate execute-mode grant for the same action is not held to the floor by that dry-run grant.

## Falsifier

A dry-run-pinned key's write actually persists state, OR the audit misses the attempt, OR no comparison shows the floor is identity-bound.

## Proof

Executable proof.
