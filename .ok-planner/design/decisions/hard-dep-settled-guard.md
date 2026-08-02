---
decision: hard-dep-settled-guard
---

# The hard-dep pull carries a settled-this-frame guard

## Choice

The upstream-refresh pull carries a settled-this-frame guard: an upstream that already has a run row in the frame but no in-flight run is not re-affirmed on receiver re-visits — its value is already in the receiver's drained wait-set. Still-running or just-woken upstreams are unaffected; the guard protects frame termination without weakening the rendezvous (see `story:multi-hard-dep-rendezvous`, `concept:cascade`).

## Rationale

Without the guard, two forced-refresh upstreams settling independently in one frame would mutually re-seed and the frame would never terminate; the deterministic two-upstream-refresh scenario stands as the regression pin.

## Alternatives

- Suppress upstream re-affirmation on every receiver re-visit — rejected: breaks the multi-hard-dep rendezvous, which depends on re-affirming upstreams not yet settled in the frame.
