---
audit: subclaims-as-input
artifact: decision:subclaims-as-input
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:18Z
---

# DispatchChildren consumes already-acquired sub-claims; it never calls the producer's split-scope verb itself

Supported. `lib/runtime/child_execution.go::DispatchChildren` takes `ChildExecutionInput.Partitions []PartitionDescriptor`, where each descriptor carries only a `PartitionKey` and an already-populated `SubClaimHandleID` — the function reads and binds these ids but contains no call to any producer client. Sub-claim acquisition itself lives in `lib/runtime/runner_subclaim.go::AcquireSubClaims`, which calls `producer.SplitScope` directly; `lib/runtime/fanout_dispatch.go::FanOutPartitions` converts the already-acquired `[]SubClaim` result into the `[]PartitionDescriptor` that `DispatchChildren` then consumes as input, matching the claimed ownership split (fan-out owns acquisition, the dispatch primitive owns run-side dispatch). `test/scenarios/fanout_success_cascade_e2e_test.go` and `fanout_callback_determinism_e2e_test.go` exercise this composed path end-to-end through real fan-out dispatch with claim producers.
