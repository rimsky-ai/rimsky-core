---
audit: kind-sugar-resolver
artifact: decision:kind-sugar-resolver
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:40Z
---

# The optional template `kind` field resolves to a pre-registered executor via a static alias map, mutually exclusive with `executor`

Supported. `lib/graph/node/kind_resolver.go`'s `KindAliasMap` is a static, registration-populated `map[string]string`; `builtin.RegisterAllKindAliases` in `lib/runtime/executor/builtin/builtins.go` populates it from the same `builtinEntries()` list that also drives `RegisterAllInProcessHandlers`, so the kind-alias map and the in-process executor registry are seeded together as claimed. `lib/graph/node/template_validator.go`'s `validateKindDeclaration` rejects a node declaring both `kind` and `executor` ("declares both kind and executor; pick one"), rejects an unregistered kind the same way an unregistered executor is rejected (`"kind %q is not registered"` / `"executor %q is not declared"`), and `CanonicalizeKindSugar` rewrites a resolved `kind` onto `Executor` and clears `Kind`. `lib/graph/node/kind_resolver_test.go` exercises all of it directly: happy-path resolution, rejection of mixed kind+executor, rejection of mixed kind+sends_message, rejection of an unknown kind, rejection when no alias map is configured, legality of a node with neither field (falls through to the ordinary executor-resolution path), and idempotence/nil-safety of the canonicalization pass — 6 of the decision's enumerated behaviors, each with its own passing test in the package's ordinary suite.
