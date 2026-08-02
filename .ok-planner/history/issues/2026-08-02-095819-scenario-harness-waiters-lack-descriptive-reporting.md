---
issue: scenario-harness-waiters-lack-descriptive-reporting
kind: audit
category: decision-drift
artifacts:
  - decision:testing-scenario-based-e2e
status: repaired
opened: 2026-08-02T09:58:19Z
---

# Do the harness's poll-until-success wait helpers report expected-vs-observed state descriptively?

`decision:testing-scenario-based-e2e`'s Choice requires harness wait
helpers to "report the expected-versus-observed state descriptively" on
their poll loop (their only exit path). Re-verification confirmed 7 of 10
`Wait*` helpers — the 5 exported helpers in
`test/support/scenario/harness.go` plus its unexported
`waitForRootDispatch`, and `test/support/eventwait`'s `WaitForEvent` —
looped via bare `time.Sleep` with no logging, while the 3 helpers in
`lib/services/test/harness` already did this via
`PollNodeObservability`'s every-40th-poll logging pattern.

Rule that determined the fix: the decision already commits to this
behavior uniformly across all wait helpers; only 7 of 10 implementations
were missing it. Bringing them into line changes no commitment — outcome
2 (code-side repair matching an already-established, already-committed
pattern), purely additive diagnostic logging with no effect on pass/fail
verdicts or timing (no deadline was added — the suite-level timeout
remains the only backstop, per the decision's own Rationale).

What changed: added an every-40th-poll (`waitForRootDispatch`: every
100th, given its faster 20ms tick) `t.Logf` call to
`WaitForNodeState`, `WaitForEventCount`, `WaitForDispatchCount`,
`WaitForLeafRunLineageCount`, `WaitForAllRunsTerminal`, and
`waitForRootDispatch` in `test/support/scenario/harness.go` (refactoring
`nodeReachedState` to also return an observed-state description), and to
`eventwait.WaitForEvent` in `test/support/eventwait/eventwait.go` (using
`Matcher.String()` plus the last-observed match count/error, mirroring
`eventwait.Events`' existing use of `String()`).

Verified: `go build ./...` and `go vet ./...` are clean. Ran a sample of
real scenario tests exercising each touched helper —
`TestParkedLifecycleResumeOnDeadline`, `TestParkedLifecycle_TagsLandInAuditEvent`,
`TestParkedLifecycleHeldClaimRetentionAcrossPark` (WaitForEventCount,
WaitForLeafRunLineageCount, WaitForAllRunsTerminal, eventwait.WaitForEvent),
`TestSubscriptionCascade_*` and `TestCascadeModeDefaultsToMostRecentAndCoalesces`
(WaitForNodeState, waitForRootDispatch), and the new
`TestAcceptance_InstantiationStaticConfigGate` subtest (WaitForNodeState)
— all pass, with the new descriptive log lines observed firing correctly
on genuine multi-poll waits.
