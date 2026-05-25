# Substitution fallback + invalidate-with-payload

**Date:** 2026-05-20
**Status:** Sketch — pre-spec design exploration
**Audience:** Future planner / implementer; rimsky maintainers

## Context

Two narrow grammar / wire-protocol additions that unlock a cleaner
shape for **producer-owned recovery patterns** — a node that
generates an output is also the node that verifies and repairs it on
downstream failure. The model collapses what would otherwise be a
separate-repair-subgraph pattern into a single producer node with a
cascade cycle back from its downstream consumer.

The producer-owned recovery pattern needs two things rimsky doesn't
have today:

1. **Optional / fallback substitution.** The producer's prompt or
   userdata references its downstream consumer's failure attribute.
   On first dispatch, the downstream hasn't run yet — the substitution
   has nothing to resolve to. Today this raises `ErrMissingSource` and
   the dispatch fails. The producer needs a way to say "use this if
   present, else a default."

2. **Invalidate-with-payload.** A lifecycle handler or admin
   invalidate that delivers structured context to the re-dispatched
   node. Useful for cascade-driven payloads (a downstream failure
   passing structured context up to a producer) and for human-driven
   ones (an operator invalidating a failed node with a hint —
   "the source moved, try this URL"). Today's invalidate just sets
   the target's state to stale; no context flows.

Each is independently useful; together they enable a single
declarative pattern for "producer fixes its own output when
downstream rejects it."

Neither feature changes any existing wire shape destructively. Both
are additive — current substitution expressions and current
invalidates continue to work unchanged.

---

## Feature 1 — Fallback substitution

### Friction

The substitution grammar at `code:graph/attribute/substitution.go`
(lines 5–24) is a closed enumeration of source kinds. Each
substitution directive resolves to exactly one upstream value. There
is no fallback operator, no default-value form, no way to express
"try X, otherwise Y."

Recovery patterns that want a producer to read its downstream
consumer's failure context hit a chicken-and-egg: on first dispatch,
the consumer hasn't run, so the producer can't resolve any reference
to the consumer's attributes. The producer dispatch fails.

Workarounds today: split the producer into a "first-time generator"
node and a separate "repair" node, each consuming different upstreams.
That defeats the producer-owned model.

### Shape

Admit a fallback infix operator `|` inside the substitution grammar:

```yaml
userdata:
  cli:
    user_prompt_template: |
      Generate or repair the item config for {{params.domain}}.
      Previous downstream warnings (if any):
      {{nodes.verify-item.attribute.warnings | null}}
```

`{{X | Y}}` resolves to `X` if `X` resolves; otherwise to `Y`.
Left-associative; chainable: `{{X | Y | Z}}` tries each in order.

`X` and `Y` are each one of:

- A substitution directive (any current source kind:
  `nodes.<X>.attribute.<field>`, `claim.<alias>.<...>`,
  `params.<key>`, etc.).
- A JSON literal: `null`, `true`, `false`, numbers
  (`42`, `3.14`), strings (`"text"`).

A literal on the right side is the natural default — `{{X | null}}`,
`{{X | "fallback string"}}`, `{{X | 0}}`. Literals on the left are
admissible but uninteresting (they'd always resolve, so the right
side never fires).

### Resolution semantics

At dispatch, the substitution engine walks the chain left-to-right.
For each candidate:

1. If the candidate is a directive, resolve it. If it returns a
   present value (including `null` produced by the upstream — see
   below), use it.
