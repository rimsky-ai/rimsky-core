# `node.TemplateSpec` JSON tags — Design

## Status

- Spec, 2026-05-02.
- Follow-up captured during the rimsky-cli / rimsky-compose plan execution
  (`docs/plans/2026-05-02-rimsky-cli-and-compose.md`, see notes file Tasks 7 / 16).
- Foundational dependencies:
  - `docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md` §1.5 — POST /templates wire shape (`{spec, tag, source}`).
  - `core/canonical/jcs.go` — `CanonicalSpecHash` is the registry's identity function (RFC 8785 JCS over `json.Marshal(spec)`); changing the marshaled bytes changes every template's hash.

## Context

The CLI/compose plan execution surfaced a casing mismatch between the
on-disk YAML representation of template specs and the JSON wire shape
the control-api accepts. The mismatch has been worked around in three
places independently; this spec proposes to remove the workarounds by
adding `json:` struct tags directly to `core/node/template.go`.

The diagnosis below describes what is in the tree today, what the wire
actually carries, and what changes when tags are added — including the
hash-bytes ripple and the dev-DB nuke that comes with it.

---

## 1. The casing mismatch

### 1.1 What the YAML looks like

Operators write template specs in lowercase YAML. The reference example
in the v1 minimal scaffold:

```yaml
name: example
version: "1.0"
frame_resolution: coalesce
nodes:
  - type: hello
    executor: http-node
    userdata:
      url: "http://example.invalid/hello"
      method: GET
    attributes:
      schema:
        type: object
        additionalProperties: true
```

This is the document the user reads, writes, and reasons about. Every
key is lowercase-snake-case (`frame_resolution`, `params_schema`, etc.).

### 1.2 What the Go types look like

`core/node/template.go::TemplateSpec`:

```go
type TemplateSpec struct {
    Name            string
    Version         string
    Description     string
    FrameResolution string `yaml:"frame_resolution" json:"frame_resolution"`
    FrameTimeoutMs  int64  `yaml:"frame_timeout_ms,omitempty" json:"frame_timeout_ms,omitempty"`
    Nodes           []TemplateNodeDef
    ParamsSchema    map[string]any
    ParamsRedact    []string
}
```

Notice: `FrameResolution` and `FrameTimeoutMs` carry both a `yaml:` and
a `json:` tag. The other six fields have **no tags**. The same gap
exists on `TemplateNodeDef`, `NodeStoreRef`, `NodeLockRef`,
`NodeAttributesDef`, `InheritEntry`, `qualityrule.Spec`,
`ErrorTypePolicy`, `PolicyAction`, and the rest of the spec tree (`grep
-c json: core/node/template.go` returns 2; the entire type tree has just
those two tagged fields).

### 1.3 What the encoders do without tags

Default behavior of the two encoders involved:

