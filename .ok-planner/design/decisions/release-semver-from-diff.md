---
decision: release-semver-from-diff
---

# SemVer bump source

## Choice

The SemVer bump is derived from inspecting the release diff's consumer-visible surfaces (proto, migrations, exports, CLI flags, env vars).

## Rationale

Objective and consistent: the bump is grounded in what actually changed on the surfaces consumers depend on, not in how the changes were described.

## Alternatives

- Deriving the bump from commit-message conventions — rejected: trusts message discipline rather than the actual changed surface.
- Manual bump judgment with no fixed inputs — rejected: inconsistent across releases and unreviewable after the fact.
