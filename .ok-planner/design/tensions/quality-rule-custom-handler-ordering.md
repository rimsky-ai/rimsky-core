---
tension: quality-rule-custom-handler-ordering
category: unspecified
status: open
affects:
  - validation
  - attribute
---

# Custom quality-rule handlers must be registered before any template that references them is loaded — but no contract enforces this

## What is muddy

`graph/qualityrule/eval/rules.go` exposes `Register(name, ev)` against a process-global `sync.RWMutex`-guarded `map[string]Evaluator`. Three builtins (`row_count_ratio`, `no_nulls`, `nullable_fields_present`) self-register at package `init()`. Consumers may register additional evaluators under arbitrary names. The literal name `custom` is reserved by the registry: a lookup for `custom` returns "no custom handler registered" rather than the generic "unknown rule type" error.

There is no documented or enforced ordering between (a) `eval.Register(name, ev)` calls in the consumer's `main()` and (b) template load / instance creation paths that may already reference the rule type. A consumer that loads template specs at process start but registers custom handlers later in `main()` — natural code shape — opens a window where commits silently fail with "no custom handler registered" until the registration happens.

## Why it matters

The contract for registration timing is implicit: it relies on the consumer ordering their `main()` such that all `eval.Register` calls precede any code path that can invoke `EvaluateAll`. A subtle re-shuffle of init order (e.g. adding a new background worker that touches templates before the registration block runs) produces sporadic commit failures that look like a runtime bug, not a startup-order bug. Adjacent to `lifecycle-subscriber` (which has the same shape: peer must register before the event arrives), but quality-rule registration has no equivalent of the LifecycleSubscriber's startup handshake.

## Resolution candidates (do NOT pick)

- Validate at template registration that every referenced quality-rule type already has a registered handler, and re-validate as new templates land, so a missing custom handler is caught at registration rather than at commit time.
- Provide a startup-completion barrier the consumer signals once all custom quality-rule handlers are registered, and reject rule evaluation until that barrier is past, making the registration-before-use ordering explicit.
- Document the registration-before-use ordering requirement as a property of the quality-rule (verifier-executor) pattern, so a consumer knows custom handlers must be registered before any referencing template loads.

## Evidence

- `_discover/quality-rules-and-attribute-validation.md` Observations bullet "custom-handler lifecycle".
- `graph/qualityrule/eval/rules.go` — the registry and reserved-name handling.

