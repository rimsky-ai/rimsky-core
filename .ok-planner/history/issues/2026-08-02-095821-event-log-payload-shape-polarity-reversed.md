---
issue: event-log-payload-shape-polarity-reversed
kind: audit
category: decision-drift
artifacts:
  - decision:event-log-payload-shapes
status: repaired
opened: 2026-08-02T09:58:21Z
---

# Does the typed-oneof/free-form-JSON split favor signal-class events, or operational events?

Operational events. `events.proto`'s `Event.payload` oneof carries typed structs for a settled subset of operational kinds (`StateTransitionPayload`, `WorkStartedPayload`, `LockAcquiredPayload`, `ClaimAcquiredPayload`, and more); every signal-class kind (`terminal/*`, `transient/*`, `attribute/*`) is excluded by construction and falls to free-form `payload_raw`. This is mechanically enforced by `TestEventPayloadOneof_ExcludesSignalClassKinds` (`lib/protocols/proto/v1/gen/event_payload_split_test.go`), whose own comment says the oneof is "reserved for operational event kinds" — the opposite polarity from what `decision:event-log-payload-shapes`'s Choice stated. Rimsky's internal write/read path (`EventRow.Payload`, `EventAppendInput.Payload`, `signal.Signal.Payload`) is free-form `map[string]any` JSON for both event classes regardless of the proto oneof — no production code constructs the proto `Event` message today.

Rule that determined the fix: outcome-2 corpus-side repair — a fitness test mechanically pins the settled polarity; the decision's prose had it backwards (an authoring transcription error), not a later intentional pivot. No commitment changes: the split itself (some typed structure, some free-form) stands, only which side gets which shape is corrected.

Changed: `.ok-planner/design/decisions/event-log-payload-shapes.md` — Choice, Rationale, and Alternatives rewritten to match the enforced polarity, and noted the oneof is not currently wired into any internal write/read path.
