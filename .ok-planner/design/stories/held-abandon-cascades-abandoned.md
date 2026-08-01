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

