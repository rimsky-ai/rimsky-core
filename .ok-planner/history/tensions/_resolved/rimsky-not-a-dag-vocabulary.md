---
resolved_by: spec:2026-05-14-subscription-cascade-and-quality-of-life-design
tension: rimsky-not-a-dag-vocabulary
category: vocabulary-drift
status: resolved
affects:
  - cascade
  - node
  - subscription
---

# Surface vocabulary treated rimsky as a DAG; the runtime is a reactive node graph

## What was muddy

External-facing docs, internal prose, and several validator error messages spoke about "dependency cycles," "dependency graphs," "topological order," and "DAG-ness." But rimsky's runtime is explicitly a reactive node graph with bidirectional message flow (cascade propagation downstream, invalidate-emit upstream-on-pass, `frame: next` deferred-invalidate queues, self-invalidate-via-error-policy). A graph with `frame: next` self-invalidates is not a DAG; cycles are first-class.

The vocabulary mismatch surfaced as:

- A `detectCycles` validator that rejected templates with mutual dependencies, even when the cycle was operator-intentional via `frame: next`.
- README/docs prose calling rimsky "a DAG orchestrator."
- Error messages citing "dependency cycle detected" that fired on legitimate templates.

## Why it mattered

Operators reading the docs expected DAG semantics; the runtime didn't deliver them. The validator's cycle rejection blocked valid templates. The implementer reading "topological order" in code had to mentally translate to "cascade-walk order, which may revisit nodes across frames."

## Resolution

Resolved by `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`. The `dependencies:` field retires; cycles are no longer a static validator concern but a runtime convergence concern handled by the `frame: next` deferred-invalidate queue. The subscription model acknowledges reactive coupling explicitly: receivers subscribe to senders; cycles are expressed as mutually-subscribed nodes with `frame: next` modifiers, and the runtime defers across frames until the instance converges (or doesn't, in which case `frame_timeout_ms` surfaces the wedged state via slog warning).

The `detectCycles` validator is retired; the prose surfaces (concept docs, public docs, agent docs) are updated to describe rimsky as a "reactive node graph" rather than a DAG.
