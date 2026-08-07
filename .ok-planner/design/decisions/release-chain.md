---
decision: release-chain
---

# Shared release chain

## Choice

Lint → license lint → build the core images → build the bundled-service images → run the full test suite → scan the built images → push the images.

## Rationale

Comprehensive pre-push verification; images get built before the test suite runs so the scenario tests can drive the locally-built image set.

## Alternatives

- Tests before image builds (the conventional order) — rejected: the scenario suites consume the locally-built image set, so the images must exist first.
- Separate bespoke chains for the formal and dev release flows — rejected: two chains drift; both flows share this one.
