---
story: claim-handoff
status: as-is
---

# Template author wires multi-node atomic staging via claim handoff

## Role

As a template author building a multi-node atomic-staging workflow, I can declare an upstream acquirer node that opens a claim and downstream co-holder nodes that share the same claim via `holds:` — reading the live claim's address, payload fields, and scope bytes via `{{claim.<alias>.address|payload.<f>|claim_scope}}` to do work against the staged location — then have the runtime fire Commit (all-success) or Abandon (any-failed) atomically across the holding subgraph, so that I compose stage-then-write-then-verify-then-commit pipelines (and similar all-or-nothing patterns) without re-acquiring the same claim from every node.

## Capability

A downstream node declaring `holds: { <alias>: { from: <upstream-type> } }` co-holds the upstream's claim by alias; at dispatch the runtime resolves `{{claim.<alias>.address}}`, `{{claim.<alias>.payload.<key>}}`, and `{{claim.<alias>.claim_scope}}` substitutions against the held claim's actual bytes — the same acquired result the original acquirer received. Auto-terminal fires once every node in the holding subgraph settles non-active: Commit on all-success, Abandon on any-failed.

## Business value

Multi-node atomic-staging composes naturally from existing template-DSL primitives. The author writes one acquirer plus N co-holders; rimsky enforces the all-or-nothing guarantee without bespoke rollback logic in template-land.

## Acceptance

A template with (a) an acquirer node opening a claim with alias X via its `stores:` declaration, and (b) a co-holder declaring `holds: { X: { from: <acquirer-type> } }` AND reading `{{claim.X.address}}` (or `.payload.<f>` or `.claim_scope`) in its attribute schema. When the acquirer is invalidated and settles `terminal/success`, the co-holder dispatches with the substitution resolved to the held claim's actual bytes — the address bytes the co-holder receives equal the bytes the acquirer received. When the co-holder also settles `terminal/success`, the held claim's auto-terminal fires Commit (the holding subgraph promotes to committed; the producer's Commit verb fires). When either the acquirer or the co-holder settles failed, auto-terminal fires Abandon.

## Falsifier

The co-holder's `{{claim.X.address}}`, `{{claim.X.payload.<f>}}`, or `{{claim.X.claim_scope}}` substitution fails at dispatch with `terminal/error/template_resolution_failed`, OR the co-holder dispatches but receives substituted bytes that don't equal the acquirer's bytes, OR auto-terminal fails to fire Commit when every holding-subgraph member settles fresh, OR fails to fire Abandon when any member settles failed.

## Proof

Executable proof — table-driven scenario test covering the regression-close shape (acquirer + co-holder reading `{{claim.X.address}}` → Commit), per-field substitution kinds (`.address`, `.payload.<f>`, `.claim_scope` each resolve to the held claim's bytes), the Abandon path (co-holder forced to terminal-error via `error_types: give_up` → Abandon), the multi-co-holder Commit shape (two co-holders both reading; auto-terminal fires only after the slowest settles), and wire-payload parity (a co-holder receives a store-handle wire entry identical to what the acquirer receives — same handle bytes regardless of whether the receiver opened the claim or co-held it). Pins `concept:claim-co-holdership` invariant "At dispatch, the co-holder's execution request carries the co-held claim's address (the same acquired result the original acquirer received)."
