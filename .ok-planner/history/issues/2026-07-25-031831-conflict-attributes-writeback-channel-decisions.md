---
issue: conflict-attributes-writeback-channel-decisions
kind: audit
category: conflicting
artifacts:
  - decision:uniform-attributes-delta
  - decision:writeback-bumps-progress
  - decision:keepalive-endpoint
status: repaired
opened: 2026-07-25T03:18:31Z
---

# Does the mid-dispatch attribute writeback channel still exist, or was it retired?

`decision:uniform-attributes-delta` stated the mid-dispatch attribute writeback callback "is retired" and that the run-terminating verdict is "the only channel an executor uses to mutate node attributes." That is false against the code: `lib/runtime/callback.go` wires `POST /v1/runs/{run_id}/attributes` to `handleAttributeWriteback` (`lib/runtime/attribute_writeback.go`), which merges `attributes_delta` into the per-run attribute row and bumps `last_progress_at` — exercised by `lib/runtime/attribute_writeback_test.go` and `test/scenarios/dispatch_input_bag_survives_writeback_test.go`, and it is the exact route `decision:writeback-bumps-progress` and `decision:keepalive-endpoint` describe as live and current.

The rules determine the fix and it changes no commitment: `decision:writeback-bumps-progress`, `decision:keepalive-endpoint`, and the running, tested code already agree the mid-dispatch channel exists; only `decision:uniform-attributes-delta`'s retirement sentence contradicted them. Repaired per the mechanical-vs-judgment rule's named example — aligning a stale sentence to the commitment the code and the counterpart artifacts already agree on, not inventing or removing a channel.

Changed `.ok-planner/design/decisions/uniform-attributes-delta.md`:
- Choice: replaced the "is retired... only channel" sentence with a statement that the terminal `attributes_delta` channel coexists with the mid-dispatch writeback callback, distinguished by the fact that only the terminal channel emits a signal/cascade-fires (verified in `lib/runtime/attribute_writeback.go`: the writeback handler merges into the attribute row and bumps progress but calls no cascade walker).
- Alternative B: reworded to no longer assert the mid-dispatch channel is retired or that it "keeps the executor's mutation surface to one channel"; now states why terminal-verdict `attributes_delta` (not the mid-dispatch callback) is what a signal predicate needs.

Verified via code reading only (`lib/runtime/callback.go`, `lib/runtime/attribute_writeback.go`); docs-only change, no build/test impact.
