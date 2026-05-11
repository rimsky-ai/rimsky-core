---
tension: quality-rule-custom-handler-ordering
category: unspecified
status: open
affects:
  - quality-rule
---

# Custom quality-rule handlers must be registered before any template that references them is loaded — but no contract enforces this

## What is muddy

`modeling/qualityrule/eval/rules.go:15-26` exposes `Register(name, ev)` against a process-global `sync.RWMutex`-guarded `map[string]Evaluator`. Three builtins (`row_count_ratio`, `no_nulls`, `nullable_fields_present`) self-register at package `init()`. Consumers may register additional evaluators under arbitrary names. The literal name `custom` is reserved by the registry: a lookup for `custom` returns "no custom handler registered" rather than the generic "unknown rule type" error.

There is no documented or enforced ordering between (a) `eval.Register(name, ev)` calls in the consumer's `main()` and (b) template load / instance creation paths that may already reference the rule type. A consumer that loads template specs at process start but registers custom handlers later in `main()` — natural code shape — opens a window where commits silently fail with "no custom handler registered" until the registration happens.

## Why it matters

The contract for registration timing is implicit: it relies on the consumer ordering their `main()` such that all `eval.Register` calls precede any code path that can invoke `EvaluateAll`. A subtle re-shuffle of init order (e.g. adding a new background worker that touches templates before the registration block runs) produces sporadic commit failures that look like a runtime bug, not a startup-order bug. Adjacent to `lifecycle-subscriber` (which has the same shape: peer must register before the event arrives), but quality-rule registration has no equivalent of the LifecycleSubscriber's startup handshake.

## Resolution candidates (do NOT pick)

- Validate at template registration that every referenced rule type is registered, and re-validate when new templates land.
- Provide a startup-completion barrier the consumer calls after all `Register` calls; reject `EvaluateAll` until the barrier is past.
- Document the ordering requirement at the top of `modeling/qualityrule/eval/rules.go` and in `docs/concepts/quality-rule.md`.

## Evidence

- `_discover/quality-rules-and-attribute-validation.md` Observations bullet "custom-handler lifecycle".
- `modeling/qualityrule/eval/rules.go:15-26,44-49` — the registry and reserved-name handling.

