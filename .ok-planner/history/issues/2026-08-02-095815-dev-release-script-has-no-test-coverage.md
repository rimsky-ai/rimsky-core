---
issue: dev-release-script-has-no-test-coverage
kind: audit
category: test-coverage
artifacts:
  - decision:release-dev-mechanical
  - decision:release-semver-sha-dot-joined
status: repaired
opened: 2026-08-02T09:58:15Z
---

# `tools/dev-release.sh` implemented two release decisions exactly but had zero test coverage

## Question

Does `tools/dev-release.sh` correctly implement `decision:release-dev-mechanical` (mechanical next-minor pre-release, no SemVer judgment, no notes) and `decision:release-semver-sha-dot-joined` (SHA dot-joined into the pre-release segment, not `+` build metadata) — and does the codebase's own mechanical-check discipline hold it to that shape the way it already does for the sibling release-chain decisions (`release-chain`, `release-attestations`, `release-scan-docker-scout` in `test/plumbline/build_chain_test.go`)?

## Repair

Re-verified: `tools/dev-release.sh` already implements both decisions exactly (`NEXT_MINOR_BASE="v${MAJOR}.$((MINOR + 1)).0"`, `DEV_VERSION="${NEXT_MINOR_BASE}-dev.${DATE}.g${SHA}"`, no SemVer prompt, no notes file). The only gap was verification, not behavior — a Plumbline "Mechanical Checks" case (every written constraint needs a check that fails on violation), the same rule the file's own sibling regex tests already follow. Adding a regex/shape test locks in behavior that already matches the decisions; it changes no commitment.

Changed: `test/plumbline/build_chain_test.go` — added `TestDevReleaseVersionIsMechanicalNextMinorDotJoinedSha`, citing both decisions, asserting (1) the next-minor base-version derivation line is present, (2) the dot-joined `-dev.${DATE}.g${SHA}` pre-release segment is present, (3) no `+`-joined SHA (build-metadata form) exists in the script.

Verified: `go test ./test/plumbline/... -v` — new test passes; full `test/plumbline` package (all pre-existing tests) still passes; `gofmt -l` and `go vet` clean on the changed file.
