---
audit: release-chain
artifact: decision:release-chain
determination: supported
commit: b767a27d
audited: 2026-08-02T09:40:22Z
---

# The shared release chain runs lint through push in the decided order

Supported. The Makefile's `release` target is declared as `lint core-images service-images test-all test-race scan push-images`, with `lint` itself depending on `license-lint` so the license boundary check runs ahead of the golangci-lint recipe; `test-all` in turn depends on `core-images service-images test-images` so the scenario suites see the locally-built image set before running. Both `make dev-release` (via `tools/dev-release.sh`, which shells to `LATEST_TAG=dev VERSION=... make release`) and the `/release` skill's automated pipeline invoke this one target rather than separate chains. `test/plumbline/build_chain_test.go::TestReleaseChainOrder` parses the Makefile's `release:` dependency line and fails if the order deviates from `lint core-images service-images test-all test-race scan push-images`.