2. If the candidate returns `ErrMissingSource` (upstream hasn't
   emitted, or the field path doesn't exist), advance to the next
   candidate.
3. If the candidate is a literal, return the literal value directly.

If every candidate is missing and there is no literal default, the
overall resolution fails with `ErrMissingSource` — same as today's
single-directive failure.

### Distinguishing "missing" from `null`

An upstream that has emitted but with a `null` value at the referenced
path resolves to `null`, not to `ErrMissingSource`. The fallback does
NOT fire for explicit `null`. This matches the principle established
in the 2026-05-19 cycle (whole-directive value lift): an upstream's
`null` is a present value, not an absence.

Consumers wanting "fall through `null` too" can use a second fallback:
`{{X | Y | "fallback"}}` where Y is the literal `null`... wait, that
doesn't work either since `null` is a literal and would terminate the
chain. The honest design: if your upstream might emit `null` and you
want a different default in that case, model it as two attributes
(one for the value, one for the presence) or use a custom executor.
Multi-source `null`-handling is intentionally not first-class.

### Auto-subscription

Each directive in a fallback chain auto-subscribes the receiver to
the named upstream, exactly as it does today. A `{{nodes.X.attribute.foo | null}}`
auto-subscribes the receiver to `nodes.X.attribute.foo`. The literal
side contributes no subscription.

### Use case — producer-owned recovery (Z pattern)

```yaml
nodes:
  - type: discover-upstream
    executor: agent-runner
    # … emits upstream-info attribute …

  - type: generate-item-config
    executor: agent-runner
    subscribes:
      - { node: discover-upstream, on: state }
      - { node: verify-item-config, on: state, when: failed, error_class: item_validation_failed }
    userdata:
      cli:
        user_prompt_template: |
          {{nodes.verify-item-config.attribute.warnings | "No prior warnings — first dispatch."}}

          Upstream info: {{nodes.discover-upstream.attribute}}.
          Produce a valid item config.
    attributes:
      schema:
        properties:
          item_config: { type: object }

  - type: verify-item-config
    executor: project-alpha-verifier
    subscribes:
      - { node: generate-item-config, on: state }
    error_types:
      item_validation_failed: { action: pass }
    attributes:
      schema:
        properties:
          warnings: { type: array, items: { type: string } }
```

First dispatch of `generate-item-config`: `verify-item-config`
hasn't run; substitution falls through to the literal default.
Producer generates from scratch.

Verify runs, fails, emits warnings. The
`subscribes: when: failed, error_class: ...` entry on the producer
re-fires it. This time `verify-item-config.attribute.warnings`
resolves to the actual failure context. Producer regenerates with
guidance. Cascade cycles bounded by
`max_retries_without_progress`.

Single producer node owns both first-generation and repair. No
separate `repair-item-config` node, no shadow indirection through
external state.

### Implementation site

`code:graph/attribute/substitution.go` — extend the directive parser
to recognize `|` as the infix fallback operator. Resolver walks
each candidate. Auto-subscribe pass at template registration walks
each directive in the chain.

Template-registration validator (`code:graph/node/template_validator.go`)
parses fallback chains; each directive is validated per existing
single-directive rules. Literal recognition (JSON primitives) is
added to the same parser.

### Alternatives considered

- **Filter-style `default()`.** Jinja-style `{{X | default(Y)}}` where
  `default` is a filter function. Cleaner extensibility (more filters
  later) but heavier grammar. The simple infix `|` is enough for the
  current need.
- **Array form `source: [...]`.** Already declined in the 2026-05-19
  reasoning — conflates and/or, doesn't read as cleanly as the
  inline `|`.
- **Skip the feature; force consumers to split into multiple nodes.**
  Loses the producer-owned model. Documented as the workaround if
  this feature is delayed.

---

## Feature 2 — Invalidate-with-payload

### Friction

Today's invalidate (whether from a lifecycle handler's
`invalidate: [<node>]` directive or from
`POST /admin/instances/{id}/nodes/{node_id}/invalidate`) carries no
data. It moves the target to `stale`, the scheduler re-dispatches
on the next tick, and the re-dispatched node has no signal about
why it was invalidated beyond "something invalidated me."

In cascade-driven cases the re-dispatched node can read context from
the invalidating node's attributes via normal substitution — usable,
but indirect and limited to whatever the invalidating node happened
to write into its attributes. In admin-driven cases the operator has
no way to attach a hint to the invalidate; they have to mutate
something else (the source row, a known config) for the re-dispatched
node to read.

### Shape

Extend the lifecycle handler and the admin invalidate endpoint to
optionally carry a structured payload. The re-dispatched node
accesses it via a new substitution source kind `rimsky.invalidate_payload`.

**Lifecycle handler form:**

```yaml
nodes:
  - type: verify-item
    on_executor_errored:
      resolve: pass
      invalidate:
        - node: generate-item
          payload:
            error_class: "{{self.attribute.error_class}}"
            warnings:    "{{self.attribute.warnings}}"
            failed_at:   "{{self.attribute.failed_at}}"
```

`payload` is a YAML object whose values are substitution expressions
resolved against the invalidating node's current dispatch context
(its attributes, its claim, its params). Resolved object is delivered
to the re-dispatched target.

**Admin endpoint form:**

```
POST /admin/instances/{id}/nodes/{node_id}/invalidate
Content-Type: application/json
{
  "payload": {
    "hint": "The source moved to https://example.com/v2/data.",
    "operator": "alice"
  }
}
```

The body's `payload` is an opaque JSON object stored verbatim and
delivered to the re-dispatched target.

**Receiver-side access:**

```yaml
nodes:
  - type: generate-item
    userdata:
      cli:
        user_prompt_template: |
          {{rimsky.invalidate_payload.hint | "No operator hint."}}
          Previous warnings: {{rimsky.invalidate_payload.warnings | null}}
          ...
```

The new source kind `rimsky.invalidate_payload` resolves to the
payload object delivered with the most recent invalidate. Bare form
`{{rimsky.invalidate_payload}}` returns the whole object; path form
`{{rimsky.invalidate_payload.field.subfield}}` walks into it.

### Resolution semantics

- On first dispatch (no prior invalidate), `rimsky.invalidate_payload`
  resolves to `ErrMissingSource`. Use the fallback operator from
  Feature 1 to provide defaults.
- On re-dispatch after invalidate, `rimsky.invalidate_payload`
  resolves to the most recent payload delivered with an invalidate
  to this node-run. Older payloads from prior invalidates are not
  retained — only the most recent.
