# Deterministic transformations

Not every node in a rimsky graph is an agent or an external service.
Many graphs benefit from interleaving deterministic post-processing
nodes between the agent-driven (or otherwise-non-deterministic) work.
This page covers the recurring patterns: post-processors,
confidence-driven branching, and agent self-blocks.

Rimsky has no special-cased node type for "deterministic"; the trick is
to use the existing executor + handler surfaces in a constrained way.

## Post-processors as native (claim-only) nodes

A node with no `executor` declared is a **native node** — rimsky
synthesizes a `Complete{changed: true}` once any of its claims have
been acquired. The value the cascade carries is whatever the upstream
nodes wrote into the attributes.

Use this for:

- Combining outputs from multiple parents into one shaped object.
- Stripping or renaming fields the downstream nodes expect.
- Forcing a cascade fan-out (`ResolveAlwaysPropagate`) at a specific
  graph point.

## Post-processors as `http-node` invocations

For non-trivial transformations, `http-node` (the bundled Go reference
executor) accepts a small declarative HTTP-call shape and returns the
response in `attributes_delta`. Pair it with `qualityrules` on the
node's attributes for assertion-style guards on the transformed
output.

## Confidence-driven branching

Pattern: an agent emits a confidence score with its findings. The
branch nodes use `on_executor_complete` with `resolve: by_changed` and
an invalidate emit to route to the right downstream.

```yaml
nodes:
  classify:
    executor: claude-agent
    userdata:
      cli:
        system_prompt: "Classify the document. Emit confidence 0..1."
    attributes:
      schema:
        properties:
          category:
            type: string
          confidence:
            type: number

  high_confidence_path:
    executor: deterministic-finalize
    attributes:
      schema:
        properties:
          category:
            source: nodes.classify.value.category
    deps: [classify]

  low_confidence_path:
    executor: human-review
    attributes:
      schema:
        properties:
          category:
            source: nodes.classify.value.category
    deps: [classify]
```

The `classify` node's `on_executor_complete` is empty (default
`by_changed`). Downstream invalidation gating is handled at attribute-
substitution time: nodes that reference `nodes.classify.value.category`
trigger when classify completes and propagates. The branching is then
done by quality-rule guards on each downstream that no-op (via a
deterministic node returning unchanged) when their confidence
condition isn't satisfied.

For sharper branching, use named events:

```yaml
nodes:
  classify:
    executor: claude-agent
    on_event:
      high_confidence:
        invalidate:
          targets: [auto_finalize]
      low_confidence:
        invalidate:
          targets: [needs_review]
```

The `classify` executor emits `high_confidence` or `low_confidence` as
a named event before its terminal `Complete`. Each `on_event` handler
fires the appropriate downstream invalidate.

## Agent self-blocks

Pattern: an agent reaches a state where it cannot continue without
external input — a missing parameter, a required artifact, an
ambiguity it cannot resolve from context. Use `Blocked` (not
`Errored`) to signal "I produced output but explicitly chose not to
claim success", paired with an `on_executor_blocked` handler that
routes the run downstream.

```yaml
nodes:
  draft:
    executor: claude-agent
    on_executor_blocked:
      resolve: pass
      invalidate:
        targets: [routing]
```

`draft` emits `Blocked { reason: "needs_review", payload: {...} }`. The
handler routes through `routing` which decides next steps based on the
blocked-payload context. See `docs/concepts/handlers.md` and
`docs/protocols/executor.md` for the wire-shape details.

## Why this split matters

Rimsky stays domain-agnostic. The graph carries flow control; the
executors carry domain logic. Deterministic transformations are just a
class of executor where the implementation happens to be pure code
rather than an agent or external service. The semantics —
substitution, retry policy, cascade, claims — are identical.
