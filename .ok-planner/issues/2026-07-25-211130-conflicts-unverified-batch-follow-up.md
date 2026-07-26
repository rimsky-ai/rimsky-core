---
issue: conflicts-unverified-batch-follow-up
kind: audit
category: conflicting
status: verified
opened: 2026-07-25T21:11:30Z
---

# Eight reported contradictions, now verified: six are real prose drift, two dismiss

A consistency audit's sub-pass reported eight possible contradictions between design artifacts without re-reading the sources. They have now each been verified against the artifacts and the code. Six are confirmed — in every case the code is consistent and one identifiable sentence of prose misdescribes it — and two dismiss as loose wording rather than contradiction.

Confirmed, with the false sentence located:

- `concept:run-scope` says no runtime caller walks the ancestor chain for sub-graph recursion depth; the dispatch path does exactly that walk (`code:lib/runtime/child_execution.go::rejectDelegateRecursionInChain`). The sub-graph concept is accurate.
- `decision:subscription-edges-only-from-explicit-block` defines a structural root as "subscribes block empty or absent," omitting the substitution-reference and message-consumption conditions the code and `decision:structural-root-edge-injection-at-registration` both apply.
- `concept:cascade-graph` claims the observability surfaces "do not join a frame to its triggering message row"; the frames endpoint returns message type/sender per frame via exactly that join (`code:lib/control/controlapi/frames.go`).
- `decision:non-cascade-direct-to-stale` says message-delivery runs get schema defaults overlaid by the payload; the code upserts the payload verbatim with no defaults pass (`code:lib/runtime/message_delivery.go`), as the attribute/node-run/message concepts correctly state.
- The decisions index line for `decision:subscription-reconciler` says "with backoff"; the reconciler sweeps on a fixed ticker (backoff exists only per-RPC within a pass), as the decision body itself correctly says.
- `concept:wait-set`'s upstream-gating predicate omits the held-co-member exemption the gate evaluator applies (`code:lib/runtime/gate_evaluator.go::anySubscribedUpstreamInFlight`) and which `concept:cascade` and `decision:held-as-state-not-phase` both state.

Dismissed: the idempotent-mode bag comparison handles the pending-row case with an explicit on-demand fallback (no nonexistent field is read), and `decision:upstream-gating-at-eligibility`'s phrasing is loose but not behaviorally false.

## Options

- Carry the six one-clause corrections as a single sprint batch (they pair naturally with the sibling held-cascade and at-most-one rulings). Cost: sprint work only.
- Split them into six separate issues — process overhead with no added clarity.

## Ruling

> Generated ruling (/verify-issues): amend the six artifacts in one batch —
> `concept:run-scope` (depth-gating bullet), `decision:subscription-edges-only-from-explicit-block`
> (full structural-root definition), `concept:cascade-graph` (frame-to-message join exists),
> `decision:non-cascade-direct-to-stale` (message-delivery bag is verbatim payload),
> the decisions-index line for `decision:subscription-reconciler` (fixed interval),
> and `concept:wait-set` (held-co-member exemption in the predicate) — each to the
> code-verified statement above. The two dismissed reports need no action.

<!-- Owner: this is a recommendation, not your decision. Leave it
as-is to accept — the next /plan-sprint carries it, naming the
generated/recommended batches at sign-off. Edit the text to
redirect, empty the section to discuss live, or delete this note
to adopt the ruling as your own. -->