- Payload is opaque to rimsky. Validation against the receiver's
  schema is the receiver's job (template author can require fields
  in their userdata schema if claude-agent's `userdata_schema` covers
  it; otherwise the agent reads whatever's there).

### Persistence

Payload lives on the target node's row (a new
`rimsky_nodes.pending_invalidate_payload` column, or a sibling table
keyed by `(node_id, sequence)`). Cleared after the re-dispatch
consumes it.

### Implementation site

- Template spec (`code:foundation/spec/template.go`): lifecycle
  handler types gain an optional `payload` field of type
  `map[string]any` (substitution-bearing values).
- Substitution engine: new source kind
  `rimsky.invalidate_payload` resolved from the target node's
  pending payload.
- Schema migration: column on `rimsky_nodes`, or sibling table.
- Admin endpoint handler: accepts `payload` in body, persists.
- Lifecycle handler emit: resolves substitutions in `payload`,
  persists alongside the invalidate.

### Alternative — read invalidating node's attributes directly

The re-dispatched node could read the invalidating node's attributes
via existing `{{nodes.X.attribute.Y}}` substitution. Works for
cascade-driven invalidates (the invalidating node's attributes are
populated when it errors), but the model is indirect:

- The producer has to know which downstream might invalidate it and
  reference that specific node by name.
- For admin invalidates, there's no "invalidating node" — the
  operator's hint has nowhere to live except as a separate side
  channel.
- The "what context flows on invalidate" decision is implicit
  (whatever the invalidating node happens to expose); explicit
  payloads make the contract visible at the lifecycle-handler
  declaration site.

Invalidate-with-payload is the more honest form. The alternative
works for some cases but not the human-driven one.

---

## How they compose

The Z-pattern (producer-owned recovery) uses both:

- **Fallback substitution** handles the first-dispatch case where the
  invalidating node hasn't run yet — the producer reads the failure
  attribute with a literal default for the first time.
- **Invalidate-with-payload** lets the cascade-driven failure deliver
  exactly the context the producer needs, declared at the
  lifecycle-handler site rather than implicit in upstream attribute
  reads. Cleaner contract.

Either feature is independently useful:

- Fallback substitution stands alone for any "optional input"
  pattern, including admin-overridable params with literal defaults
  in the template and "compute-from-X-if-emitted-else-from-Y" data
  flows that don't involve invalidates.
- Invalidate-with-payload stands alone for operator-driven hint
  flows ("invalidate this failed node with this hint") and for
  cascade-driven payloads that don't need a fallback (the
  invalidator always populates the payload).

---

## Scope

Two narrow additions. Each is:

- One file in the substitution layer (`graph/attribute/`).
- One file in the template-spec layer (`foundation/spec/`).
- One migration (for invalidate-with-payload's payload column).
- One control-API route handler addition (admin invalidate payload
  body).
- Conformance test additions covering: fallback resolution with all
  candidate combinations; payload delivery across cascade-driven
  invalidate; payload delivery across admin-driven invalidate;
  payload persistence and clear-on-consume.

No protocol changes. No claim-producer / executor / publisher
service contract changes. No auth surface changes.

Composes with all existing primitives: claims, events, sensors,
fan-out, run-tree, parked state, error policies, lifecycle handlers.

---

## Open questions for the spec phase

1. **Fallback chain length cap.** Should `{{X | Y | Z | …}}` admit
   arbitrary chain length, or cap at a small N (e.g., 4)? Probably
   no cap — the grammar is left-associative, parser handles any
   length cleanly. But operators chaining 10 fallbacks is a smell;
   linting could warn.

2. **Substitution in payload values — when resolved?** Lifecycle
   handler payloads use `{{...}}` substitutions resolved against
   the invalidating node's context. Are those resolved when the
   handler fires (the invalidate moment) or when the target
   re-dispatches (read-time)? Fires-time is the natural semantic —
   snapshots the invalidating context, doesn't drift. Spell out
   in the spec.

3. **Admin payload size cap.** The admin endpoint accepts arbitrary
   JSON. Some cap is sensible — probably `cfg:messages.idempotency_ttl_seconds`'s
   neighbor `cfg:invalidate.max_payload_bytes` (default ~64 KiB to
   match blob-spill threshold). Reject above the cap; document the
   limit.

4. **Invalidate-with-payload + already-pending invalidate.** If a
   node has a pending invalidate (delivered, not yet consumed) and
   a new invalidate arrives with a different payload, what
   happens? Replace the pending payload, or accumulate? Replace is
   simpler; accumulate is fancier. Default to replace; revisit if
   the use case emerges.

5. **`rimsky.invalidate_payload` substitution in non-userdata
   positions.** Should the new source kind be valid in
   `attributes.schema.<field>.source`, in lifecycle handler payloads
   (recursive use), in `tags:` (resolved at materialization)? Probably
   the same scope as `{{rimsky.resume_payload}}` today — wherever
   substitution is permitted at dispatch.
