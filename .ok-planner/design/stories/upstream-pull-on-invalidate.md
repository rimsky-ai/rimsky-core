---
story: upstream-pull-on-invalidate
status: as-is
---

# Template author pulls an upstream fresh when the receiver is invalidated

## Story

As a template author, I can declare on a subscription that the sender be brought current before my receiver dispatches, so the receiver's substitution context contains the sender's freshest value at dispatch.

The receiver carries an explicit `subscribes:` entry naming the upstream with `force_upstream_refresh: true`. When the receiver is invalidated, the cascade walker also invalidates the named upstream so it re-runs in the same frame before the receiver dispatches; the receiver's substitution context then carries the post-evaluation value.

Template authors express "the value I read at dispatch must reflect the upstream's freshest evaluation" directly on the subscription, without standing up a separate trigger pathway or relying on incidental invalidation order to refresh the upstream.
