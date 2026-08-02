---
issue: cli-archive-release-channel-untested
kind: audit
category: test-coverage
artifacts:
  - decision:release-distribution
status: repaired
opened: 2026-08-02T09:58:16Z
---

# The goreleaser-built CLI/GitHub-Release distribution channel had no test or CI coverage

## Question

Does `.goreleaser.yaml` match `decision:release-distribution`'s CLI-archive specifics (Linux/macOS on amd64/arm64, no Windows, per-archive SBOMs), and does it carry regression coverage the way the other three named channels (images, npm, Go modules) do?

## Repair

Re-verified: `.goreleaser.yaml` already matches the decision exactly (`goos: [linux, darwin]`, `goarch: [amd64, arm64]`, `sboms:` over `archive` artifacts, no windows target). The gap was purely missing verification, not a behavioral mismatch — a Plumbline "Mechanical Checks" case, matching the regex/shape-test pattern `test/plumbline/build_chain_test.go` already applies to the other release-chain decisions. Adding a shape test locks in a configuration that already matches the decision; it changes no commitment. (The issue's second candidate — a live `goreleaser build --snapshot` CI job — goes beyond what any of the other three channels' coverage does, and beyond what the decision itself claims about CI; not added, to keep parity with sibling coverage rather than exceeding it.)

Changed: `test/plumbline/build_chain_test.go` — added `TestGoreleaserCLIArchiveChannelMatchesDecision`, citing the decision, asserting (1) `goos` is exactly linux+darwin, (2) no windows target, (3) `goarch` is exactly amd64+arm64, (4) an SBOM is published over archive artifacts.

Verified: `go test ./test/plumbline/... -v` — new test passes; full `test/plumbline` package still passes; `gofmt -l` clean on the changed file.
