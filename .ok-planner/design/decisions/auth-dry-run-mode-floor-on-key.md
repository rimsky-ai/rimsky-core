---
decision: auth-dry-run-mode-floor-on-key
status: as-is
---

# Identity-bound dry-run

## Choice

A grant whose mode is dry-run pins the key to dry-run regardless of request flag.

## Rationale

Attempt-only credentials, identity-bound.

## Alternatives

- Treat the grant's mode as a default the request flag may override — rejected: an attempt-only credential must not be escalatable to real writes by its holder.
