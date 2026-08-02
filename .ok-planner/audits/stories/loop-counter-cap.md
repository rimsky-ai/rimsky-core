---
audit: loop-counter-cap
artifact: story:loop-counter-cap
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:29:05Z
---

# Bundled loop-counter utility node bounds iteration via max-count and loop/done tags

Supported. `lib/runtime/executor/builtin/loop_counter` implements exactly this contract: `schema.go` declares a required `max` (JSON-Schema `minimum: 1`) input attribute and a read-only `count`; `handler.go`'s `Execute` requires `max`, rejects `max < 1`, increments the carried-forward `count`, and emits the `loop` tag while the new count is below `max` and the `done` tag once it reaches `max`. The handler is registered as a bundled builtin utility kind (`lib/runtime/executor/builtin/builtins.go`, keyed by `loop_counter.KindName`/`ExecutorAlias`, no custom executor authored by the template). `TestLoopCounterCapE2E` (`test/scenarios/loop_counter_cap_e2e_test.go`) drives a self-subscribed counter node through 3 dispatches end-to-end and asserts 2 `loop`-tagged and 1 `done`-tagged `terminal/success` events, with downstream `loop_sink`/`done_sink` nodes firing on the respective tags — confirming both the bounded-iteration behavior and that no custom executor is required.
