---
issue: root-usage-omits-a-conformance-suite
kind: human
category: cli
artifacts:
  - decision:conformance-suite-per-protocol
  - concept:conformance
  - story:lifecycle-subscriber-author
  - decision:cli-verb
status: repaired
opened: 2026-08-04T20:55:27Z
github: https://github.com/rimsky-ai/rimsky-core/issues/41
---

# `rimsky help` lists seven conformance suites; the dispatcher accepts eight

## Question

`cmd/rimsky/main.go::printRootUsage` printed seven `conformance`
subcommands while `cmd/rimsky/conformance.go`'s `dispatchConformance`
switch accepts eight (`lifecycle-subscriber` missing from root usage only).
Should root usage list all eight, and can drift like this be prevented
mechanically?

## Repair

`concept:conformance`'s invariant — "Every protocol carrying a conformance
suite reaches it through exactly one CLI entry point: its own subcommand"
— together with the plain fact that `dispatchConformance` already accepts
`lifecycle-subscriber` (`cmd/rimsky/conformance.go:66-67`) and
`printConformanceUsage` already lists it (`cmd/rimsky/conformance.go:79`)
fully determines the fix: root usage must list the same eight suites the
dispatcher accepts. No commitment changes — this is a print-string bug in
one help block, not a design question; `decision:conformance-suite-per-protocol`
and `decision:cli-verb` are unaffected.

Changes (code-side):
- `cmd/rimsky/main.go::printRootUsage` — added `lifecycle-subscriber` to the
  `conformance` line so root usage names all eight suites
  (`cmd/rimsky/main.go:366-369`).
- `cmd/rimsky/conformance_test.go` — added
  `TestRootUsageListsEveryConformanceSubcommand`, asserting root usage
  contains every entry in the existing `conformanceSubcommands` table (the
  same table `TestConformanceSubcommandsRegisterDocumentedFlags` and
  friends already drive), so the two lists cannot silently diverge again.

Verified: `go build ./cmd/rimsky/...` clean; `go vet ./cmd/rimsky/...`
clean; `gofmt -l` clean on both changed files; `go test ./cmd/rimsky/...
-run 'TestRootUsageListsEveryConformanceSubcommand|TestConformanceSubcommands|TestNoProtocolSuiteHangsOffAnotherProtocolsSubcommand'`
passes, including the new test.
