---
issue: releasing-md-chain-order-contradicts-makefile
kind: human
category: doc-drift
artifacts:
  - decision:release-chain
status: repaired
opened: 2026-08-07T08:49:23Z
github: https://github.com/rimsky-ai/rimsky-core/issues/76
---

# Does RELEASING.md state the release chain in an order that contradicts the Makefile?

Yes, confirmed on the current tree: `RELEASING.md` stated the chain (in two
places) as `lint → license-lint → test-all → core-images → service-images →
scan → push-images`, putting `test-all` before the image builds. The
Makefile's `release` target is `lint core-images service-images test-all
scan push-images` — images built before tests — and `license-lint` is not a
separate top-level step; it runs as a prerequisite of `lint`
(`lint: license-lint`).

`decision:release-chain`'s Choice is the authoritative order — "Lint →
license lint → build the core images → build the bundled-service images →
run the full test suite → scan the built images → push the images" — with
the Rationale explicit about why: "images get built before the test suite
runs so the scenario tests can drive the locally-built image set." The
Makefile already encodes this correctly; only `RELEASING.md`'s prose was
stale. The rules left exactly one compliant text: match the decision doc
and the Makefile. No commitment changed.

**Change:** `RELEASING.md` — both the sub-step-7 parenthetical and the
"Shared chain" section now read `lint (incl. license-lint) → core-images →
service-images → test-all → scan → push-images`, with the `lint` bullet
noting it includes `license-lint` and the `core-images`/`service-images`
bullet noting the ordering reason (scenario tests consume the locally-built
image set).

**Verified:** diffed the corrected order against
`Makefile:411` (`release: lint core-images service-images test-all scan
push-images`) and `decisions/release-chain.md`'s Choice — exact match.
Docs-only change; no build/test run needed.
