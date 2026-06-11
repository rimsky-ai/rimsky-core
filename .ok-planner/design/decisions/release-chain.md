---
decision: release-chain
status: as-is
---

# Shared release chain

## Choice

`lint → license-lint → core-images → service-images → test-all → scan → push-images`.

## Rationale

Comprehensive pre-push verification; images get built before the test suite runs so the scenario tests can drive the locally-built image set.
