---
audit: entry-absorption-flag
artifact: decision:entry-absorption-flag
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:10:25Z
---

# Entry absorption is a boolean on the dispatch-children input, not a pre-step

Supported. The dispatch-children primitive's input struct carries an entry-absorbed boolean, and the primitive consumes it inside its own body to gate the recursive-delegation rejection before creating any child scope. Both call sites of the primitive — the two that exist, one per child-execution mechanism — set the field explicitly rather than leaving it to a default: the delegation path sets it true, the fan-out path false. No absorption pre-step runs before either call, and there is no second dispatch shape: the primitive is the only creator of child run-scopes and child runs on the run side, since the underlying child-run creation helper is called from nowhere else. The absorption property itself is stamped onto the node definition at template canonicalization and is rejected outright when an author tries to declare it, so the runtime flag is derived rather than author-supplied. Registration and dispatch tests cover the recursion rejection the flag gates, and the delegation and fan-out scenarios exercise both settings end to end.
