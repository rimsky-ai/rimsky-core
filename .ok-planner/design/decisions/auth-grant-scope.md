---
decision: auth-grant-scope
---

# Per-grant scope dimensions

## Choice

A per-grant scope map of action-specific dimension keys (e.g., template tag) constraining the action (see `concept:permission`).

## Rationale

Least-privilege delegation across resource lifecycle.

## Alternatives

- Flat per-action grants with no dimensions — rejected: cannot confine a key to a subset of resources, forcing over-broad credentials.
- Per-resource ACLs — rejected: scoping by instance identifier breaks down for resources created after the grant; dimension keys constrain by property instead.
