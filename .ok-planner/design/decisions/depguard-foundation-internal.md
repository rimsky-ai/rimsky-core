---
decision: depguard-foundation-internal
---

# The foundation module's internal package is private

## Choice

External imports of the foundation module's internal package are forbidden, enforced by dependency lint.

## Rationale

Implementation details stay behind the foundation module's public packages, which keeps them free to change without sweeping consumers.

## Alternatives

- Exposing the helpers as public foundation packages — rejected: consumers couple to implementation detail the module needs freedom to change.
- Relying on the Go toolchain's internal-visibility rule alone — rejected: the lint rule keeps every import boundary in the one authoritative enforcement surface, with a violation message naming the intended alternative.
