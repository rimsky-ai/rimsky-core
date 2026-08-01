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

