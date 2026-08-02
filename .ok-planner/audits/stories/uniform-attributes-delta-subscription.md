---
audit: uniform-attributes-delta-subscription
artifact: story:uniform-attributes-delta-subscription
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:40Z
---

# Subscriber predicates on verdict attributes across terminal kinds

Supported. Both `Success` and `Error` messages in `lib/protocols/proto/v1/executor.proto` carry an `attributes_delta` field on the wire; `lib/runtime/runner_dispatch.go::readExecutorOutcome` copies either into a common `terminalEvent.AttributesDel`, which `lib/runtime/signal_for_terminal.go` embeds into the emitted `terminal/success` or `terminal/error/<class>` signal payload via `signalpkg.BuildTerminalSuccessSignal` / `BuildTerminalErrorSignal`, making `payload.attributes_delta.<key>` available to a subscription's CEL `when:` clause identically for both kinds. `test/scenarios/uniform_attributes_delta_subscription_test.go` (carrying the story's citation on all three cases) proves this directly: one subscription with a single `payload.attributes_delta.trigger == "yes"` predicate fires a handler node when the producer succeeds and again when the producer errors, and a third case proves the predicate is not vacuously true by showing a non-matching delta value suppresses the fire while an unconditional sibling subscription to the same producer still proceeds.
