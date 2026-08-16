---
audit: layer-ordering
artifact: decision:layer-ordering
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 43
unaccounted: 0
---

# Whether the four layers exist in the declared order with the dependency lint holding the direction

Supported. The three layer directories exist under the root module beneath the separate foundation module, and the dependency lint carries one rule per upward-facing boundary: the foundation rule denies the three layers above it, the graph rule denies runtime and control, and the runtime rule denies control — each also denying the binary layer, which is what leaves the control layer needing no rule of its own. Reality holds across all 43 packages in the three lower tiers: 23 foundation packages import nothing above them, 6 graph packages import no runtime or control, and 14 runtime packages import no control, so the dependency graph is the acyclic ordering the choice names. A fitness test asserts all four rules remain and still deny their layers, and the rejected module-per-layer alternative is accurately described, since three of the four tiers share one module.
