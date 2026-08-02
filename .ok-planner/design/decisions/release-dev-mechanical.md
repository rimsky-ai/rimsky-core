---
decision: release-dev-mechanical
---

# Dev-release flow

## Choice

The dev-release flow is mechanical: no SemVer judgment, no notes, version derived as the next minor pre-release with a dev pre-release suffix that includes a build date and the current commit hash.

## Rationale

Continuous internal channel without ceremony: a dev build is throwaway, so nothing in it repays SemVer judgment or notes drafting, while the derived version keeps every build traceable to its commit.

## Alternatives

- Running the formal release ceremony for dev builds — rejected: SemVer judgment and notes drafting cost human attention a throwaway build doesn't repay.
- Publishing dev builds under only a floating tag with no derived version — rejected: a mutable tag alone leaves no way to trace a running build back to its commit.
