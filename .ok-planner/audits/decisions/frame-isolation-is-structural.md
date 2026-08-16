---
audit: frame-isolation-is-structural
artifact: decision:frame-isolation-is-structural
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:30:00Z
---

# Frame isolation as a structural property, and its six concept boundaries

Supported, on both the corpus half and the code half. All six concepts the decision names — frame, node, instance, attribute, signal, and cascade-mode — carry the piece assigned to them, stated positively: none is silent and none states it as a policy or a default. On the code side the four row kinds the decision says never cross a boundary are bounded structurally, not by filters — node-run and wait-set rows carry a non-null frame reference cascade-deleted with the frame, attribute rows hang off a node-run, and run scopes are per-frame because each frame creates its own root in the transaction that inserts the frame row. Every one of the eight runtime surfaces the decision enumerates was checked and each keys on run scope or frame: substitution pins its senders from the current frame's wait-set rows and falls back only within the current scope tree; the diff-gate's prior-run lookup joins on the same run scope and, finding nothing, falls back to the template's schema defaults rather than to any earlier row; the cascade-mode dedup lookups take a run scope; wait-set inserts take a frame; the cascade walker carries the sender's run scope to the receiver; and the two operator-triggered paths reach the boundary explicitly — recalculate abandons the request unless the target's latest run sits in the instance's currently-running frame, and the debug-override entry point returns a cross-frame error naming both frames rather than acting. No opt-in exists: sweeping the whole tree for a cross-frame or widened mode finds no configuration key, no template field, and no code path, and the one query in the surface that is node-keyed rather than scope-keyed is used only to locate a scope, with each of its runtime and control callers either comparing frames explicitly or reading for external observability. All five rejected alternatives are absent from the tree.
