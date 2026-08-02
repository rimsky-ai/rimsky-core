---
issue: lifecycle-subscriber-conformance-subcommand-missing
kind: audit
category: decision-drift
artifacts:
  - decision:conformance-suite-per-protocol
status: repaired
opened: 2026-08-02T09:58:27Z
---

# Does lifecycle-subscriber have its own `rimsky conformance` subcommand?

`decision:conformance-suite-per-protocol` requires one conformance suite
per protocol "exposed as a per-protocol conformance subcommand on the
CLI." Re-verification confirmed lifecycle-subscriber has a real,
standalone-capable suite (`RunLifecycleCheck` in
`lib/protocols/conformance/executor/lifecycle_check.go`) but it was
reachable only via a `--check-lifecycle` flag bolted onto the unrelated
`rimsky conformance executor` subcommand — the other five
rimsky-implementable protocols each got their own subcommand.

Rule that determined the fix: the decision already commits to a
subcommand per protocol; lifecycle-subscriber was the one protocol not
yet matching it. Adding the subcommand realizes the existing commitment
without changing it — outcome 2 — and per the issue's own first
candidate, the old flag is kept as a deprecated alias rather than
removed, so no existing invocation breaks.

What changed: added `rimsky conformance lifecycle-subscriber`
(`runConformanceLifecycleSubscriber` in `cmd/rimsky/conformance.go`,
`--endpoint`/`--transport`/`--timeout`/`--tls`) wrapping the same
`conformance.RunLifecycleCheck`, wired into `dispatchConformance` and
`printConformanceUsage`; relabeled `executor`'s `--check-lifecycle` flag
help text as a deprecated alias. Added a `lifecycle-subscriber` row to
the `conformanceSubcommands` table in `cmd/rimsky/conformance_test.go`,
which drives the existing generic flag/routing/required-input tests for
every subcommand.

Verified: `go build ./cmd/...` clean; `go test ./cmd/rimsky/ -run
"TestConformance|TestDispatchConformanceRouting|TestReportConformanceResults"`
passes, including the new `lifecycle-subscriber` cases in all four
table-driven tests; `go test ./cmd/rimsky/ -short` (full package minus
the docker-dependent `TestCtxDemo`) passes.
