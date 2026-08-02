---
audit: run-scope-is-per-frame
artifact: decision:run-scope-is-per-frame
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:34Z
---

# RunScope lives inside exactly one frame

Supported. The frame producer (`openRunningFrameForMessage`) creates the root RunScope and the frame row together, in the same transaction, before the frame row insert — the root RunScope's ID is generated first and handed to `Frames().InsertRunningFrame` as its `root_run_scope_id`, matching the "created inside a frame, at frame start, same tx" claim. Sub-graph and fan-out-partition RunScopes are created elsewhere (child-execution dispatch) parented to whatever RunScope invoked them, consistent with the three-kind taxonomy the decision names; there is no code path that creates a RunScope independent of some frame's tree. RunScope-scoped queries observed elsewhere in this audit pass (diff-gate baseline, cascade-mode dedup) key directly off `run_scope_id` with no additional frame filter, which only stays correct because RunScope identity is already frame-bounded — the structural property the decision argues for.
