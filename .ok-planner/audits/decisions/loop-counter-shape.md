---
audit: loop-counter-shape
artifact: decision:loop-counter-shape
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# loop_counter is a carry-forward counter with a strictly-positive max and two Success-outcome tags

Supported. `lib/runtime/executor/builtin/loop_counter/schema.go` declares `max` required with `minimum: 1` (strictly positive) and `count` as `readOnly` (executor-owned); `handler.go` rejects `max < 1` and any non-integer `max`/`count`, reads the incoming `count` from the dispatch attribute bag (carry-forward), and returns the new count via `Success.AttributesDelta` (the outcome's attributes delta) rather than any side-channel — this is the wire mechanism the decision names. The handler emits exactly one of two tags (`loop` while below max, `done` at/after max) on every `Success` outcome, matching the "two declared tags on the Success outcome" clause; `handler_test.go`'s `TestExecute_TagsAndDeltaAcrossBoundary` and `TestExecute_RedispatchAfterDoneKeepsEmittingDone` verify the tag/count boundary behavior directly, and `test/scenarios/loop_counter_cap_e2e_test.go` verifies the carry-forward actually round-trips through the runtime's real dispatch-to-dispatch attribute persistence within one frame (the decision's intra-frame/per-RunScope carry-forward scope), checked against 1 story test plus 5 unit-test cases spanning the max/count boundary.
