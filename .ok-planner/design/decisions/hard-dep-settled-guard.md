---
decision: hard-dep-settled-guard
status: as-is
---

# The hard-dep pull carries a settled-this-frame guard

## Choice

The upstream-refresh pull carries a settled-this-frame guard: an upstream that already has a run row in the frame but no in-flight run is not re-affirmed on receiver re-visits — its value is already in the receiver's drained wait-set. The in-flight probe runs first, so a still-running or just-woken upstream falls through to the normal gate-insert path; the guard protects frame termination without weakening the rendezvous (see `story:multi-hard-dep-rendezvous`, `concept:cascade`).

## Rationale

Without the guard, two `force_upstream_refresh: true` upstreams settling independently in one frame would mutually re-seed and the frame would never terminate; the deterministic two-upstream-refresh scenario stands as the regression pin.
