---
story: audit-log-read
status: as-is
---

# Operator reads auth-relevant action audit

## Role

As an operator, I can read the audit log of every auth-relevant action against the deployment — key creates, revokes, rotates, dry-run-mode access attempts, denied attempts — with filtering, so that I see who did what to the rimsky stack and when.

## Capability

An audit-read surface gated on an audit-read permission, exposing every auth-relevant action with actor identity, action name, outcome, target, and time.

## Business value

Operators see who did what against the rimsky stack and when — a forensic record for compliance and incident review.

## Acceptance

Through the audit-read surface (gated by `audit:read`), after an admin mints / revokes / rotates keys and a non-admin caller triggers an access denied, the audit log returns each event in timestamp order carrying actor identity, action name, outcome, and resource target.

## Falsifier

A real access denied doesn't appear in the audit, OR dry-run-mode attempts are absent, OR actor identity is dropped from the record.

## Proof

Executable proof.
