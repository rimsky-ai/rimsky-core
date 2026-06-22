---
story: held-abandon-cascades-abandoned
status: as-is
---

# Held work cascades terminal/error/abandoned when auto-terminal abandons

## Role

As a template author whose downstream node must react when an upstream's held work is rolled back, I can know that my downstream subscriber sees a `terminal/error/abandoned` signal from the upstream at the moment auto-terminal abandons. I can subscribe to that signal (or to the broader `terminal/error/*` pattern) and act on the rollback.

## Capability

When a held node-run's auto-terminal handler resolves to Abandon, the run transitions `held → failed` AND the cascade walker fires a `terminal/error/abandoned` signal downstream at that moment. The signal is shaped uniformly with other error signals (`terminal/error/<class>` with `class=abandoned`), so existing wildcard subscriptions (`terminal/error/*`) match it.

## Business value

Without an abandoned-signal, downstream subscribers have no way to detect that upstream held work was rolled back. They either keep relying on stale state (if they previously cascaded on the held terminal — but cascade-defer-on-held prevents that) or they see nothing at all and never know to compensate. With the abandoned-signal, downstream can subscribe specifically to `terminal/error/abandoned` for rollback compensation, or to `terminal/error/*` for broader error handling that includes rollback.

## Acceptance

An author writes a graph A → B where A holds a claim and B subscribes to A's `terminal/error/abandoned`. The test asserts B does NOT dispatch on A's held terminal. The test then triggers auto-terminal abandon on A's held claim. B dispatches at this moment, with its bag reflecting A's pre-abandon state and an event in B's lineage tying the dispatch to the abandoned signal. Observable as: B's executor invocation comes only after auto-terminal abandon, and the wait-set row for B references the `terminal/error/abandoned` signal from A.

## Falsifier

B is NOT dispatched after auto-terminal abandon — observable by checking B's lineage for any post-abandon dispatch — OR B is dispatched on A's held terminal before the abandon — observable by comparing B's first dispatch timestamp against the abandon event timestamp.

## Proof

An executable scenario test where A holds a claim, B subscribes to A's `terminal/error/abandoned`, A returns its held terminal (B does NOT dispatch), auto-terminal abandon fires on A, the test asserts B dispatches with a wait-set row referencing the abandoned signal.
