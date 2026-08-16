---
audit: testing-scenario-based-e2e
artifact: decision:testing-scenario-based-e2e
text: noncompliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:49:58Z
checked: 10
unaccounted: 0
---

# The end-to-end suites, the container-backed persistence tests, the wait-helper shape, and the hang backstop

Supported on every clause. The two scenario collections the choice names exist and drive the assembled product: 160 entries in the root test group's scenarios directory against a booted stack, and 35 in the services module's scenarios test directory against real images. Persistence and stack tests boot real backends through container helpers layered over testcontainers — a shared pooled Postgres for the foundation suites and per-network containers for the services harness. Every wait helper in both harnesses was enumerated and checked: all 10 are poll-until-success loops with no exit other than the awaited condition, and each reports expected-versus-observed state descriptively, either by logging the last observation every Nth poll or, for the one helper bounded by the caller's context, by naming the expectation and the last observation in its error when the run is cut short. The suites carry no per-package time ceiling: every module's test target runs through the guard, which forces the runner's timeout to zero, and a pin test fails the build if the makefile or either CI workflow declares a non-zero one. Hang detection lives in that guard, which parses the runner's JSON event stream, kills the process group only after a configurable no-progress window with a 20-minute default, and exits with a distinct inconclusive code rather than reporting a test failure.

## Compliance

Lumps unrelated choices into one file, against "one decision per choice": the end-to-end scenario shape, the container-backed persistence-test posture, the poll-until-success wait-helper shape, and the no-ceiling-plus-progress-guard hang backstop are four separate choice points, and the title names a topic ("Testing discipline") rather than a choice. Compliant text is four decisions, each titled with its own choice and each carrying the alternative it was taken over — the Alternatives section here offers alternatives only for the last of the four, leaving the other three with no identifiable alternative, which is the definition of a default rather than a decision.
