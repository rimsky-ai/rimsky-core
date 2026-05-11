---
topic: attribute-substitution-grammar
kind: discipline
---

# Attribute substitution: a fixed closed grammar; validation gates twice (dispatch + commit)

## Description

Attributes are the typed inputs and outputs of a node, declared by a JSON Schema in the template's `attributes:` block (`docs/concepts/attributes.md`). The schema's `source:` fields carry `{{...}}` substitution directives that rimsky resolves at dispatch time. The grammar is fixed: only the source kinds declared in `modeling/attribute/substitution.go:7-13` are recognized, and `substitution.go:18-19` is explicit that "the grammar implemented here enumerates exactly the source kinds above and nothing else."

Six recognized source kinds:

- `{{deps.<node>.<field>}}` — upstream node's persisted `rimsky_node_attributes.data`.
- `{{claim.<alias>.address}}` — live claim's address bytes (post-`Open`).
- `{{claim.<alias>.payload.<field>}}` — live claim's payload at named path.
- `{{claim.<alias>.scope}}` — live claim's scope bytes.
- `{{params.<key>}}` — instance-level `rimsky_instances.params` blob.
- `{{nodes.<emitter>.event.<name>.<json_path>}}` — most-recent executor-emitted `NamedEvent` payload (added by the 2026-05-08 platform-extensions plan F4).

The grammar is implemented by `directivePattern` regex (`substitution.go:117`) plus `walkPath` (line 189-310). `walkPath` is the single sanctioned introspection site for substitution-leaf extraction from claim/event content; per `@blessed-invariant 20` (annotated at `substitution.go:21-40`) it lazy-unmarshals into a transient `map[string]any` only inside the leaf-extraction call and discards the map after extraction. The `stringifyRaw` helper (around line 280) is the second sanctioned exception — it unwraps a JSON-string value, otherwise returns the raw bytes verbatim.

Attribute validation gates twice (`@blessed-invariant 12` at `modeling/attribute/validate.go:9-26`):

1. **At dispatch (post-substitution)**: the populated attribute object is validated against the schema. Failure raises `template_resolution_failed` (the supervisor's policy chain maps the typed `ErrSchemaValidation{Phase: "dispatch"}` from `validate.go:47-63`).
2. **At commit (executor writeback)**: when the executor's `Complete` arrives, the writeback is merged and validated again. Failure raises `attributes_schema_failed` with `Phase: "commit"`.

The same `Validate(schema, data, phase)` function (`validate.go:98-152`) is the only entry point; the caller supplies the phase. Removing either call site is a regression of invariant 12 (annotated explicitly at line 24).

JSON Schema compilation uses `github.com/santhosh-tekuri/jsonschema/v5` (draft-07). Schemas are compiled per-call; the comment at `validate.go:92-97` notes that since validation runs at most twice per node-run, the cost is negligible against the surrounding postgres + HTTP work. A future cache keyed by template-id is mentioned but not implemented.

Substitution errors deliberately omit the value being walked — `ErrMissingSource` and `ErrSchemaValidation` cite path tokens only (`substitution.go:99-101`) — to preserve invariant 20 in error/event surfaces. The single introspection site stays single even in failure paths.

## Code surface

- `modeling/attribute/substitution.go` (entire file; grammar regex + walkPath + stringifyRaw).
- `modeling/attribute/validate.go` (entire file; `Validate`, `ErrSchemaValidation`, `PhaseDispatch`/`PhaseCommit`).
- `modeling/attribute/callback.go` — the writeback-callback shape used by executors at `Complete`.
- `foundation/integration/runner_dispatch.go:515` — commit-time re-validation site (`@blessed-invariant 12`).
- `foundation/integration/runner_dispatch.go:710-770` — wire-encoding site (the second sanctioned introspection exception for invariant 20).

## Prose surface

- `docs/concepts/attributes.md` — concept-reference treatment of the substitution grammar.
- `docs/concepts/userdata.md` — explicit contrast (`{{...}}` in userdata is literal, not substituted).
- `docs/concepts/scope.md` — `{{claim.<alias>.scope}}` resolves to opaque scope bytes.
- `CLAUDE.md` "Blessed invariants" §11 + §12 + §20.
- `.ok-planner/specs/2026-05-04-modeling-layer-contract.md` §5 — modeling-layer attribute validation contract.

## Adjacent topics

- `2026-05-10-opacity-of-userdata-claim-blob` — substitution is the sole introspection exception to opacity.
- `2026-05-10-event-log-append-only-jsonb` — `rimsky_node_events` is the read side for `nodes.<emitter>.event.<name>` substitution.
- `named-events-and-on-event-handlers` — emit side of the event source kind.

## Observations

- The grammar enumerates six kinds inline; CLAUDE.md and `docs/concepts/attributes.md` cite five (omitting the event-source kind in some passes). `substitution.go:7-13` lists five, with the sixth (`nodes.<emitter>.event.<name>.<json_path>`) added separately in the `ResolveContext.EventLookup` field comment at line 72-90. A reader counting from one doc surface alone gets a stale grammar.
- The same `Validate` function is used for both gates; the phase string is the only differentiator and is "free-form for forward compatibility" (`validate.go:72-76`) — callers could accidentally pass `phase: "dispach"` and the error would still be raised, just with a typo in the event payload. Not enforced.
- `walkPath` is described as "single introspection site," but there is a second site at `foundation/integration/runner_dispatch.go::makeStoreHandle` (lines 710-770) — the wire-encoding projection of address/payload into the executor's `google.protobuf.Struct`. The substitution.go comment at line 33-37 calls this out explicitly as "one additional sanctioned exception" outside the package.
- `stringifyRaw` is a third partial-introspection site within the same package (top-level address/scope directives) — it's named in the invariant-20 docstring at lines 26-32 as a sanctioned shape-flattening site. So the precise rule is "two sanctioned sites in `modeling/attribute/substitution.go` plus one in `foundation/integration/runner_dispatch.go`."
