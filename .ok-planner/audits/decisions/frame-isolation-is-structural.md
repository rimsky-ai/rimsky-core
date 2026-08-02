---
audit: frame-isolation-is-structural
artifact: decision:frame-isolation-is-structural
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:34Z
---

# Frame isolation is a structural invariant, not a tunable

Supported. Checked all six concept boundaries the decision names and found each enforced by RunScope/frame identity rather than by a per-call qualifier: the node identity row (`NodeRow`) carries no frame pointer at all (migration `018-frame-isolation-restoration.sql` dropped the mutable `frame_id`/`updated_at` columns it once had, and the current row shape confirms it stayed dropped); attribute state resets to schema defaults at every new frame's dispatch bag (regression-pinned by `TestPerRunAttributes_FreshScopeDefaultsAtFrameStart`, which mutates in frame 1 and asserts frame 2's starting bag is the factory default); the `attribute/<key>/changed` diff-gate baseline (`GetPriorRunData`) is scoped to the same `run_scope_id` as the current run, with no cross-scope fallback; cascade-mode dedup (`applyCascadeModeRule`/`bagsEqual`) looks up prior queued/settled runs keyed by `(node_id, run_scope_id)`; and operator-triggered recalculate (`RecalculateNode`) explicitly reads the instance's current running frame and no-ops if the target's latest run isn't in that frame. Because RunScope rows themselves never span frames (per `decision:run-scope-is-per-frame`), every one of these lookups is intra-frame by construction rather than by discipline, matching the "no per-call frame qualifier" claim. No opt-in flag, widened mode, or cross-scope fallback was found anywhere these mechanisms are implemented.
