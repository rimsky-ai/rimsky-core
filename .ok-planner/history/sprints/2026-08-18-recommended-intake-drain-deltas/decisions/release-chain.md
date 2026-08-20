---
decision: release-chain
---

# Shared release chain

## Choice

Lint → license lint → build the core images → build the bundled-service images → run the full test suite → scan the built images → push the images → read each pushed tag's published platforms back from the registry and fail on a mismatch.

## Rationale

Comprehensive pre-push verification; images get built before the test suite runs so the scenario tests can drive the locally-built image set. The read-back checks the one property the earlier steps cannot see: what the registry holds. An image that lost its platform list still passes every local gate, so only the registry answers the question.

## Alternatives

- Tests before image builds (the conventional order) — rejected: the scenario suites consume the locally-built image set, so the images must exist first.
- Separate bespoke chains for the formal and dev release flows — rejected: two chains drift; both flows share this one.
- Trust the push flags to carry the platform list and verify nothing — rejected: a dropped flag ships a single-platform image with no signal.
