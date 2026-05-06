# `node.TemplateSpec` JSON tags Implementation Plan

**Goal:** Add `json:` tags to every wire-relevant field of the template-spec type tree under `core/node/` (and `core/qualityrule/`), then delete the workarounds those missing tags forced into `core/controlapi/templates.go`, `core/cli/templates.go`, `core/cli/compose/resolver.go`, `core/cli/compose/apply.go`, `core/cli/compose/plan.go`, and `core/cli/run.go`.

**Architecture:** Single source of truth for the wire shape becomes `core/node/template.go` + `core/node/policy.go` + `core/qualityrule/spec.go`. The control-api decodes POST `/templates` bodies straight into `node.TemplateSpec`; the CLI marshals typed `node.TemplateSpec` straight to wire. The shadow type tree (`templateDeployRequest`, `templateNodeDefJSON`, `nodeStoreRefJSON`, `nodeLockRefJSON`, `inheritEntryJSON`, `nodeAttributesDefJSON`, `qualityRuleJSON`, `errorTypePolicyJSON`, `policyActionJSON`) and its `toTemplateSpec` mapper go away. The CLI's YAML-→generic-map round-trip (`toJSONShape` in `core/cli/templates.go`, `yamlToJSON` in `core/cli/compose/resolver.go`) goes away. The `hashRewrite` defense in `core/cli/compose/apply.go::ApplyPlan` (added explicitly to absorb pre-fix hash divergence between CLI and control-api) becomes dead code and is removed.

**Tech Stack:** Go 1.22+ (root `go.mod`); stdlib `encoding/json`; `gopkg.in/yaml.v3` (still used at the YAML→typed boundary); `github.com/cyberphone/json-canonicalization` (unchanged). No new dependencies.

---

## Pre-flight context for the implementer

Read these before starting. The plan is self-contained but the design rationale, blessed-invariant impact, and surrounding rules live elsewhere:

- The spec: `docs/history/2026-05-02-template-spec-json-tags-design.md`. Authoritative on every behavioral question. When the plan and the spec differ, the spec wins; flag the discrepancy and continue.
- The hash mechanism: `core/canonical/jcs.go::CanonicalSpecHash` runs `json.Marshal(spec)` → JCS canonicalization → SHA-256. Before this change, missing tags meant capital-N keys (`Name`, `Nodes`, …) in the marshaled bytes. After this change, lowercase-snake-case keys (`name`, `nodes`, `params_schema`, …) — so **every existing template's content hash changes**. There are no production templates and no consumers locked into a specific hash; under `.claude/rules/rules.md` ("Pre-v1 — break freely") this is acceptable.
- Pre-v1 dev-DB rule: any dev DB carrying templates registered before this change has stale hashes. Drop and recreate (`docker compose -f deploy/docker-compose.yml down -v && docker compose -f deploy/docker-compose.yml up -d`). Tests that boot a fresh testcontainers Postgres are not affected — their hashes are derived from the current code in the same run.
- The control-api decode entrypoint: `core/controlapi/templates.go::decodeRegisterRequest` decodes the wrapped body `{spec: {...}, tag, source}`. Today `Spec` is `*templateDeployRequest` — change to `*node.TemplateSpec`.
- The CLI register/run/compose call paths:
  - `core/cli/run.go` (the ergonomic top-level `run` command) and `core/cli/templates.go::RunTemplateRegister` both call `readSpecFile` to get a `map[string]any`, then `c.RegisterTemplate(ctx, RegisterTemplateRequest{Spec: spec, ...})`.
  - `core/cli/compose/plan.go::ComputePlan` calls `compose.ResolveTemplate(path)` returning `(hash, specMap)`, stashes the map on `Step.SpecBody`, and `apply.go::applyStep` passes `step.SpecBody` to `c.RegisterTemplate`.
- Cold-read / project rules: `.claude/rules/rules.md` and `.claude/rules/cold-read-cheatsheet.md`. Cold-read conventions apply to all new code. Pre-v1 rules permit hash-bytes change without a compat shim.
- Build & test commands per `CLAUDE.md`:
  - `go build ./...`
  - `go test ./...` (testcontainers spins up `postgres:15`; Docker must be running)
  - `make lint`
- Module path is `github.com/fallguy/rimsky`; `go.mod` is at the repo root.

The `hashRewrite` map in `apply.go::ApplyPlan` was added during the rimsky-cli plan execution explicitly to absorb the JSON-tag asymmetry that this change fixes (see the comment block at `core/cli/compose/apply.go:34-44` referencing this design doc). Once the asymmetry is gone, the rewrite is dead — both ends compute the same hash. Delete the map and the per-iteration rewrite logic.

Do **not** add `json:` tags to types that don't cross the wire (e.g. `EvaluatorState`, `ResolvedAction`, `Failure`, `EvalInput`, `Evaluator`, `BackoffConfig`). Out of scope per design doc §6.

---

## File structure

Files modified (no new files):

```
core/node/
  template.go                     # add json: tags to TemplateSpec, TemplateNodeDef, NodeStoreRef, NodeLockRef, NodeAttributesDef, InheritEntry
  policy.go                       # add json: tags to ErrorTypePolicy, PolicyAction

core/qualityrule/
  spec.go                         # add json: tags to Spec

core/controlapi/
  templates.go                    # delete shadow tree + toTemplateSpec; decode straight into node.TemplateSpec

core/cli/
  client.go                       # RegisterTemplateRequest.Spec: map[string]any -> node.TemplateSpec
  templates.go                    # delete toJSONShape; readSpecFile returns node.TemplateSpec
  run.go                          # readSpecFile returns typed; pass typed to RegisterTemplateRequest

core/cli/compose/
  resolver.go                     # delete yamlToJSON; ResolveTemplate returns (hash, node.TemplateSpec, err)
  resolver_test.go                # update assertions for typed return
  plan.go                         # Step.SpecBody: map[string]any -> node.TemplateSpec; specBodies map keyed by typed
  apply.go                        # delete hashRewrite map + per-iteration rewrite; pass typed Spec to RegisterTemplateRequest

core/cli/internal/clitest/
  state.go                        # hashSpec: continue accepting map[string]any (test helper); no functional change but verify it still works after the wire change

CHANGELOG.md                      # Unreleased entry: hash-bytes change + dev-DB nuke instruction
CLAUDE.md                         # update "Templates are content-addressed" gotcha to note hash bytes are not pinned across this change
```

Files deleted (none — all changes are in-place edits / removals within existing files).

Files moved at the end of the run (final location):

```
docs/history/2026-05-02-template-spec-json-tags-design.md
```

---

## Task 1 — Add `json:` tags to `core/node/template.go`

**Files:** `core/node/template.go`

**Steps:**

