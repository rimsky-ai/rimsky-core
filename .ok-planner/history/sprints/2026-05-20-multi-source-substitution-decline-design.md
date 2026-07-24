# Multi-source attribute substitution: decline

**Date:** 2026-05-20
**Status:** design
**Sketch:** `.ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md` (archived by this spec)

## Context

A sketch proposed lifting the attribute-substitution grammar's per-field arity from 1 to N: allow `attributes.schema.<field>.source` to be either a string (today) or an array of strings (new), resolved at dispatch by first-non-missing in declared order, with auto-subscription to every upstream named in the array. The motivating case was recovery / repair subgraphs — a downstream verifier consuming an attribute that can come from either a happy-path producer or a recovery producer.

Brainstorm walked the proposal and declined it. This spec records the decision, archives the sketch, and lands the rationale durably on the design docs so the question doesn't recur.

## Rationale

Three observations led to the decline. They're worth keeping with the design docs because they describe a load-bearing distinction in the grammar that isn't obvious from the surface.

**The first-non-missing semantic loses signal.** The reactive cascade's whole point is that signals don't disappear — subscriptions fire on each upstream transition, the cascade walks every impactee, the wait-set ledger guarantees a receiver sees every contributing upstream. A grammar rule that says "pick one candidate and discard the rest" runs against that grain. If the receiver doesn't care which upstream fired, fallback isn't needed; if it does care, the receiver needs both values, not the first.

**Array-as-value collapses to today's schema.** Re-reading the proposal as "the field's value is the tuple of all candidate resolutions" rather than "the field's value is the first present candidate," the receiver is just consuming several upstream attributes — which is what a 1:1 schema with multiple optional fields already does. The dispatch path at `code:runtime/runner_dispatch.go::substituteAttributesSchema#532` already swallows `ErrMissingSource` on non-required fields and omits them from the output object. A verifier expressed as:

```yaml
verify-config:
  subscribes:
    - { node: generate-config, on: state }
    - { node: repair-config, on: state }
  attributes:
    schema:
      required: []
      properties:
        generated_config: { source: "{{nodes.generate-config.attribute.config_blob}}" }
        repaired_config:  { source: "{{nodes.repair-config.attribute.config_blob}}" }
```

dispatches when either upstream transitions, sees whichever fields are present, and has the full multi-upstream signal in hand. No grammar lift needed; no signal lost.

**The read-vs-cascade arity split is intentional.** Many-to-many subscriptions express "fire me when any of these transitions" — that's the cascade summing signals. Per-field 1:1 substitution expresses "this field's value is *this* expression" — that's the dispatch naming a value. The two arities encode different jobs. Lifting substitution to multi-source per field imports a summing semantic (which one? all? first?) into a naming surface, and any answer is wrong in some direction. The asymmetry between `concept:node-subscription` (N upstreams per receiver) and `concept:attribute`'s per-field `source:` (1 directive per field) is the right shape.

## Design changes

- Concept: mutate `.ok-planner/design/concepts/attribute.md` in place.

  - Append one new bullet to the `## Invariants` bulleted list, immediately after the existing "Errors omit value bytes ..." bullet, with this exact text:

    > Per-field `source:` arity is 1 — each attribute property declares exactly one substitution directive. Many-to-many fan-in across upstreams lives in the cascade vocabulary (subscriptions over multiple senders, plus optional schema fields whose dispatch-time `ErrMissingSource` is silently omitted at `code:runtime/runner_dispatch.go::substituteAttributesSchema`). Enforced at registration by `code:graph/node/template_validator.go::checkAttributeSource` (rejects any `source:` that isn't exactly one `{{...}}` directive with no surrounding text). The arity asymmetry between subscriptions (many-to-many) and substitution (per-field 1:1) is intentional: subscriptions sum signals across upstreams; substitution names a single value per field.

  - Append a second paragraph to the `## Boundaries` section, after the existing "Clarifying note (per 2026-05-15 ...)" paragraph, with this exact text:

    > Clarifying note on arity: per-field substitution is 1:1 by design — one `source:` directive names one value. Multi-upstream fan-in is the cascade vocabulary's job, expressed through `concept:node-subscription` (N upstreams per receiver) and optional schema fields (the dispatch path omits non-required fields on `ErrMissingSource`). The arity asymmetry is load-bearing — see the per-field-arity invariant.

  - Append a new entry to the bottom of the `## Notes` section (matching the project's chronological-ascending convention — newest entry at the bottom of the list), with this exact text:

    > 2026-05-20 — Multi-source attribute substitution proposal declined. Sketch archived to `.ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md`; the per-field-arity invariant and Boundaries clarification above were added by this spec. Rationale: a first-non-missing fallback semantic loses signal (subscriptions fire on each upstream transition, but substitution would collapse to one candidate); an array-as-value semantic collapses to today's 1:1 schema with optional fields plus auto-subscribe; the read-vs-cascade arity split is the load-bearing distinction. See `.ok-planner/history/specs/2026-05-20-multi-source-substitution-decline-design.md` for the full reasoning trail.

- Concept: mutate `.ok-planner/design/concepts/node-subscription.md` in place.

  - Append a new entry to the bottom of the `## Notes` section (matching the project's chronological-ascending convention — newest entry at the bottom of the list), with this exact text:

    > 2026-05-20 — The arity split between node-subscriptions (many-to-many over upstreams) and per-field attribute substitution (1:1) is load-bearing, not an inconsistency. Subscriptions sum signals; per-field `source:` names a single value. See `concepts/attribute.md` (per-field-arity invariant + Boundaries clarification) for the rationale; companion to the declined multi-source-substitution sketch (`.ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md`).

- Sketch archival: move `.ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md` to `.ok-planner/history/sketches/2026-05-19-multi-source-attribute-substitution.md`. Move the file unchanged; rationale lives on the concept Notes entries above, not as a preamble inside the moved file. Precedent: commit `cce2f1d` (the 2026-05-14-agentic-platform sketch decline) follows the same unchanged-move pattern.

- CHANGELOG: append a new bullet under the `## Unreleased` heading in `CHANGELOG.md`, with this exact text:

    > - **Multi-source attribute substitution proposal declined.** `concept:attribute` gains a per-field-arity invariant ("`source:` arity is 1 — one substitution directive per field") and a Boundaries clarification spelling out the read-vs-cascade arity split. `concept:node-subscription` gains a companion Notes cross-reference. Sketch (`.ok-planner/sketches/2026-05-19-multi-source-attribute-substitution.md`) archived to `.ok-planner/history/sketches/`. Rationale: a first-non-missing fallback semantic loses signal (subscriptions fire on each upstream transition, but substitution would collapse to one candidate); an array-as-value semantic collapses to today's 1:1 schema with optional fields plus auto-subscribe (`code:runtime/runner_dispatch.go::substituteAttributesSchema` already omits non-required fields on `ErrMissingSource`); the arity asymmetry between subscriptions (many-to-many) and per-field substitution (1:1) is intentional — subscriptions sum signals, substitution names values. See `.ok-planner/history/specs/2026-05-20-multi-source-substitution-decline-design.md`.

## Out of scope

- No code changes. The grammar at `code:graph/attribute/substitution.go`, the validator at `code:graph/node/template_validator.go::checkAttributeSource`, the dispatch wiring at `code:runtime/runner_dispatch.go::substituteAttributesSchema`, and the subscription-edge builder at `code:graph/node/subscription_edges.go` all stay as they are.
- No tension catalog entry. Tensions catalog muddiness in the codebase; this is a declined-proposal rationale and belongs on the concept Notes, not in `tensions/`.
