---
audit: resume-preserves-snapshot
artifact: story:resume-preserves-snapshot
determination: supported
commit: b767a27d
audited: 2026-08-02T09:32:02Z
---

# Resumed dispatch replays the dispatch-time substitution snapshot, not freshly re-resolved upstream values

Supported. Attribute resolution for a dispatch (including a resumed one) reads from a per-run `SetDispatchInputBag`/`GetDispatchInputBag` snapshot (`lib/foundation/persistence/node_attributes.go`) rather than recomputing substitutions from the current upstream attribute rows; `resolveAttributesCore` (`lib/runtime/runner_dispatch.go`, annotated `@story: resume-preserves-snapshot`) loads that snapshot via `loadDispatchBag` before building the resolved context, and the deadline-wake path (`wake_parked.go`) transitions the parked row to `stale` for redispatch on the same row rather than rebuilding it. The claim is exercised end to end by `test/scenarios/resume_preserves_snapshot_test.go` (`TestResumePreservesSnapshot_DeadlineWakeReusesDispatchTimeBag`): it parks a downstream node substituting an upstream attribute, mutates that upstream attribute directly in the database while the node is parked, and asserts the deadline-driven resume re-invokes the executor with the original ("initial") substituted value rather than the mutated ("updated") one.