1. Open `core/node/template.go`. The current type declarations are at lines 27–93 and 105–107. The only fields that already carry `json:` tags are `FrameResolution` and `FrameTimeoutMs` on `TemplateSpec`.

2. Update `TemplateSpec` (currently at lines 27–36) to:

   ```go
   type TemplateSpec struct {
       Name            string             `yaml:"name" json:"name"`
       Version         string             `yaml:"version" json:"version"`
       Description     string             `yaml:"description,omitempty" json:"description,omitempty"`
       FrameResolution string             `yaml:"frame_resolution" json:"frame_resolution"`
       FrameTimeoutMs  int64              `yaml:"frame_timeout_ms,omitempty" json:"frame_timeout_ms,omitempty"`
       Nodes           []TemplateNodeDef  `yaml:"nodes" json:"nodes"`
       ParamsSchema    map[string]any     `yaml:"params_schema,omitempty" json:"params_schema,omitempty"` // JSON Schema
       ParamsRedact    []string           `yaml:"params_redact,omitempty" json:"params_redact,omitempty"`
   }
   ```

   Rationale: `description`, `params_schema`, `params_redact` are presence-optional → `omitempty`. `name`, `version`, `frame_resolution`, `nodes` are required by `node.ValidateTemplate` → no `omitempty`. `frame_timeout_ms` carries `omitempty` matching its current declaration (validator default-fills via `ApplyFrameResolutionDefaults` after decode).

3. Update `TemplateNodeDef` (currently at lines 50–63) to add `json:` tags on every field, mirroring the existing `yaml:` tag where present and adding lowercase-snake-case where absent:

   ```go
   type TemplateNodeDef struct {
       Type         string                     `yaml:"type" json:"type"`
       Description  string                     `yaml:"description,omitempty" json:"description,omitempty"`
       Executor     string                     `yaml:"executor,omitempty" json:"executor,omitempty"`
       Userdata     map[string]any             `yaml:"userdata,omitempty" json:"userdata,omitempty"`
       Schedule     string                     `yaml:"schedule,omitempty" json:"schedule,omitempty"`
       Dependencies []string                   `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
       Stores       []NodeStoreRef             `yaml:"stores,omitempty" json:"stores,omitempty"`
       Locks        []NodeLockRef              `yaml:"locks,omitempty" json:"locks,omitempty"`
       Attributes   NodeAttributesDef          `yaml:"attributes,omitempty" json:"attributes,omitempty"`
       QualityRules []qualityrule.Spec         `yaml:"quality_rules,omitempty" json:"quality_rules,omitempty"`
       Inherits     []InheritEntry             `yaml:"inherits,omitempty" json:"inherits,omitempty"`
       ErrorTypes   map[string]ErrorTypePolicy `yaml:"error_types,omitempty" json:"error_types,omitempty"`
   }
   ```

   Note: `Type` is required (no `omitempty`); `Executor` is optional (empty = pure-cascade node — see lines 47–49 doc comment). The shadow tree in `core/controlapi/templates.go` had `executor,omitempty` so this matches.

4. Update `NodeStoreRef` (currently at lines 72–77) to mirror the existing `yaml:` tags as `json:` tags:

   ```go
   type NodeStoreRef struct {
       Name     string `yaml:"name" json:"name"`
       Selector string `yaml:"selector" json:"selector"`
       Intent   string `yaml:"intent" json:"intent"` // "r" | "rw"
       Alias    string `yaml:"alias,omitempty" json:"alias,omitempty"`
   }
   ```

5. Update `NodeLockRef` (currently at lines 83–85):

   ```go
   type NodeLockRef struct {
       Name string `yaml:"name" json:"name"`
   }
   ```

6. Update `NodeAttributesDef` (currently at lines 91–93):

   ```go
   type NodeAttributesDef struct {
       Schema map[string]any `yaml:"schema,omitempty" json:"schema,omitempty"`
   }
   ```

7. Update `InheritEntry` (currently at lines 105–107):

   ```go
   type InheritEntry struct {
       Claim string `yaml:"claim" json:"claim"`
   }
   ```

8. Leave `AliasOf` and `RequiredStores` (lines 112–139) untouched.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/node/...` — must succeed.
- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go vet ./core/node/...` — must succeed.

---

## Task 2 — Add `json:` tags to `core/node/policy.go`

**Files:** `core/node/policy.go`

**Steps:**

1. Open `core/node/policy.go`.

2. Update `ErrorTypePolicy` (currently at lines 11–13) to:

   ```go
   type ErrorTypePolicy struct {
       Policy []PolicyAction `yaml:"policy" json:"policy"`
   }
   ```

3. Update `PolicyAction` (currently at lines 40–49) to:

   ```go
   type PolicyAction struct {
       Action         string             `yaml:"action" json:"action"`
       Count          int                `yaml:"count,omitempty" json:"count,omitempty"`
       Backoff        shared.BackoffKind `yaml:"backoff,omitempty" json:"backoff,omitempty"`
       Jitter         shared.JitterKind  `yaml:"jitter,omitempty" json:"jitter,omitempty"`
       BaseDelayMs    int                `yaml:"base_delay_ms,omitempty" json:"base_delay_ms,omitempty"`
       MaxDelayMs     int                `yaml:"max_delay_ms,omitempty" json:"max_delay_ms,omitempty"`
       Targets        []string           `yaml:"targets,omitempty" json:"targets,omitempty"`
       ReasonTemplate string             `yaml:"reason_template,omitempty" json:"reason_template,omitempty"`
   }
   ```

   Notes:
   - `Action` is required (every entry must declare an action), no `omitempty`.
   - `Count` is presence-optional in the JSON shape today (`count,omitempty` on the shadow tree's `policyActionJSON`); keep matching.
   - `Backoff` / `Jitter` are typed strings (`shared.BackoffKind`, `shared.JitterKind`); `omitempty` on a typed string fires when the value is the empty string, which matches the shadow tree.

4. Leave `EvaluatorState`, `ResolvedAction`, `Evaluate`, `step` untouched. They do not cross the wire (per design doc §6).

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/node/...` — must succeed.
- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go vet ./core/node/...` — must succeed.

---

## Task 3 — Add `json:` tags to `core/qualityrule/spec.go`

**Files:** `core/qualityrule/spec.go`

**Steps:**

1. Open `core/qualityrule/spec.go`.

2. Update `Spec` (currently at lines 12–16) to:

   ```go
   type Spec struct {
       Type     string          `yaml:"type" json:"type"`
       Config   map[string]any  `yaml:"config,omitempty" json:"config,omitempty"`
       Severity shared.Severity `yaml:"severity,omitempty" json:"severity,omitempty"` // default "error"
   }
   ```

3. Leave `Failure`, `EvalInput`, `Evaluator` untouched. They do not cross the wire.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/qualityrule/...` — must succeed.

