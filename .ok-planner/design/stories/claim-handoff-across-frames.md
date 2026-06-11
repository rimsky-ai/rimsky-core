---
story: claim-handoff-across-frames
status: as-is
---

# Template author wires a claim handoff that survives across frames

## Role

As a template author wiring a multi-node atomic-staging workflow where the co-holder runs in a different frame from the acquirer — e.g. a `frame: next` per-node subscription, or a cross-cutting (`instance: true`) subscription — I can rely on the held claim staying active across the frame boundary until the holding subgraph completes, with `{{claim.<alias>.address|payload.<f>|claim_scope}}` resolving in the new frame to the same bytes it would in the acquirer's frame, so that I'm free to separate work into independent frames for clean per-iteration audit and distinct `frame.start`/`frame.end` markers without breaking the atomicity guarantee.

## Capability

A held claim's lifetime is governed by the holding subgraph, not by any frame. When the co-holder's subscription opens a fresh frame (`frame: next`, or `instance: true` which defaults to `frame: next`), the claim handle row stays active until every holder settles, regardless of how many frames the holding subgraph spans. The substitution context's claim-alias lookup walks from the holding-subgraph's template `holds:` directive to the upstream's claim-handle row directly, so the alias resolves in any frame where the holding subgraph is still open.

## Business value

Frame topology and claim lifetime are independent design knobs. Authors can choose per-iteration frames for audit-trail granularity or for distinct frame-timeout windows without losing the holding-subgraph atomicity. Conversely, an author who needs the entire holding subgraph in one frame (for shared in-frame substitution context or a single `frame.start`/`frame.end` pair) chooses `frame: in` and gets that — the claim doesn't care either way.

## Acceptance

Same template shape as `story:claim-handoff` (acquirer + co-holder with `holds:` reading `{{claim.X.address}}`), but the co-holder's `subscribes:` block sets `frame: next` (or uses `instance: true`). When the acquirer is invalidated and settles `terminal/success` in one frame, the cascade walk opens a fresh frame for the co-holder; the co-holder dispatches in the new frame; the co-holder's `{{claim.X.address}}` resolves to bytes equal to the acquirer's claim handle's address; both settle fresh; auto-terminal fires Commit only after the co-holder's frame ends. The claim handle row stays active across the frame boundary; the acquirer's run and the co-holder's run carry distinct frame ids, both committed before the held claim resolves.

## Falsifier

The held claim is released between the acquirer's frame end and the co-holder's frame start (auto-terminal fires prematurely on the acquirer's settlement alone), OR the co-holder's `{{claim.X.<field>}}` substitution returns missing-source in the new frame (alias context not threaded across the frame boundary), OR auto-terminal fires Commit before the co-holder's frame ends.

## Proof

Executable proof — three scenario variants: a co-holder with `frame: next` on a per-node subscription (assert the co-holder's frame id differs from the acquirer's, the substitution resolves to identical bytes, the claim handle row stays active until the second frame ends, auto-terminal fires Commit once after the second frame); a co-holder with `instance: true` (cross-cutting; defaults to `frame: next`); and a three-frame chain (acquirer plus two co-holders each subscribed `frame: next`, each reading `{{claim.X.address}}`; assert three distinct frame ids, the claim handle row stays active until the third frame ends).
