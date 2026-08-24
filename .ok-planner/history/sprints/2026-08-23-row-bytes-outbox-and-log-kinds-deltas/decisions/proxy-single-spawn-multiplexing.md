---
decision: proxy-single-spawn-multiplexing
---

# Concurrent dispatches multiplex over one spawn

## Choice

Concurrent dispatches against one (run-scope, binding-name) multiplex over the single daemon connection: each dispatch carries its own stream identifier, so a slow in-flight call never blocks a faster one sharing the same spawned process — and only one spawn is ever issued, even when several dispatches race to be the first against a given (run-scope, binding).

## Rationale

One process per binding keeps the spawn lifecycle state machine simple and preserves run-scope-lifetime semantics (`concept:host-daemon-proxy`); racing spawns would duplicate whatever side effects the spawned binary's startup performs. Per-dispatch stream identifiers remove the head-of-line blocking a shared connection would otherwise impose.

## Alternatives

- Spawn-per-dispatch — rejected: process churn, duplicated startup side effects, and it breaks the one-spawn-per-(run-scope, binding) lifecycle the proxy commits to.
- One spawn with serialized dispatches (no multiplexing) — rejected: a slow call head-of-line blocks every faster call sharing the binding.
