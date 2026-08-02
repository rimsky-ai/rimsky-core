---
audit: debug-channel
artifact: story:debug-channel
determination: supported
commit: b767a27d
audited: 2026-08-02T09:41:29Z
---

# Operator override-invalidate / override-set via control-API, gated to explicit debug mode

Supported. `POST /v1/instances/{id}/debug/override` (`lib/control/controlapi/debug_override.go`) implements both `invalidate_node` and `set_attribute` actions, gated on the instance being paused or holding an unresumed pause-mode breakpoint hit, and returns HTTP 409 with both legal-state names otherwise. `test/scenarios/story_debug_channel_e2e_test.go::TestStoryDebugChannel_GateAndOverrideAcrossRealStack` (tagged `@story: debug-channel`) checks all of it against a real stack: a healthy (non-debuggable) instance is refused with 409 and leaves no audit row; a paused instance accepts `invalidate_node` and the graph is actually mutated (new stale row, audit event written); a breakpoint-held instance accepts `set_attribute` and the value is merged into the in-flight run's attribute row and audited; and a restricted API key lacking `instance:debug-override` is refused with 403 without writing an audit row. Both of the story's named actions and both of the story's named debug-mode entry states are exercised.
