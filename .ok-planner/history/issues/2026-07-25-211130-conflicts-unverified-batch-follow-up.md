---
issue: conflicts-unverified-batch-follow-up
kind: audit
category: conflicting
status: repaired
opened: 2026-07-25T21:11:30Z
---

# Do the six confirmed prose-vs-code contradictions from the consistency-audit sub-pass still stand?

Yes, all six were still present and have now been repaired as one batch of corpus-side, intent-preserving fixes (per `{{MECHANICAL-VS-JUDGMENT-RULE}}`: each aligns a stale sentence to the commitment the code and a counterpart artifact already agreed on — no commitment changed). Verified against the code and the sibling artifact, then corrected:

- `concept:run-scope` — the depth-gating bullet claimed no runtime caller walks the ancestor chain; `lib/runtime/child_execution.go::rejectDelegateRecursionInChain` does exactly that walk at dispatch time, as a backstop behind the canonicalizer's static rejection (`concept:sub-graph` already said this correctly). Reworded to state both layers.
- `decision:subscription-edges-only-from-explicit-block` — the Choice defined a structural root as "subscribes block empty or absent," omitting the substitution-reference and message-consumption conditions that `decision:structural-root-edge-injection-at-registration` and the code both apply. Expanded to the full definition.
- `concept:cascade-graph` — claimed the frames-read routes never join a frame to its triggering message row; `lib/control/controlapi/frames.go` returns `MessageType`/`MessageSender`/`MessageSenderKind` per frame via exactly that join. Corrected to state the join exists.
- `decision:non-cascade-direct-to-stale` — the `message_delivery` bullet claimed the bag is schema defaults overlaid by the payload; `lib/runtime/message_delivery.go::deliverNamedMessageInTx` upserts the payload verbatim with no defaults pass, as `concept:attribute` already correctly states. Reworded to match.
- `decisions.md` TOC line for `subscription-reconciler` said "with backoff"; the decision body itself (unchanged) says the reconciler sweeps at a fixed interval with no attempt cap. TOC line corrected to match the body.
- `concept:wait-set` — the pending→stale predicate omitted the held-co-member exemption `lib/runtime/gate_evaluator.go::anySubscribedUpstreamInFlight` applies (a `held` upstream sharing subgraph co-membership with the receiver does not gate it), which `decision:held-as-state-not-phase` already states. Added the exemption clause alongside the existing self-subscription exemption.

The two dismissed reports (idempotent-mode bag comparison; `decision:upstream-gating-at-eligibility` loose-but-not-false phrasing) needed no action and none was taken.

Verified by reading each cited code path and each counterpart artifact directly; these are prose-only corrections with no build/test surface.
