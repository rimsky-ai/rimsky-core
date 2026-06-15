---
story: claim-handoff-across-frames
status: as-is
---

# Template author wires a claim handoff that survives across frames

## Role

As a template author wiring a multi-node atomic-staging workflow where the co-holder runs in a different frame from the acquirer — e.g. a per-node subscription that opens a fresh frame, or a cross-cutting instance-scoped subscription — I can rely on the held claim staying active across the frame boundary until the holding subgraph completes, with the alias-keyed substitution for the claim's address, payload fields, and claim-scope resolving in the new frame to the same bytes it would in the acquirer's frame, so that I'm free to separate work into independent frames for clean per-iteration audit and distinct frame-start and frame-end markers without breaking the atomicity guarantee.

## Capability

A held claim's lifetime is governed by the holding subgraph, not by any frame. When the co-holder's subscription opens a fresh frame (per-node next-frame subscription, or an instance-scoped subscription which defaults to next-frame), the claim handle row stays active until every holder settles, regardless of how many frames the holding subgraph spans. The substitution context's claim-alias lookup walks from the holding-subgraph's co-holdership directive to the upstream's claim-handle row directly, so the alias resolves in any frame where the holding subgraph is still open.

## Business value

Frame topology and claim lifetime are independent design knobs. Authors can choose per-iteration frames for audit-trail granularity or for distinct frame-timeout windows without losing the holding-subgraph atomicity. Conversely, an author who needs the entire holding subgraph in one frame (for shared in-frame substitution context or a single frame-start/frame-end pair) chooses the in-frame subscription mode and gets that — the claim doesn't care either way.

## Acceptance

Same template shape as `story:claim-handoff` (acquirer plus co-holder declaring co-holdership and reading the alias-keyed address substitution), but the co-holder's subscription is configured to open a fresh frame (per-node next-frame, or instance-scoped). When the acquirer is invalidated and settles to terminal success in one frame, the cascade walk opens a fresh frame for the co-holder; the co-holder dispatches in the new frame; the co-holder's alias-keyed address substitution resolves to bytes equal to the acquirer's claim handle's address; both settle fresh; auto-terminal fires Commit only after the co-holder's frame ends. The claim handle row stays active across the frame boundary; the acquirer's run and the co-holder's run carry distinct frame ids, both committed before the held claim resolves.

## Falsifier

The held claim is released between the acquirer's frame end and the co-holder's frame start (auto-terminal fires prematurely on the acquirer's settlement alone), OR the co-holder's alias-keyed substitution returns missing-source in the new frame (alias context not threaded across the frame boundary), OR auto-terminal fires Commit before the co-holder's frame ends.

## Proof

Executable proof — three scenario variants: a co-holder with a next-frame per-node subscription (assert the co-holder's frame id differs from the acquirer's, the substitution resolves to identical bytes, the claim handle row stays active until the second frame ends, auto-terminal fires Commit once after the second frame); a co-holder with an instance-scoped (cross-cutting) subscription that defaults to next-frame; and a three-frame chain (acquirer plus two co-holders each subscribed with next-frame, each reading the alias-keyed address substitution; assert three distinct frame ids, the claim handle row stays active until the third frame ends).
