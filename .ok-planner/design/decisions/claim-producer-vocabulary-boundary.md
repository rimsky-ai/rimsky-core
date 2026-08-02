---
decision: claim-producer-vocabulary-boundary
---

# The claim-producer rename stops at the shipped surface

## Choice

The store→claim-producer vocabulary sweep covers every shipped, user-facing surface: binaries, entrypoints, config grammar, example templates, and every name a template author or operator observes. Internal test machinery is exempt and keeps its existing names — the test-fake helper package, test-fixture names, harness database names, container-internal mount paths. The per-producer internal storage layer keeps its separate "store" sense (each claim producer's own storage packages): storage-layer naming, a different word doing a different job, not producer vocabulary.

## Rationale

No template author or operator ever observes internal test names, so renaming them buys no user-facing consistency while churning many test files; and the tree already tolerates storage-layer "store" beside claim-producer vocabulary by design, so the exemption is consistent with an existing boundary rather than a new inconsistency. Writing the boundary down is the point: without it, the leftover names refile as an audit finding every cycle.

## Alternatives

- Extend the sweep into the test machinery — rejected: invasive churn across many files with zero user-observable benefit.
- Leave the boundary undocumented — rejected: the question has no home and reopens indefinitely.
