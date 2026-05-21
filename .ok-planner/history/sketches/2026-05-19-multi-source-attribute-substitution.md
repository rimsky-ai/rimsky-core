# Multi-source attribute substitution

**Date:** 2026-05-19
**Status:** Sketch — pre-spec design exploration
**Audience:** Future planner / implementer; rimsky maintainers

## Context

Today, the substitution grammar at
`code:graph/attribute/substitution.go` (lines 5–24) is a closed
enumeration of source kinds: `params`, `nodes.<X>.attribute`,
`nodes.<X>.event`, `claim.<alias>.{address|scope|payload}`,
`trigger.message.payload`, `child.partition_key`. Each `source:`
directive on an attribute-schema field resolves through exactly one of
these — a single upstream, a single field path. The grammar has no
fallback (`??`), no coalesce, no array form. The dispatch-time resolver
either finds the named value or returns `ErrMissingSource`.

Subscriptions, by contrast, *are* multi-upstream: `subscribes:` is an
array, and the semantics are inclusive-OR — any subscribed upstream
firing re-dispatches the receiver (per the wait-set invariant
`code:.ok-planner/design/concepts/wait-set.md#36-42`). The cascade
layer composes well with multi-upstream graphs. The substitution layer
does not.

That mismatch surfaces immediately when a consumer wants to model
alternative-path graphs. The canonical case is **recovery / repair
subgraphs**: a downstream node consumes an attribute that can come from
either a happy-path producer (`generate-foo`) or a recovery producer
(`repair-foo`), depending on which path actually ran. Subscription
firing handles the trigger side cleanly (the receiver subscribes to
both upstreams), but the receiver's `source:` directive can only name
one of them — so substitution resolves to a single upstream regardless
of which subscription actually fired.

Today consumers work around this by routing through external state: the
recovery node writes to a substrate the consumer manages (a filesystem
path, an S3 prefix, a database row), and the downstream node re-fetches
from that substrate at dispatch via an http-node or custom-executor
call. That works but undermines the typed-attribute substitution
model. The cleanest expression of "this value can come from either
upstream" should be in the receiver's `attributes.schema`, not in
out-of-band state-management plumbing.

This sketch proposes lifting the grammar limitation to admit
multi-source resolution as a first-class shape.

## Proposed shape

Allow `attributes.schema.<field>.source` to be either a string (today's
shape) or an array of strings (new). Each element must be a valid
single-source substitution directive — the same grammar today, no
nested operators, no new directive kinds. Auto-subscribes to every
upstream named anywhere in the array.

```yaml
nodes:
  - type: generate-config
    executor: claude-agent
    attributes:
      schema:
        properties:
          config_blob: { type: object }

  - type: repair-config
    executor: claude-agent
    attributes:
      schema:
        properties:
          config_blob: { type: object }

  - type: verify-config
    executor: project-alpha-verifier
    subscribes:
      - { node: generate-config, on: state }
      - { node: repair-config, on: state }
    attributes:
      schema:
        properties:
          config_blob:
            type: object
            source:
              - "{{nodes.repair-config.attribute.config_blob}}"
              - "{{nodes.generate-config.attribute.config_blob}}"
```

`verify-config` subscribes to both producers. Its `config_blob` field
resolves at dispatch by walking the source array in declared order and
picking the first source whose upstream has emitted (the upstream's
attribute store has a row for the named field). Order is significant
and declares preference: `repair-config` listed first means "if repair
has run, use repair; otherwise fall back to generate."

## Resolution semantics

At dispatch, the substitution engine walks the source array in order.
For each candidate:

