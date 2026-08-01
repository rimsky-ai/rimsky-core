---
decision: build-tool-makefile
status: as-is
---

# Build orchestration

## Choice

A Makefile at the repo root is the single source of truth for build orchestration — builds, tests, lint, image builds, and the release chain all run as its targets.

## Rationale

Shell-native and universally available; contributors and CI need no extra tooling installed, and targets compose the same commands a developer would run by hand.

## Alternatives

- A Go-native task runner (Mage, Task) — rejected: adds a tool dependency for what shell recipes already express.
- Ad-hoc per-task shell scripts — rejected: no shared dependency graph and no single discoverable target list.
