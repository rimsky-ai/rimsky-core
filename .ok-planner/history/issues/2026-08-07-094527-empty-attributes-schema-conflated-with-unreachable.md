---
issue: empty-attributes-schema-conflated-with-unreachable
kind: human
category: bug
artifacts:
  - concept:attribute
  - concept:executor
  - story:host-agent-late-bind-all-protocols
status: repaired
opened: 2026-08-07T09:45:27Z
github: https://github.com/rimsky-ai/rimsky-core/issues/87
---

# An empty expected_attributes_schema was indistinguishable from an unreachable executor

Re-verified on the current tree: the conflation was real and wider than the
two sites the issue named. Every reader of an `(schema []byte, ok bool)`
expected-attributes-schema hook — `lib/graph/node/template_validator_attrschema.go`
(registration), `lib/runtime/runner_dispatch.go::computeEffectiveAttributeSchema`
(dispatch) — gated "schema is visible" on `ok && len(schema) > 0` instead of
`ok` alone. The root cause traced one hop further than the issue's own
citations: `lib/control/observability/expected_attributes_schema_resolver.go`,
the production resolver wired to dispatch-time `ExpectedAttributesSchemaFor`,
itself collapsed `len(schema) == 0` into `ok=false` — so even a genuinely
reachable, discovered executor that legitimately advertises no schema was
reported as "not visible," independent of the two callers' own conflation.

**Rule that determined the fix.** Not a new design question. `templates.go`'s
own `isLateBind` hook already states the intended contract unambiguously —
`return nil, true` for a late-bound executor, i.e. `ok` means "known,"
independent of byte length. `concept:attribute`'s invariant on the
delegated-naming-authority carve-out ("either the executor declares no
enumerated properties block (a fully permissive schema) ... the executor has
delegated naming authority for unenumerated properties") treats an
executor's genuinely-empty schema as a legal, permissive state — not an
unavailable one. The two booleans ("is the schema known" vs "does it have
enumerated content") were never actually one boolean; every site conflating
them had exactly one correct un-conflation.

**What changed.**
- `lib/control/observability/expected_attributes_schema_resolver.go`: returns
  `(schema, true)` whenever the executor is discovered with non-nil
  capabilities, regardless of `len(schema)`; only logs at Debug when the
  advertised schema is empty.
- `lib/graph/node/template_validator_attrschema.go::validateAttributesSchema`:
  sets `execSchemaVisible = true` on `ok` alone; only attempts to unmarshal
  and extract read-only properties when `len(execBytes) > 0`.
- `lib/runtime/runner_dispatch.go::computeEffectiveAttributeSchema`: same
  un-conflation for the dispatch-time hook.

A late-bound executor node declaring `attributes:` now registers instead of
failing with "expected_attributes_schema is not visible at registration,"
and a reachable executor that legitimately advertises an empty (fully
permissive) schema no longer fails dispatch with `executor_schema_unavailable`.

**Verified.** `go build ./...`; `go test ./lib/graph/node/... ./lib/runtime/...
./lib/control/observability/...` pass; `make lint` clean.
