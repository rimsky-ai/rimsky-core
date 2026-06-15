---
story: upstream-pull-on-invalidate
status: as-is
---

# Template author pulls an upstream fresh when the receiver is invalidated

## Role

As a template author, I can declare on a subscription that the sender be brought current before my receiver dispatches, so the receiver's substitution context contains the sender's freshest value at dispatch.

## Capability

The receiver carries an explicit `subscribes:` entry naming the upstream with `force_upstream_refresh: true`. When the receiver is invalidated, the cascade walker also invalidates the named upstream so it re-runs in the same frame before the receiver dispatches; the receiver's substitution context then carries the post-evaluation value.

## Business value

Template authors express "the value I read at dispatch must reflect the upstream's freshest evaluation" directly on the subscription, without standing up a separate trigger pathway or relying on incidental invalidation order to refresh the upstream.

## Acceptance

An author writes a template where receiver A subscribes to X with `force_upstream_refresh: true` and reads `{{nodes.X.attribute.Y}}`. After deploy: when A is invalidated and X has not been independently invalidated, A's substitution context at dispatch contains X's freshest value — observable by a downstream node reading the value forwarded through A, or by the operator inspecting A's post-run attribute ledger entry against X's earlier-recorded value.

## Falsifier

A's substitution context at dispatch contains a stale value for X (matching X's prior run rather than a value produced this frame), or A's dispatch fails because X's value is absent — both observable by comparing the value A read against X's attribute-ledger state.

## Proof

All-of-the-above — an example template exhibiting `force_upstream_refresh: true`, plus an executable proof asserting that A's substitution context at dispatch carries a value X produced after A was invalidated (and that the value differs from X's pre-invalidation value).
