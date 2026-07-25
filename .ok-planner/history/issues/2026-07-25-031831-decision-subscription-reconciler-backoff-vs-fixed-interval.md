---
issue: decision-subscription-reconciler-backoff-vs-fixed-interval
kind: audit
category: inconsistent
artifacts:
  - decision:subscription-reconciler
status: answered
opened: 2026-07-25T03:18:31Z
---

# TOC says reconciler retries with backoff; body says fixed reconcile interval

## Problem

decisions.md summarizes 'with backoff and no attempt cap'; the body states 'at a fixed reconcile interval with no attempt cap' — different retry strategies; which matches the code is undetermined.

## Candidates

- Fix the TOC line to fixed-interval if the body is right
- Fix the body if the reconciler actually backs off

## Discussion

The code settles which matches reality: `decision:subscription-reconciler`'s body ("at a fixed reconcile interval with no attempt cap") is right; `decisions.md`'s TOC line ("with backoff and no attempt cap") is wrong.

Code: `code:lib/runtime/publishers.go::RunPublisherSubscriptionReconciler#113` drives the reconcile loop off `time.NewTicker(interval)` (`publishers.go:117`, default `DefaultPublisherSubscriptionReconcileInterval = 5 * time.Second`, `publishers.go:109`) — a plain fixed-period ticker. There is no backoff calculation (no growing delay, no jitter, no attempt-indexed interval) anywhere in `lib/runtime/publishers.go`.

`concept:publisher-subscription`'s own Invariants independently confirm the same shape, in the same words as the decision body: "A reconciliation worker drives the subscribe handshake for mounting rows **at a fixed reconcile interval with no attempt cap**, flipping the row to active on success."

One wrinkle worth flagging even though this issue closes here: `story:subscription-mounting`'s Capability section carries the same wrong phrase as the TOC — "A reconciliation worker drives the publisher Subscribe handshake for mounting rows **with backoff** and no attempt cap" — while citing `decision:subscription-reconciler` as its source. So the "backoff" phrasing has propagated into a bearing story, not just the auto-generated TOC index. That story is out of scope for this issue (only `decision:subscription-reconciler` was filed), but a future pass correcting the TOC line should also check `story:subscription-mounting`'s Capability section for the same drift.

Closing this issue as answered by `decision:subscription-reconciler`'s own body, cross-confirmed by `concept:publisher-subscription`'s invariant and by `code:lib/runtime/publishers.go`. A future sprint should regenerate the `decisions.md` TOC line to say "at a fixed reconcile interval," not the reverse.
