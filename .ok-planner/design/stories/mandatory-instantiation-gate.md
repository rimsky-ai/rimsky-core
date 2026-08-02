---
story: mandatory-instantiation-gate
---

# Instance create validates value constraints

## Story

As an operator creating an instance from a deployed template, I can trust that rimsky validates statically-knowable attribute config against every referenced service's schema — including value constraints, not just shape — and refuses the create with a clear error if anything is statically misconfigured, so that bad config is caught at create time rather than as a mid-run dispatch failure.
