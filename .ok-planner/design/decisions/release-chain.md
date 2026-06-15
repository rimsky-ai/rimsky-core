---
decision: release-chain
status: as-is
---

# Shared release chain

## Choice

Lint → license lint → build the core images → build the bundled-service images → run the full test suite → scan the built images → push the images.

## Rationale

Comprehensive pre-push verification; images get built before the test suite runs so the scenario tests can drive the locally-built image set.
