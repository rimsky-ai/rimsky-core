---
decision: release-chain
status: as-is
---

# Shared release chain

## Choice

Lint → license lint → build the core images → build the bundled-service images → run the full test suite → run the dedicated repeated race-detection gate (see `decision:race-gate-split`) → scan the built images → push the images.

## Rationale

Comprehensive pre-push verification; images get built before the test suite runs so the scenario tests can drive the locally-built image set; the repeated race-detection gate's repetition budget is reserved for release time rather than every everyday run.

## Alternatives

- Tests before image builds (the conventional order) — rejected: the scenario suites consume the locally-built image set, so the images must exist first.
- Separate bespoke chains for the formal and dev release flows — rejected: two chains drift; both flows share this one.