| Encoder | Default key |
| --- | --- |
| `yaml.v3` (used by the CLI to read the user's YAML) | lowercases the Go field name verbatim — `FrameTimeoutMs` → `frametimeoutms` (no underscore) |
| `encoding/json` (used by the control-api wire and `CanonicalSpecHash`) | uses the Go field name verbatim — `Name` stays `Name`, `Nodes` stays `Nodes` |

So a `TemplateSpec` value, marshaled the two ways, produces:

```yaml
# yaml.Marshal(spec) — no tags
name: x
version: "1.0"
frame_resolution: coalesce  # tag honored
nodes: [...]
```

```json
// json.Marshal(spec) — no tags
{
  "Name": "x",
  "Version": "1.0",
  "frame_resolution": "coalesce",
  "Nodes": [...]
}
```

The `frame_resolution` field is the only one whose name matches across
both representations because it has explicit tags. Every other field is
**case-mismatched against the user's mental model** the moment it
crosses the JSON boundary.

### 1.4 Why the system works in spite of this

Three accidents conspire:

1. **YAML decoding is loose.** `yaml.v3.Unmarshal([]byte("name: x"), &spec)`
   matches the lowercase YAML key against the Go field `Name` because
   the library lowercases Go field names for matching. So loading the
   user's YAML into `TemplateSpec` works without tags.
2. **JSON decoding is case-insensitive on field names.** Go's
   `encoding/json` decoder, when comparing a JSON key to a struct field
   name, falls back to a case-insensitive match if the case-sensitive
   one fails. So `{"Name": "x"}` decodes into `Name string \`json:"name"\``
   *and* `{"name": "x"}` does too. This is what hides the
   inconsistency between the CLI's wire body and the control-api's
   shadow-type expectations.
3. **The control-api maintains a parallel type tree** with explicit
   tags so the *decode* side never depends on the case-insensitive
   fallback. `core/controlapi/templates.go` defines
   `templateDeployRequest`, `templateNodeDefJSON`, `nodeStoreRefJSON`,
   `nodeLockRefJSON`, `inheritEntryJSON`, `nodeAttributesDefJSON`,
   `qualityRuleJSON`, `errorTypePolicyJSON`, `policyActionJSON` — every
   one a duplicate of a `core/node/` type, every one with `json:"…"`
   tags on every field, and `toTemplateSpec` walks the parallel tree
   converting wire shapes back to domain shapes.

### 1.5 Where the system pays the cost

Three places carry the workaround:

- **`core/controlapi/templates.go:42-178`** — the shadow type tree
  (~140 lines). Every change to `core/node/template.go` requires a
  parallel change here.
- **`core/cli/templates.go::readSpecFile` and `core/cli/compose/resolver.go::ResolveTemplate`** —
  rather than marshal `TemplateSpec` directly, both functions parse YAML
  into a generic `map[string]any` and ship that map verbatim as the
  JSON wire body. The helper `yamlToJSON` (resolver.go) converts
  `yaml.v3`'s `map[any]any` shape into JSON-marshallable
  `map[string]any`. The cleaner code path —
  `yaml.Unmarshal → typed → json.Marshal → wire` — produces capital-N
  JSON keys, which work due to accident #2 above but contradict the
  documented wire shape and the operator's mental model.
- **`core/cli/internal/clitest/state.go::hashSpec`** — round-trips a
  spec map through `node.TemplateSpec` and `canonical.CanonicalSpecHash`
  to get a hash that matches what the live control-api would produce.
  This works only because the typed-spec field-tag asymmetry is mirrored
  in production.

---

## 2. Proposed change

### 2.1 What lands

Add `json:` tags to every field of every type in `core/node/template.go`
that participates in the wire shape:

- `TemplateSpec`: `name`, `version`, `description`, `nodes`,
  `params_schema`, `params_redact` (the `frame_resolution` and
  `frame_timeout_ms` tags already exist).
- `TemplateNodeDef`: `type`, `description`, `executor`, `userdata`,
  `schedule`, `dependencies`, `stores`, `locks`, `inherits`,
  `attributes`, `quality_rules`, `error_types`.
- `NodeStoreRef`: `name`, `selector`, `intent`, `alias`. (yaml tags
  already exist; mirror them as json tags.)
- `NodeLockRef`: `name`.
- `InheritEntry`: `claim`.
- `NodeAttributesDef`: `schema`.
- `ErrorTypePolicy`, `PolicyAction`, `qualityrule.Spec` — same
  pattern, lowercase-snake-case keys.

`omitempty` should match the existing convention on each field's
nullability — `description` and `params_schema` are presence-optional,
`nodes` and `name` are required.

### 2.2 What gets removed

In `core/controlapi/templates.go`:

- The shadow type tree: `templateDeployRequest`, `templateNodeDefJSON`,
  `nodeStoreRefJSON`, `nodeLockRefJSON`, `inheritEntryJSON`,
  `nodeAttributesDefJSON`, `qualityRuleJSON`, `errorTypePolicyJSON`,
  `policyActionJSON`.
- `(r *templateDeployRequest).toTemplateSpec()` and its callers.
- `decodeRegisterRequest` decodes a wrapper `{spec, tag, source}` where
  `spec` is now decoded directly into `node.TemplateSpec`.

In `core/cli/compose/resolver.go`:

- `yamlToJSON` and the YAML→generic-map round-trip.
- `ResolveTemplate` returns `(hash string, spec node.TemplateSpec, err error)`
  instead of `(hash, specMap map[string]any)`. Callers in `apply.go`
  marshal directly.

In `core/cli/templates.go`:

- `toJSONShape` and `readSpecFile`'s map round-trip; `readSpecFile`
  returns `node.TemplateSpec` typed.

In `core/cli/client.go::RegisterTemplateRequest`:

- `Spec` becomes `node.TemplateSpec` instead of `map[string]any`. The
  CLI library now imports `core/node`, but it already does for the
  hash logic via `canonical`.

### 2.3 What stays the same

- `core/canonical/jcs.go` is unchanged. It still marshals
  `TemplateSpec` to JSON, JCS-canonicalizes, and SHA-256s. The bytes it
  produces are different (lowercase keys instead of capital), but the
  function is the same.
- The wire body shape is unchanged from the operator's perspective. The
  user still POSTs `{"spec": {"name": "x", "version": "1.0", ...}}`. The
  case-insensitive accident that hid the bug is no longer needed; the
  system is correct rather than accidentally working.
- YAML decoding is unchanged. yaml.v3 ignores `json:` tags and uses
  either the existing `yaml:` tag or the lowercased Go field name.

---

## 3. Hash-bytes ripple

This is the load-bearing concern.

### 3.1 The mechanism

`CanonicalSpecHash(spec)` runs `json.Marshal(spec)`, JCS-canonicalizes,
and SHA-256s. Today, `json.Marshal` produces:

```json
{"Name":"x","Version":"1.0","frame_resolution":"coalesce","Nodes":[...]}
```

After tags land, the same input produces:

```json
{"name":"x","version":"1.0","frame_resolution":"coalesce","nodes":[...]}
```

The SHA-256 of those two byte sequences is different. **Every existing
template's hash changes.** Pre-v1 rules permit this — there are no
production templates and no operator's data to migrate — but it does
mean:

- Every dev DB has stale hashes; the simplest fix is to drop and
  recreate (`docker compose -f deploy/docker-compose.yml down -v` to
  nuke the postgres volume, then `up -d` for a fresh schema).
- Any test that round-trips through `CanonicalSpecHash` and asserts on
  the produced hash will get a different (still-valid) hash. Tests that
  compare two such hashes for equality continue to work.
- Any test that hard-codes a specific 64-hex hash literal would break,
  but a survey of the tree (`grep -rn 'sha256-[0-9a-f]\{8,\}' core/ test/`)
  finds **zero** such literals — the existing fake-hash stubs in
  `core/cli/client_test.go` are short fictional values like
  `sha256-abc` that never claim to be canonical hashes.

### 3.2 Test impact

The realistic test impact:

- `core/canonical/jcs_test.go` likely has fixture-based equality tests.
  Read the file; either the assertions are on stability (same input
  produces same output → still true), or there's a hard-coded hash that
  needs regenerating.
- `core/cli/internal/clitest/state.go::hashSpec` continues to work
  unchanged (it calls `CanonicalSpecHash`, which produces the new
  hashes; CLI tests then compare against `compose.ResolveTemplate`,
  which also produces new hashes — both sides shift together).
- Any scenario test under `test/scenarios/` that loads a fixture
  template, deploys it, and reads back the row's `template_hash` will
  observe the new hash. As long as those tests don't compare against a
  pre-recorded hash literal, they continue to pass.

The one place that needs explicit handling: existing `template_hash`
foreign keys on `rimsky_instances` that point at the old hashes. After
the migration, those FK rows still reference old hashes that no longer
exist in `rimsky_templates`. The simplest answer is "nuke the dev DB"
(rules.md "Pre-v1 — break freely"). If anyone has data they want to
preserve, they re-register the templates and recreate the instances by
hand.

### 3.3 Detection

Add a release-note-style entry to `CHANGELOG.md` flagging the
hash-bytes change with the dev-DB-nuke instruction. The migration
runner doesn't need a schema migration — the schema is unchanged; only
the values that get stored are different on the next register.

---

## 4. Affected files

Concrete file list for the cleanup PR:

**Core type changes:**
- `core/node/template.go` — add `json:` tags to all fields.
- `core/qualityrule/spec.go` (or wherever `qualityrule.Spec` lives) — add tags.

**Removals:**
- `core/controlapi/templates.go` — delete shadow type tree (~140 lines), delete `toTemplateSpec`, simplify `decodeRegisterRequest` to decode straight into `node.TemplateSpec`.

**CLI simplifications:**
- `core/cli/client.go` — `RegisterTemplateRequest.Spec` becomes typed `node.TemplateSpec`.
- `core/cli/compose/resolver.go` — drop `yamlToJSON`, return typed spec.
- `core/cli/compose/apply.go` — `applyStep` for `ActionRegister` marshals typed spec.
- `core/cli/templates.go` — drop `toJSONShape`, `readSpecFile` returns typed spec.

**Tests:**
- `core/canonical/jcs_test.go` — regenerate any hash fixtures.
- `core/controlapi/templates_test.go` — re-validate any hash assertions; the wire-decode tests should continue to pass since the JSON keys haven't changed for the operator.
- All scenario tests using template fixtures — verify they don't assert hard-coded hashes; if they do, regenerate.

**Documentation:**
- `CHANGELOG.md` — Unreleased bullet flagging the hash-bytes change and dev-DB nuke.
- `CLAUDE.md` — update the "Templates are content-addressed" gotcha to note that v1's hash bytes are not pinned across this change.
- This design doc — `docs/2026-05-02-template-spec-json-tags-design.md` — moved to `docs/history/` once the work lands.

---

## 5. Why this isn't already done

The plan-execution surfaced this as Task 7/16 noted in
`docs/plans/2026-05-02-rimsky-cli-and-compose-notes.md`. The reason it
was deferred from the CLI plan: the work is conceptually simple but
mechanically broad — it touches `core/node/`, `core/controlapi/`,
multiple `core/cli/` files, and ripples through every test that
exercises the template registry. Rolling it into the CLI plan would
have:

- Added 100+ lines of test churn unrelated to CLI semantics.
- Required a dev-DB nuke flagged mid-plan rather than at a designed
  boundary.
- Coupled CLI rollout to a hash-bytes change that benefits the whole
  codebase, not just the CLI.

Splitting it out lets this design land as its own coherent change with
its own test sweep and changelog entry.

---

## 6. Out of scope

- Schema migrations — none are needed; the schema is unchanged.
- Backwards-compatible hash forms — pre-v1 rules say no compat shim.
- Renaming `qualityrule.Spec` to a less collision-prone name.
- Adding `omitempty` policy review on the existing tagged fields.
- Adding `json:` tags to `node.TemplateSpec`'s sibling types that don't
  cross the wire (e.g. `EvaluatorState`).

---

## 7. Summary of decisions

| # | Decision | Rationale |
|---|---|---|
| 1 | Add `json:` tags directly to `core/node/template.go` | Single source of truth; removes the parallel type tree |
| 2 | Delete `controlapi/templates.go` shadow types in the same change | The shadow tree only existed to compensate for the missing tags |
| 3 | CLI marshals typed `TemplateSpec` to wire | Drops `yamlToJSON` workaround; one round-trip instead of two |
| 4 | Hash bytes change; pre-v1 dev-DB nuke is the migration | No production data to preserve; rules.md authorizes this |
| 5 | Land as its own PR, not piggybacking on CLI plan | Test ripple deserves a dedicated review window |
