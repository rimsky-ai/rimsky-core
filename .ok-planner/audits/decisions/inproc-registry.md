---
audit: inproc-registry
artifact: decision:inproc-registry
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:52Z
---

# Builtin in-process handlers are wired by explicit construction at supervisor startup, not init-time self-registration

Supported. `lib/runtime/executor/builtin/builtins.go` holds a single `builtinEntries()` function listing all 3 builtin handlers (`loop_counter`, `attribute_passthrough`, `send_message`) with their constructed handler instances, schema, tags, and error classes; `lib/runtime/supervisor.go`'s setup path calls `executor.NewInProcessRegistry()` then `builtin.RegisterAllInProcessHandlers(inprocReg)`, an explicit, statically-enumerable call with no package-`init` side effects anywhere in the builtin tree. `lib/runtime/executor/builtin/builtins_test.go` constructs a fresh registry in-test and asserts all 3 builtins land in it, confirming the registry is independently constructible with arbitrary handler sets as the decision claims.
