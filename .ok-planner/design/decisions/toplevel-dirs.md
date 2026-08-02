---
decision: toplevel-dirs
---

# Idiomatic top-level code directories

## Choice

Four top-level code directories split the repo by role: binaries (the cmd group), shippable library code (the lib group), out-of-tree tests and their machinery (the test group), and dev tooling (the tools group).

## Rationale

Conventional Go layout; a clear binary vs. lib vs. test vs. dev-tooling split gives the import-boundary lint stable directory roots to hang rules on.

## Alternatives

- Flat root-level packages — rejected: binaries, shippable code, test machinery, and dev tooling interleave with no mechanical boundary for the dependency lint.
- The `pkg/` wrapper convention for library code — rejected: an extra path segment with no grouping benefit over a single library root.
