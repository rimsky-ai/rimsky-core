---
audit: uniform-attributes-delta
artifact: decision:uniform-attributes-delta
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:40Z
---

# attributes_delta is a feature of run-terminating verdicts only

Supported. `lib/protocols/proto/v1/executor.proto`'s `Success` and `Error` messages both carry an `attributes_delta` field (`google.protobuf.Struct`), while `Park` carries no such field at all (its only substantive field is `resume_at`; the reasons/payload/session-token fields are explicitly `reserved`, not repurposed for attributes). `lib/runtime/runner_dispatch.go::readExecutorOutcome` reads `AttributesDelta` off `Outcome_Success`/`Outcome_Error` into a shared `terminalEvent.AttributesDel` and never reads any such field off `Outcome_Park`. `lib/runtime/runner_terminal.go::applyTerminalComplete` merges the delta into the per-run attribute row (`mergeAttributesDelta`, citing the decision) inside the same DB transaction that commits the terminal verdict, and both the success and error signal builders (`signalpkg.BuildTerminalSuccessSignal` / `BuildTerminalErrorSignal` in `lib/runtime/signal_for_terminal.go`) embed the same delta on the emitted signal's payload, so a subscription's CEL predicate can match `payload.attributes_delta.<key>` identically for both kinds — exercised end to end by `test/scenarios/uniform_attributes_delta_subscription_test.go`. The separate mid-dispatch writeback channel (`lib/runtime/attribute_writeback.go`) merges into the attribute row but never emits a signal, matching the decision's claim that it cannot substitute for verdict-carried `attributes_delta`.
