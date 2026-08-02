---
audit: inproc-utility-executor
artifact: story:inproc-utility-executor
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:52Z
---

# Utility node kinds dispatch in-process without an external executor deployment

Supported. Three builtin handlers (`loop_counter`, `attribute_passthrough`, `send_message`) are registered into an in-process executor registry and into the node-kind alias map unconditionally at control-API and supervisor startup (`builtin.RegisterAllKindAliases` / `builtin.RegisterAllInProcessHandlers`, both called with no operator opt-in), so a template author references a bare `kind:` (e.g. `loop_counter`) and the kind-sugar resolver rewrites it to the builtin's in-process executor alias without any `executors:` config entry. `test/scenarios/inproc_utility_executor_e2e_test.go` exercises this end-to-end against a template that declares `kind: loop_counter` with no `executor:` field, asserting the kind-sugar resolver — not a pre-spelled executor — does the resolution, and a second scenario (`loop_counter_cap_e2e_test.go`) drives it to completion.