---

## Task 4 — Confirm typed-spec marshaling produces the expected wire keys

**Files:** none modified; this is a one-shot check to catch any tag mistake from Tasks 1–3 before downstream cleanup that depends on the new shape.

**Steps:**

1. Run a quick repl-style check by writing a `_test.go` snippet (do not commit it). Add a temporary file `core/canonical/taghand_check_test.go` (do **not** prefix with `_`: the Go toolchain silently skips files whose names begin with `_` or `.`, and the test would never run):

   ```go
   package canonical

   import (
       "encoding/json"
       "strings"
       "testing"

       "github.com/fallguy/rimsky/core/node"
       "github.com/fallguy/rimsky/core/qualityrule"
       "github.com/fallguy/rimsky/core/shared"
   )

   func TestTagHandCheck(t *testing.T) {
       spec := node.TemplateSpec{
           Name:            "x",
           Version:         "1",
           FrameResolution: node.FrameResolutionCoalesce,
           Nodes: []node.TemplateNodeDef{
               {
                   Type:     "n",
                   Executor: "e",
                   Stores:   []node.NodeStoreRef{{Name: "s", Selector: "@p", Intent: "r"}},
                   Locks:    []node.NodeLockRef{{Name: "l"}},
                   Inherits: []node.InheritEntry{{Claim: "c"}},
                   Attributes: node.NodeAttributesDef{
                       Schema: map[string]any{"type": "object"},
                   },
                   QualityRules: []qualityrule.Spec{
                       {Type: "no_nulls", Severity: shared.Severity("warning")},
                   },
                   ErrorTypes: map[string]node.ErrorTypePolicy{
                       "x": {Policy: []node.PolicyAction{{Action: "retry", Count: 1}}},
                   },
               },
           },
       }
       raw, err := json.Marshal(spec)
       if err != nil {
           t.Fatal(err)
       }
       s := string(raw)
       for _, want := range []string{
           `"name":"x"`,
           `"version":"1"`,
           `"frame_resolution":"coalesce"`,
           `"nodes":[`,
           `"type":"n"`,
           `"executor":"e"`,
           `"stores":[`,
           `"selector":"@p"`,
           `"intent":"r"`,
           `"locks":[`,
           `"inherits":[`,
           `"claim":"c"`,
           `"attributes":{`,
           `"schema":{`,
           `"quality_rules":[`,
           `"severity":"warning"`,
           `"error_types":{`,
           `"policy":[`,
           `"action":"retry"`,
           `"count":1`,
       } {
           if !strings.Contains(s, want) {
               t.Errorf("expected %s in %s", want, s)
           }
       }
       // No capital-N leftovers.
       for _, bad := range []string{
           `"Name":`, `"Nodes":`, `"Type":`, `"Executor":`,
           `"Stores":`, `"Locks":`, `"Inherits":`, `"Attributes":`,
           `"QualityRules":`, `"ErrorTypes":`, `"Policy":`, `"Action":`,
           `"Count":`, `"Selector":`, `"Intent":`, `"Schema":`,
       } {
           if strings.Contains(s, bad) {
               t.Errorf("unexpected capital key %s in %s", bad, s)
           }
       }
   }
   ```

2. Run `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/canonical/ -run TestTagHandCheck -v`.

3. If it fails, the corresponding tag is missing or wrong — fix it in `core/node/template.go` / `core/node/policy.go` / `core/qualityrule/spec.go` and re-run.

4. Once it passes, **delete** `core/canonical/taghand_check_test.go`. This file is scaffolding for the implementer; it must not land in the final tree.

**Verification:**

- The temp test passes.
- `ls /Users/patrick/Documents/projects/research/verantel/submodules/rimsky/core/canonical/taghand_check_test.go` — file must not exist after the task.

---

## Task 5 — Delete the shadow type tree in `core/controlapi/templates.go` and decode straight into `node.TemplateSpec`

**Files:** `core/controlapi/templates.go`

**Steps:**

1. Open `core/controlapi/templates.go`.

2. Delete the shadow type declarations (currently at lines 42–108):
   - `templateDeployRequest`
   - `templateNodeDefJSON`
   - `nodeStoreRefJSON`
   - `nodeLockRefJSON`
   - `inheritEntryJSON`
   - `nodeAttributesDefJSON`
   - `qualityRuleJSON`
   - `errorTypePolicyJSON`
   - `policyActionJSON`

3. Delete `(r *templateDeployRequest).toTemplateSpec()` (currently at lines 110–178).

4. Update `templateRegisterRequest` (currently at lines 184–188) to wrap a typed spec:

   ```go
   type templateRegisterRequest struct {
       Spec   *node.TemplateSpec `json:"spec,omitempty"`
       Tag    string             `json:"tag,omitempty"`
       Source string             `json:"source,omitempty"`
   }
   ```

5. Update `decodeRegisterRequest` (currently at lines 708–719) to return the typed spec:

   ```go
   // decodeRegisterRequest decodes the wrapped POST /templates body shape
   // `{spec: {...}, tag, source}`. The legacy bare-spec shape was removed
   // alongside the control-plane v1 cutover; bodies missing the "spec" key
   // are rejected.
   func decodeRegisterRequest(body []byte) (specOut *node.TemplateSpec, tag, source string, err error) {
       var wrap templateRegisterRequest
       dec := json.NewDecoder(bytesReader(body))
       dec.DisallowUnknownFields()
       if err := dec.Decode(&wrap); err != nil {
           return nil, "", "", fmt.Errorf("invalid JSON body: %w", err)
       }
       if wrap.Spec == nil {
           return nil, "", "", fmt.Errorf("invalid JSON body: missing required field \"spec\"")
       }
       return wrap.Spec, wrap.Tag, wrap.Source, nil
   }
   ```

6. Update `handleDeployTemplate` (currently at lines 259–360) to remove the `specBody.toTemplateSpec()` call. Replace lines 266–276 with:

   ```go
       specBody, tag, source, err := decodeRegisterRequest(raw)
       if err != nil {
           badRequest(w, err.Error())
           return
       }
       if tag != "" && !validTag(tag) {
           badRequest(w, "invalid tag identifier")
           return
       }

       spec := *specBody
       res := node.ValidateTemplate(&spec, validatorHooksFor(deps))
   ```

   (everything below that line is unchanged.)

7. Update the file's package-level doc comment (currently at lines 1–11) to drop the reference to the JSON shape mirroring story:

   ```go
   // templates.go — POST /templates, GET /templates, GET /templates/:id,
   // DELETE /templates/:id.
   //
   // The deploy handler decodes JSON request bodies straight into
   // node.TemplateSpec (the in-memory representation, json-tagged) and
   // runs node.ValidateTemplate against the per-process store registry
   // (AppDeps.Stores). Concurrency-tag / owns-resources / reads-resources
   // fields were retired in the stores redesign (spec §11.3); the JSON
   // shape mirrors the current template shape: stores, locks, attributes,
   // quality_rules. Per the 2026-04-30 stores cleanup, claim_resolutions
   // is gone — store disposition is governed by per-store config, not by
   // the template.
   ```

8. Remove now-unused imports if any. After steps 2–6, check whether `qualityrule` and `shared` are still imported in this file. They are used elsewhere in the file (`qualityrule.Spec`, `shared.Severity`, `shared.BackoffKind`, `shared.JitterKind`) only inside the deleted `toTemplateSpec`. Run `goimports` (via `make lint`) at the end to detect this; if either becomes unused, remove the import.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/controlapi/...` — must succeed.
- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go vet ./core/controlapi/...` — must succeed.
- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && grep -n "templateNodeDefJSON\|nodeStoreRefJSON\|toTemplateSpec\|templateDeployRequest" core/controlapi/templates.go` — must return zero matches.

---

## Task 6 — Run `core/controlapi` tests against the new decode path

**Files:** none modified; this is a verification gate before touching the CLI.

**Steps:**

1. Run `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/controlapi/... -count=1`.

2. The package's tests construct register/deploy bodies as `map[string]any` with lowercase-snake-case keys (`name`, `version`, `frame_resolution`, etc. — confirmed by `grep "frame_resolution" core/controlapi/templates_test.go`). They were valid input to the old decoder via the case-insensitive accident **and** are still valid input to the new decoder because the JSON keys haven't changed for the operator. They must continue to pass.

3. If any test fails:
   - The most likely cause is a missing `omitempty` on a tag added in Tasks 1–3 that causes the marshaled spec to round-trip differently than expected. Fix the tag.
   - If a test asserts on a hash value (none do today; verify with `grep -n "sha256-[0-9a-f]\{8,\}" core/controlapi/templates_test.go` returns at most the `"sha256-" + repeatHex("a", 64)` regex-input-shape literal at line 140), regenerate any genuinely-broken hash literal — but the existing literal is a regex input, not a hash output, so it should still pass.

**Verification:**

- `go test ./core/controlapi/... -count=1` exits 0.

---

## Task 7 — Make `cli.RegisterTemplateRequest.Spec` typed

**Files:** `core/cli/client.go`

**Steps:**

1. Open `core/cli/client.go`.

2. Update the import block (currently at lines 11–22) to add `"github.com/fallguy/rimsky/core/node"`:

   ```go
   import (
       "bytes"
       "context"
       "encoding/json"
       "fmt"
       "io"
       "net/http"
       "net/url"
       "strconv"
       "strings"
       "time"

       "github.com/fallguy/rimsky/core/node"
   )
   ```

3. Update `RegisterTemplateRequest` (currently at lines 103–110):

   ```go
   // RegisterTemplateRequest is the wrapped POST /templates body shape per
   // control-plane v1 spec §1.5: `{spec: {...}, tag, source}`. Spec is the
   // typed template spec; tag and source are optional.
   type RegisterTemplateRequest struct {
       Spec   node.TemplateSpec `json:"spec"`
       Tag    string            `json:"tag,omitempty"`
       Source string            `json:"source,omitempty"`
   }
   ```

4. `RegisterTemplate(ctx, body)` (currently at lines 153–163) is unchanged in body; the type is updated transitively.

5. Leave the `Template` response struct's `Spec map[string]any` (currently at line 124) **as-is**. The response decode path is opaque — `GetTemplate` returns `Spec` for callers that introspect it generically (smoke tests, compose state queries), and switching to `node.TemplateSpec` would force every reader to know the typed shape. Out of scope for this design (per design doc §2.2: only the wire-body type changes; response decoding is untouched).

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/cli/` — will fail because `core/cli/templates.go`, `core/cli/run.go`, `core/cli/client_test.go`, and `core/cli/internal/clitest/server_test.go` still pass `map[string]any` into `RegisterTemplateRequest{Spec: ...}`. That is expected. Tasks 7a, 8, and 9 fix it.

---

## Task 7a — Update test fixtures that build `RegisterTemplateRequest{Spec: ...}`

**Files:** `core/cli/client_test.go`, `core/cli/internal/clitest/server_test.go`

**Steps:**

1. **Important scope note:** the `core/cli/internal/clitest` package's **fake server's internal API** keeps `srv.State.RegisterTemplate(spec map[string]any, tag, source string) (hash string, isNew bool)` and `hashSpec(spec map[string]any) string` map-typed. Those are *not* the wire body shape — they are the in-memory fake's storage API and are exercised directly by tests that pre-seed state without going through HTTP. Leaving them map-typed minimizes test churn and is conceptually correct: the fake stores opaque specs.

   What changes is **only** the typed shape of `cli.RegisterTemplateRequest.Spec` (which goes over HTTP to the fake or the real control-api). The plan must update every caller of `cli.RegisterTemplateRequest{Spec: …}` that still passes a map.

2. Open `core/cli/internal/clitest/server_test.go`. The helper at lines 12–22 is:

   ```go
   func minimalSpec() map[string]any {
       return map[string]any{
           "name":             "demo",
           "version":          "1",
           "frame_resolution": "coalesce",
           "nodes": []map[string]any{
               {"type": "n", "executor": "e"},
           },
       }
   }
   ```

   Two callers exist for `minimalSpec()` in this file: (a) the `srv.State.RegisterTemplate(minimalSpec(), …)` direct-state seeding (lines elsewhere — `grep -n minimalSpec core/cli/internal/clitest/server_test.go`), and (b) the `cli.RegisterTemplateRequest{Spec: minimalSpec()}` HTTP-driven calls at lines 26–27, 52, 64, 110.

   Provide both. Add a second helper alongside `minimalSpec`:

   ```go
   func minimalSpecTyped() node.TemplateSpec {
       return node.TemplateSpec{
           Name:            "demo",
           Version:         "1",
           FrameResolution: "coalesce",
           Nodes: []node.TemplateNodeDef{
               {Type: "n", Executor: "e"},
           },
       }
   }
   ```

   And add `"github.com/fallguy/rimsky/core/node"` to the import block at the top of `server_test.go`.

3. In `core/cli/internal/clitest/server_test.go`, replace every `cli.RegisterTemplateRequest{Spec: minimalSpec(), …}` site with `cli.RegisterTemplateRequest{Spec: minimalSpecTyped(), …}`. Affected lines:
   - line 26–27 (the only `RegisterTemplateRequest` literal that spans two lines)
   - line 52
   - line 64
   - line 110

   Leave any direct `srv.State.RegisterTemplate(minimalSpec(), …)` call (state-seeding API, map-typed, unchanged) alone.

4. Open `core/cli/client_test.go`. Lines 60–63 currently are:

   ```go
   got, err := c.RegisterTemplate(context.Background(), RegisterTemplateRequest{
       Spec: map[string]any{"name": "x", "version": "1"},
       Tag:  "ingest@1.0",
   })
   ```

   Replace with:

   ```go
   got, err := c.RegisterTemplate(context.Background(), RegisterTemplateRequest{
       Spec: node.TemplateSpec{Name: "x", Version: "1"},
       Tag:  "ingest@1.0",
   })
   ```

   Add `"github.com/fallguy/rimsky/core/node"` to the import block at the top of `client_test.go` if it isn't already there.

5. Run a string sweep to catch any remaining map-typed RegisterTemplateRequest:

   ```sh
   cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky
   grep -rn "RegisterTemplateRequest{" core/
   ```

   Inspect each hit. Anything passing `Spec: <map literal>` or `Spec: <map-typed variable>` must be migrated to a typed value. Hits where `Spec:` is followed by `node.TemplateSpec{…}`, `minimalSpecTyped()`, or another typed-spec helper are fine.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/cli/...` — will fail until Tasks 8 and 9 also land (they fix the production callers in `core/cli/templates.go` and `core/cli/run.go` via the `readSpecFile` signature change). Re-run the build at the end of Task 9.
- The grep in step 5 returns no `Spec: map[string]any` matches in `core/cli/`.

---

## Task 8 — Make `cli.readSpecFile` return a typed spec

**Files:** `core/cli/templates.go`

**Steps:**

1. Open `core/cli/templates.go`.

2. Replace `readSpecFile` (currently at lines 62–85) with a typed implementation:

   ```go
   // readSpecFile reads <path> as YAML and decodes it into node.TemplateSpec.
   // The control-api accepts JSON-shaped bodies; YAML is the on-disk form.
   // yaml.v3 honors the json: tags' lowercase-snake-case keys via its
   // own yaml: tags (already declared on the spec types) and via
   // case-insensitive Go-field-name fallback.
   func readSpecFile(path string) (node.TemplateSpec, error) {
       raw, err := os.ReadFile(path)
       if err != nil {
           return node.TemplateSpec{}, err
       }
       var spec node.TemplateSpec
       if err := yaml.Unmarshal(raw, &spec); err != nil {
           return node.TemplateSpec{}, fmt.Errorf("parse %s: %w", path, err)
       }
       return spec, nil
   }
   ```

3. Delete `toJSONShape` (currently at lines 87–127) entirely — no callers remain after this task.

4. Update the import block to add `"github.com/fallguy/rimsky/core/node"`:

   ```go
   import (
       "context"
       "encoding/json"
       "errors"
       "flag"
       "fmt"
       "os"
       "sort"
       "strings"

       "gopkg.in/yaml.v3"

       "github.com/fallguy/rimsky/core/node"
   )
   ```

5. Update `RunTemplateRegister` (currently at lines 134–172). The relevant lines (153–159) currently read:

   ```go
       spec, err := readSpecFile(rest[0])
       if err != nil {
           fmt.Fprintln(os.Stderr, err)
           return 2
       }
       c := NewClient(endpoint)
       tpl, err := c.RegisterTemplate(ctx, RegisterTemplateRequest{Spec: spec, Tag: tag, Source: source})
   ```

   The `spec` variable's type changes from `map[string]any` to `node.TemplateSpec` automatically; the `RegisterTemplateRequest{Spec: spec, ...}` call now matches the new typed field. No code change needed beyond the readSpecFile signature change.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/cli/` — must succeed (Task 9 fixes the remaining caller in run.go in parallel; if you do this task first, build will still fail until Task 9 completes — that is fine, but run the verify command once both are done).

---

## Task 9 — Update `core/cli/run.go` to pass typed spec

**Files:** `core/cli/run.go`

**Steps:**

1. Open `core/cli/run.go`.

2. Lines 97–104 currently read:

   ```go
       spec, err := readSpecFile(rest[0])
       if err != nil {
           fmt.Fprintln(os.Stderr, err)
           return 2
       }
       c := NewClient(endpoint)

       tpl, err := c.RegisterTemplate(ctx, RegisterTemplateRequest{Spec: spec, Tag: tag})
   ```

   No code change needed — the type of `spec` flips automatically to `node.TemplateSpec`, and `RegisterTemplateRequest.Spec` now accepts that. This task is a verification placeholder to confirm the file compiles after Tasks 7–8.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/cli/` — must succeed.
- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go vet ./core/cli/` — must succeed.

---

## Task 10 — Make `compose.ResolveTemplate` return a typed spec

**Files:** `core/cli/compose/resolver.go`

**Steps:**

1. Open `core/cli/compose/resolver.go`.

2. Replace the entire file body (lines 1–103) with:

   ```go
   // resolver.go — read a template spec file from disk, apply
   // frame-resolution defaults, and compute its content hash via the
   // shared canonical hasher (matching the control-api's hash exactly).
   package compose

   import (
       "fmt"
       "os"

       "gopkg.in/yaml.v3"

       "github.com/fallguy/rimsky/core/canonical"
       "github.com/fallguy/rimsky/core/node"
   )

   // ResolveTemplate reads a template spec file from disk, runs
   // frame-resolution default-fill on the typed view, and returns:
   //
   //   - the canonical hash (computed from the typed TemplateSpec, matching
   //     the control-api exactly), and
   //   - the typed TemplateSpec to ship verbatim to POST /templates.
   //
   // After the 2026-05-02 json-tags cleanup the typed view marshals to
   // the same lowercase-snake-case JSON keys the control-api decodes
   // (`name`, `version`, `frame_resolution`, `nodes`, …), so no wire
   // shaping or YAML→generic-map round-trip is needed.
   func ResolveTemplate(path string) (hash string, spec node.TemplateSpec, err error) {
       raw, err := os.ReadFile(path)
       if err != nil {
           return "", node.TemplateSpec{}, err
       }
       if err := yaml.Unmarshal(raw, &spec); err != nil {
           return "", node.TemplateSpec{}, fmt.Errorf("parse %s: %w", path, err)
       }
       node.ApplyFrameResolutionDefaults(&spec)
       hash, err = canonical.CanonicalSpecHash(spec)
       if err != nil {
           return "", node.TemplateSpec{}, err
       }
       return hash, spec, nil
   }
   ```

   This deletes `yamlToJSON` and the second YAML→generic-map round-trip.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/cli/compose/` — will fail because `plan.go` still expects `(hash, map[string]any)`. Task 11 fixes that.

---

## Task 11 — Update `Step.SpecBody` to typed and adjust `ComputePlan`

**Files:** `core/cli/compose/plan.go`

**Steps:**

1. Open `core/cli/compose/plan.go`.

2. Update the `Step` struct's `SpecBody` field (currently at lines 84–86):

   ```go
       // SpecBody is the typed spec body for register steps. Not emitted
       // to the JSON output to keep plans concise.
       SpecBody *node.TemplateSpec `json:"-"`
   ```

   Use a pointer so the zero value stays distinguishable from a populated spec; `apply.go` only reads it when `Action == ActionRegister`.

3. Add the `node` import to the import block at the top of the file. Find the existing import block and add:

   ```go
       "github.com/fallguy/rimsky/core/node"
   ```

4. Update the `specBodies` map type and the resolver loop (currently at lines 156–165) to:

   ```go
       // Resolve template paths → hashes upfront.
       resolved := map[string]string{} // prefixedTag → newHash
       specBodies := map[string]node.TemplateSpec{}
       for _, t := range m.Templates {
           hash, spec, err := ResolveTemplate(t.Path)
           if err != nil {
               return nil, fmt.Errorf("resolve %s: %w", t.Path, err)
           }
           resolved[m.PrefixedTag(t.Tag)] = hash
           specBodies[hash] = spec
       }
   ```

5. Update the register-step construction (currently at lines 182–189) to attach a pointer to a local copy of the typed spec:

   ```go
           specCopy := specBodies[hash]
           registers = append(registers, Step{
               Action:       ActionRegister,
               Kind:         KindTemplate,
               TemplateHash: hash,
               FromPath:     t.Path,
               Source:       fmt.Sprintf("manifest:%s:%s", m.Project, t.Tag),
               SpecBody:     &specCopy,
           })
   ```

   The local `specCopy` is necessary because taking the address of a map index expression (`&specBodies[hash]`) is not legal in Go — map values are not addressable. The copy is a per-loop-iteration value, so each `Step` gets its own pointer into a stable backing store.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/cli/compose/` — will fail until Task 12 lands; that is expected at this point.

---

## Task 12 — Drop the `hashRewrite` defense in `apply.go` and pass typed Spec to RegisterTemplate

**Files:** `core/cli/compose/apply.go`

**Steps:**

1. Open `core/cli/compose/apply.go`.

2. Replace `ApplyPlan` (currently at lines 31–65) with:

   ```go
   // ApplyPlan executes plan.Steps serially against c. Returns immediately
   // on the first step error, wrapping the failed step.
   //
   // Pre-2026-05-02 the CLI computed plan-time hashes against the typed
   // TemplateSpec while the control-api stored hashes computed against the
   // shadow-tree-decoded view; those two could diverge when capital-N
   // capital keys leaked into one side's JSON marshal but not the other.
   // The 2026-05-02 json-tags cleanup unified them — both sides now hash
   // the same lowercase-snake-case bytes — so ApplyPlan no longer needs
   // the hash-rewrite defense it carried during the rimsky-cli rollout.
   func ApplyPlan(ctx context.Context, c *cli.Client, plan *Plan, opts ApplyOpts) error {
       w := opts.Logger
       if w == nil {
           w = os.Stdout
       }
       for _, step := range plan.Steps {
           if _, err := applyStep(ctx, c, step, w); err != nil {
               return fmt.Errorf("step %s %s: %w", step.Action, stepTarget(step), err)
           }
       }
       return nil
   }
   ```

3. Update only the body of the `case ActionRegister:` branch inside `applyStep`. The rest of `applyStep` (lines 95–170, all other `case` branches and the closing braces) is unchanged.

   The current `case ActionRegister:` branch is at lines 86–94:

   ```go
       case ActionRegister:
           body := cli.RegisterTemplateRequest{Spec: step.SpecBody, Source: step.Source}
           resp, err := c.RegisterTemplate(ctx, body)
           if err != nil {
               return "", err
           }
           serverHash := resp.Hash()
           logf("register", cli.TruncHash(serverHash), "ok")
           return serverHash, nil
   ```

   Replace those nine lines (86–94) with the eleven-line block below (the new `if step.SpecBody == nil` guard adds two lines):

   ```go
       case ActionRegister:
           if step.SpecBody == nil {
               return "", fmt.Errorf("register step missing spec body")
           }
           body := cli.RegisterTemplateRequest{Spec: *step.SpecBody, Source: step.Source}
           resp, err := c.RegisterTemplate(ctx, body)
           if err != nil {
               return "", err
           }
           logf("register", cli.TruncHash(resp.Hash()), "ok")
           return "", nil
   ```

   The branch now returns `""` for the first return value (the rewrite map is gone, so the caller no longer reads it) — matching every other branch's `return "", nil` shape.

   Also update the function-level doc comment (lines 78–80) from:

   ```go
   // applyStep returns the server-stored hash for ActionRegister (so the
   // caller can rewrite divergent plan-time hashes); empty string for
   // other actions.
   ```

   to:

   ```go
   // applyStep executes one plan step against the control-api and logs the
   // outcome. The first return value is unused (kept for signature
   // stability with the previous hash-rewrite mode); the second return is
   // the control-api error, if any.
   ```

   Keep the function signature `(string, error)` as-is — simplifying it to `error` is out of scope. The unused first return is a one-line `return "", …` everywhere.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/cli/compose/` — must succeed.
- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go vet ./core/cli/compose/` — must succeed.

---

## Task 13 — Update `compose/resolver_test.go` for the typed return

**Files:** `core/cli/compose/resolver_test.go`

**Steps:**

1. Open `core/cli/compose/resolver_test.go`.

2. The current test (lines 22–54) destructures the old tuple `(hash, specMap, err)` and asserts `specMap["nodes"]` is present. Replace with the typed equivalent:

   ```go
   func TestResolveTemplate_HashMatchesCanonical(t *testing.T) {
       dir := t.TempDir()
       path := filepath.Join(dir, "spec.yml")
       if err := os.WriteFile(path, []byte(exampleSpec), 0o644); err != nil {
           t.Fatal(err)
       }
       gotHash, gotSpec, err := ResolveTemplate(path)
       if err != nil {
           t.Fatal(err)
       }
       if gotHash == "" {
           t.Error("hash empty")
       }
       if len(gotSpec.Nodes) == 0 {
           t.Errorf("spec missing nodes: %+v", gotSpec)
       }
       // Cross-check against direct canonical hash.
       var domainSpec node.TemplateSpec
       if err := yaml.Unmarshal([]byte(exampleSpec), &domainSpec); err != nil {
           t.Fatal(err)
       }
       node.ApplyFrameResolutionDefaults(&domainSpec)
       wantHash, err := canonical.CanonicalSpecHash(domainSpec)
       if err != nil {
           t.Fatal(err)
       }
       if gotHash != wantHash {
           t.Errorf("hash drift: got %q want %q", gotHash, wantHash)
       }
   }
   ```

   The unused `_ = spec` line and the redundant `var spec node.TemplateSpec` from the original test are dropped.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/cli/compose/ -run TestResolveTemplate_HashMatchesCanonical -count=1 -v` — must pass.

---

## Task 13a — Update `compose/plan_test.go` ResolveTemplate destructuring

**Files:** `core/cli/compose/plan_test.go`

**Steps:**

1. After Task 10's signature change, every site that destructures `(hash, body, err) := compose.ResolveTemplate(path)` produces `body` as `node.TemplateSpec` (typed) instead of `map[string]any`. Four sites in `plan_test.go` then pass `body` into the fake server's seeding API `srv.State.RegisterTemplate(body, …)`, which still accepts `map[string]any` (per Task 7a's note: the fake's storage API stays map-typed). The mismatch breaks compilation.

2. The cleanest local fix is to convert the typed spec to a map at each call site via a single round-trip helper. Add this helper near the top of `plan_test.go` (e.g. just below the `package compose_test` import block):

   ```go
   // specToMap round-trips a typed spec through json into the map shape
   // the fake server's storage API accepts. The fake's storage layer is
   // map-typed by design (it stores opaque specs); only the wire body
   // type is typed, so tests that pre-seed state via srv.State.RegisterTemplate
   // need this conversion when they reuse the spec returned by
   // compose.ResolveTemplate.
   func specToMap(t *testing.T, spec node.TemplateSpec) map[string]any {
       t.Helper()
       raw, err := json.Marshal(spec)
       if err != nil {
           t.Fatal(err)
       }
       var m map[string]any
       if err := json.Unmarshal(raw, &m); err != nil {
           t.Fatal(err)
       }
       return m
   }
   ```

   Add `"encoding/json"` and `"github.com/fallguy/rimsky/core/node"` to the import block of `plan_test.go` if not already present.

3. Update each of the four affected sites. Replace `gotHash, _ := srv.State.RegisterTemplate(body, "compose:p:a@1.0", "")` (lines 175, 254, 300, 377) with `gotHash, _ := srv.State.RegisterTemplate(specToMap(t, body), "compose:p:a@1.0", "")`. The `t` argument requires the surrounding test function's `t *testing.T` to be in scope — it always is at these sites (they live inside `func TestX(t *testing.T)` bodies).

4. Run a sanity sweep:

   ```sh
   cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky
   grep -n "ResolveTemplate" core/cli/compose/plan_test.go
   ```

   For every match where the result is destructured into a `body` (or similarly-named) variable, follow that variable through the test function and confirm any subsequent `srv.State.RegisterTemplate(...)` or other map-typed API call wraps it in `specToMap(t, body)`.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./core/cli/compose/...` — must succeed.
- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/cli/compose/ -count=1` — must pass (this exercises the four updated sites).

---

## Task 14 — Run the full CLI / compose / clitest suite

**Files:** none modified; verification gate.

**Steps:**

1. Run `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/cli/... -count=1`.

2. The clitest fake control-api stores templates as `map[string]any` (`storedTemplate.Spec map[string]any`). Its `hashSpec` helper round-trips a map through `node.TemplateSpec`'s typed unmarshal + `canonical.CanonicalSpecHash`. With the new tags, `json.Unmarshal(raw, &ts)` for a lowercase-keyed map still works (tags map the lowercase keys to fields). The hash output bytes change vs pre-fix, but the CLI side now also produces the new bytes through `compose.ResolveTemplate`, so both sides shift together. Tests that compare CLI-computed hash against fake-server-computed hash continue to pass.

3. If a compose plan/apply/dev/down test fails (after Tasks 7a, 13, 13a have already covered the known compile breakages):
   - Synthetic `Step{}` literals with `SpecBody: someMap` need `SpecBody: &node.TemplateSpec{...}` or `&specCopy` (where `specCopy := someTypedSpec`). Search: `grep -rn "SpecBody:" core/cli/compose/`.
   - Any remaining `cli.RegisterTemplateRequest{Spec: <map>}` site missed by Task 7a's sweep needs the typed Spec. Search: `grep -rn "RegisterTemplateRequest{" core/`.
   - Any test that destructures `compose.ResolveTemplate(...)` into a `body` variable and then passes it into a map-typed API needs the `specToMap(t, body)` conversion (the helper added in Task 13a). Search: `grep -rn "ResolveTemplate" core/cli/compose/`.

**Verification:**

- `go test ./core/cli/... -count=1` exits 0.

---

## Task 15 — Run the canonical hash unit tests

**Files:** none modified; verification gate.

**Steps:**

1. Run `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/canonical/... -count=1`.

2. The tests in `core/canonical/jcs_test.go` are all structural / determinism / library-level — none assert a specific 64-hex hash. They must continue to pass.

3. If a test fails: investigate. The likely cause is an `omitempty` tag mismatch between the YAML default-fill path and the JSON marshal path that breaks `TestCanonicalSpecHash_Deterministic`. Trace through the spec construction + tag list for the offending field and correct.

**Verification:**

- `go test ./core/canonical/... -count=1` exits 0.

---

## Task 16 — Run the scenario suite (testcontainers Postgres)

**Files:** none modified; verification gate.

**Steps:**

1. Confirm Docker is running (`docker ps` returns without error). The scenario suite spins up `postgres:15` containers via testcontainers-go; Docker is required.

2. Run `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./test/scenarios/... -count=1`.

3. None of the scenario tests assert on hard-coded hash literals (verified: `grep -rn 'sha256-[0-9a-f]\{60,\}' test/scenarios/` returns no matches). They register templates and read back their hashes within the same run, so the new hash bytes flow through transparently.

4. If a scenario test fails: investigate the failure on its own merits — it is not expected to be hash-related. If it is hash-related, the most likely cause is a test that bypassed `node.TemplateSpec` and built a `map[string]any` body with capital-N keys; fix the test fixture to use lowercase keys.

**Verification:**

- `go test ./test/scenarios/... -count=1` exits 0.

---

## Task 17 — Run the storage and queue suites

**Files:** none modified; verification gate.

**Steps:**

1. Run the suites that touch templates / hashes:
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/storage/... -count=1`
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/queue/... -count=1`
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/scheduler/... -count=1`
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/supervisor/... -count=1`

2. None of these are expected to assert on hash values. They are run as a regression gate to catch any indirect breakage from the typed-spec change in upstream packages.

3. If any test fails, investigate and fix.

**Verification:**

- All four commands exit 0.

---

## Task 18 — Race-sensitive paths regression check

**Files:** none modified; verification gate.

**Steps:**

1. Run `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./core/queue/... ./core/supervisor/... ./core/scheduler/... -race -count=3`.

2. Required by `.claude/rules/rules.md` ("After Code Changes — Required Final Step"). No new races are expected from this change (the change is type-shape-only, no concurrency primitives touched), but the gate is mandatory.

**Verification:**

- The command exits 0.

---

## Task 19 — Full module build, vet, and lint

**Files:** none modified; verification gate.

**Steps:**

1. Run all of:
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./...`
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go vet ./...`
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && make lint`

2. `make lint` runs golangci-lint (`gofmt`, `goimports`, `govet`, `staticcheck`, `unused`, `ineffassign`, `errcheck`, `revive`). The `unused` and `goimports` checks in particular will flag any dead helper or stale import left behind from Tasks 5, 8, 10, 12.

3. Fix any issues. Do not skip-list any new findings.

**Verification:**

- All three commands exit 0.

---

## Task 20 — Full `go test ./...`

**Files:** none modified; verification gate.

**Steps:**

1. Run `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./... -count=1`.

2. This is the final gate. Required by `.claude/rules/rules.md`.

3. Fix any remaining failures.

**Verification:**

- The command exits 0.

---

## Task 21 — Update `CHANGELOG.md`

**Files:** `CHANGELOG.md`

**Steps:**

1. Open `CHANGELOG.md`. The file already has an `## Unreleased` section at line 3.

2. Append a new bullet under `## Unreleased`, after the existing `rimsky-cli and rimsky-compose.yml` bullet block:

   ```markdown
   - **Template-spec JSON tags.** Add `json:` struct tags to every wire-relevant
     field of `core/node/template.go`, `core/node/policy.go`, and
     `core/qualityrule/spec.go`, then delete the JSON shadow-type tree and
     `toTemplateSpec` mapper from `core/controlapi/templates.go`, the
     `toJSONShape` helper from `core/cli/templates.go`, the `yamlToJSON` helper
     and YAML→generic-map round-trip from `core/cli/compose/resolver.go`, and the
     `hashRewrite` defense from `core/cli/compose/apply.go::ApplyPlan` (which
     existed only to absorb the JSON-tag asymmetry that this change fixes).

     **Hash-bytes change.** `canonical.CanonicalSpecHash` now marshals
     `TemplateSpec` with lowercase-snake-case JSON keys (`name`, `nodes`,
     `params_schema`, …) instead of the old capital-cased Go-field-name keys
     (`Name`, `Nodes`, …) that came from the missing tags. Every existing
     template's content hash changes. There are no production templates;
     dev-DB users must drop and recreate the postgres volume:

     ```
     docker compose -f deploy/docker-compose.yml down -v
     docker compose -f deploy/docker-compose.yml up -d
     ```

     Per `docs/history/2026-05-02-template-spec-json-tags-design.md`.
   ```

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && grep -A1 "Template-spec JSON tags" CHANGELOG.md` returns the new bullet.

---

## Task 22 — Update `CLAUDE.md`

**Files:** `CLAUDE.md`

**Steps:**

1. Open `CLAUDE.md`. The "Templates are content-addressed" gotcha is at line 123.

2. Replace that single bullet with:

   ```markdown
   - **Templates are content-addressed.** `rimsky_templates.id` is `sha256-<64-hex>` over an RFC 8785 JCS-canonicalized spec (`core/canonical/CanonicalSpecHash`). Tags in `rimsky_template_tags` are movable aliases. Re-registering the same spec is a cheap no-op. Tag movement does not migrate live instances — instances bind to the resolved hash at creation. **Hash bytes are not pinned across pre-v1 changes**: the 2026-05-02 json-tags cleanup (`docs/history/2026-05-02-template-spec-json-tags-design.md`) changed the canonical bytes from capital-cased Go-field keys to lowercase-snake-case keys; under pre-v1 rules this required a dev-DB nuke (no production data to preserve). Future hash-bytes changes follow the same pre-v1 break-freely rule until v1 ships.
   ```

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && grep -n "Hash bytes are not pinned" CLAUDE.md` returns one match.

---

## Task 23 — Move the design doc to `docs/history/`

**Files:** the design doc lands at `docs/history/2026-05-02-template-spec-json-tags-design.md` (the standard "implementation execution log" location per `.claude/rules/rules.md`).

**Steps:**

1. Confirm the target directory exists: `ls /Users/patrick/Documents/projects/research/verantel/submodules/rimsky/docs/history/` should succeed.

2. Use `git mv` to relocate the design doc into `docs/history/` (preserves history — required because the design doc was committed in a prior turn). If the file is uncommitted (only present in the working tree), `git mv` still works against the index; if `git mv` complains about an untracked source, fall back to `mv` plus `git add` of the new path.

**Verification:**

- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && ls docs/history/2026-05-02-template-spec-json-tags-design.md` succeeds.
- `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && git ls-files docs/history/2026-05-02-template-spec-json-tags-design.md | grep -q .` succeeds (the design doc is tracked at its final location).

---

## Task 24 — Final cross-package smoke

**Files:** none modified; final verification gate.

**Steps:**

1. Re-run the full module sweep one more time:
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go build ./...`
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && go test ./... -count=1`
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && make lint`

2. If any of the three fails, fix and re-run.

3. Run a final string-level sanity sweep to confirm the workarounds are gone:
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && grep -rn "templateNodeDefJSON\|nodeStoreRefJSON\|nodeLockRefJSON\|inheritEntryJSON\|nodeAttributesDefJSON\|qualityRuleJSON\|errorTypePolicyJSON\|policyActionJSON\|toTemplateSpec\|yamlToJSON\|toJSONShape\|hashRewrite" core/`

   Must return zero matches. Any hits in `docs/` are expected (history mentions).

4. Confirm no remaining `Spec: map` literals in `cli.RegisterTemplateRequest`:
   - `cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky && grep -rn "RegisterTemplateRequest{" core/ | grep "Spec:"` — every hit must show `Spec: node.TemplateSpec{…}` or `Spec: <typed-helper-call>()`. No `Spec: map[string]any{...}` and no `Spec: someMap`.

**Verification:**

- `go build ./...`, `go test ./... -count=1`, and `make lint` all exit 0.
- The grep returns zero matches under `core/`.

---

## Manual checks after completion

None. All verification is expressible as automated tests and lint runs above. The dev-DB nuke described in `CHANGELOG.md` is an instruction for downstream operators, not a step the implementer must perform — the testcontainers-driven scenario suite already runs against ephemeral Postgres instances created within each test run.
