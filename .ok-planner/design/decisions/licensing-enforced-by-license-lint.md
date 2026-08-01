---
decision: licensing-enforced-by-license-lint
status: as-is
---

# License-import discipline

## Choice

Enforced by a build-step license check that constrains Apache-licensed packages to import only the standard library plus permissive-licensed and Apache-licensed dependencies (see `concept:module-layout`).

## Rationale

Prevent AGPL contamination of permissive consumers.

## Alternatives

- Rely on the module split and code review to keep copyleft imports out of the permissive surface — rejected: nothing fails mechanically when a contaminating import slips in.
- Enforce with import-path deny rules alone — rejected: blind to the licenses of third-party dependencies, which are exactly where contamination enters unnoticed.
