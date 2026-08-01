---
story: mandatory-instantiation-gate
status: as-is
---

# Instance create validates value constraints

## Story

As an operator creating an instance from a deployed template, I can trust that rimsky validates statically-knowable attribute config against every referenced service's schema — including value constraints, not just shape — and refuses the create with a clear error if anything is statically misconfigured, so that bad config is caught at create time rather than as a mid-run dispatch failure.

Mandatory instantiation gate: rimsky validates statically-knowable attribute config (including value constraints, not just shape) against every referenced service's schema before persisting the instance.

Bad config is caught at create time with a clear naming of the offending attribute, not mid-run as a dispatch failure that wastes runtime resources and produces a confusing error trail.