1. Resolve the directive against the receiver's `ResolveContext`
   (same logic as today's single-source case).
2. If the candidate resolves successfully (the named upstream has a
   row in `rimsky_node_attributes` and the field path walks to a
   present value), use it. Stop walking.
3. If the candidate resolves to `ErrMissingSource` (upstream has not
   produced the named attribute / field yet), try the next candidate.
4. Any *other* resolution error (e.g. type mismatch against the
   receiver's JSON Schema) is fatal — do not silently fall through.
   Multi-source is about availability, not about hiding bad data.

If every candidate is missing, the resolution fails the same way a
single-source missing candidate fails today (`ErrMissingSource`
surfaces at dispatch, blocks the run).

### "Most recent" is not the semantic

An alternative semantic — "whichever upstream most recently ran wins" —
is tempting but harder to reason about. Order-of-execution dependence
makes templates non-deterministic from the reader's perspective
(reading the template doesn't tell you which source you'll get
without knowing what fired). First-non-missing in declared order is
deterministic from the template alone: list repair first if you want
repair to win when present.

### Auto-subscription

Like single-source substitution, multi-source auto-subscribes the
receiver to every upstream named in the array. Explicit `subscribes:`
entries are still allowed for state-only dependencies that don't
consume attributes; the auto-subscribe and the explicit one merge
deduplicated into the same wait-set.

### Backward compat (pre-v1 hand-wave)

Pre-v1 the wire shape is free to move. The Go struct for an attribute
schema's `source` field changes from `string` to a typed sum (or to
`[]string` with the single-element string case still admitted for
ergonomic continuity). The substitution engine's existing single-source
code path remains the inner loop; the array case wraps it.

## Implementation sketch

Two narrow changes:

1. **Schema decode** (`code:foundation/spec/template.go` and the
   attribute-schema validator at `code:graph/node/template_validator.go`).
   `source` accepts either a string or `[]string`. Validation: every
   element passes the existing single-source directive parser.
   Template-registration rejects empty arrays, mixed types, and
   directives that don't match the closed grammar.

2. **Substitution engine** (`code:graph/attribute/substitution.go`).
   Add a new helper `resolveOneOf(refs []string, ctx ResolveContext) (any, error)`
   that walks an array and applies the single-source resolver per
   element. The existing `resolveDirective` path becomes the
   single-element case (an array of one).

The grammar itself does not change — multi-source is an array *of*
existing directives, not a new directive kind. The existing five
source kinds (and their auto-subscribe semantics) carry through
unchanged.

## What this is not

- **Not coalesce-on-null.** `null` is a legitimate value an upstream
  can produce — the receiver's schema validates it, and the
  substitution engine treats `null` as "present" rather than
  "missing." (Today's single-source case has the same semantic — an
  upstream emitting `null` for a field resolves to `null`, not to
  `ErrMissingSource`. Multi-source preserves that.)
- **Not "merge."** No object-spread, no array-concat, no
  field-by-field reconciliation across upstreams. Multi-source picks
  one upstream's value verbatim. Consumers wanting structured merge
  can keep using a custom executor that reads from both upstreams via
  separate fields and emits a merged attribute downstream.
- **Not branching by event provenance.** "If this subscription fired,
  use source A; if that one fired, use source B" is a different
  feature (subscription-keyed conditional resolution). Multi-source's
  semantic is event-provenance-agnostic — the receiver doesn't know
  which subscription triggered the current dispatch, and the
  resolution is the same regardless.

## Scope

One narrow grammar lift. Two files touched (schema decode + resolver).
Conformance test: register a template with multi-source attribute,
create an instance, dispatch with both candidates present, dispatch
with only one present, dispatch with neither — assert the resolver
picks correctly in each case. No protocol changes, no migrations, no
auth surface. Composes with all existing primitives (claims, events,
sensors, fan-out, the resolution-time engine).

## Open questions for the spec phase

1. **Validation depth at template registration.** Today's single-source
   substitution validator at `code:graph/node/template_validator.go`
   checks that each `{{...}}` directive is syntactically well-formed
   and that referenced upstreams exist in the template. The
   multi-source case should validate every array element through the
   same path. Should it additionally validate that the upstreams are
   reachable from the receiver under at least one cascade path, to
   catch "unreachable repair branch" templates at registration? Or is
   that an over-reach (the operator may want a repair branch that's
   only reachable via an admin-invalidate path with no upstream
   cascade)?

2. **Should `source:` accept mixed-kind arrays?** E.g., one element a
   `nodes.X.attribute.field`, another a `params.Y`. The grammar
   parser doesn't care, so technically yes — but a `params` value is
   always "present" (instance creation populated it), which means a
   trailing `params` entry acts as a default-value fallback. That's
   useful — a repair sequence with a hardcoded baseline as the last
   resort. Worth supporting explicitly in the spec.

3. **Bare-form whole-attribute pulls in multi-source.** The
   2026-05-19 cycle landed bare-form pulls (`{{nodes.X.attribute}}`
   resolves to the whole upstream attribute object). Multi-source
   should accept those without modification — the array element is a
   single directive, and the bare form is one. Verify in the
   conformance test.
