# Stores Redesign v2 — Implementation Plan

**Spec:** `docs/specs/2026-04-27-stores-redesign-v2-design.md`
**Glossary:** `docs/glossary.md`

**Working directory:** `/Users/patrick/Documents/projects/research/verantel/submodules/rimsky`. All paths in this plan are relative to that directory unless absolute. Git submodule.

**Goal:** Implement the protocol and lock model refinement described in the spec — verb-set rewrite (5 verbs), pick policies substrate-side, held claims via inheritance + auto-terminal, capability struct collapse to one field, schema migration, and supporting code/test/doc changes.

**Architecture:** The rimsky platform has three architectural collections (orchestrator, stores, executors). This work touches the orchestrator's interaction with stores: `core/store/` (rewritten interface + types + registry + schema helpers), in-process store implementations (`filesystem`, postgres-rename, `stub`), `core/attributes/` (substitution paths), `core/supervisor/` (atomic acquisition + auto-terminal), `core/scheduler/`, `core/queue/`, `core/node/` (template parser), `core/migrations/`, plus operator config schema, scenario tests, and docs.

**Tech Stack:** Go 1.21+ (root module `github.com/fallguyconsulting/rimsky`; `go.mod` is at the repo root, NOT under `core/`), pgx/v5, postgres 15, testcontainers-go (real postgres for scenario tests), stdlib `log/slog` (no Zap/Zerolog — see lint rules), go-chi/chi, robfig/cron/v3, JSON Schema (santhosh-tekuri/jsonschema/v5).

**Build commands** (referenced throughout):
- `go build ./...` — full-tree build
- `go test ./... -count=1` — full-tree tests (testcontainers tests pull `postgres:15`; Docker socket required)
- `go test ./... -race -count=1` — race detector
- `make lint` — golangci-lint (gofmt, goimports, govet, staticcheck, unused, ineffassign, errcheck, revive)
- `make tidy` — `go mod tidy`
- `make proto-gen` — regenerate proto bindings (only if `proto/v1/*.proto` changed)

**Pre-v1:** No production data. Migrations are rewritten in place; dev DBs are nuked on adoption per `.claude/rules/rules.md`. Don't add backwards-compatibility shims.

**Convention used in this plan:**
- "Spec §X.Y" refers to a section in `docs/specs/2026-04-27-stores-redesign-v2-design.md`. Read the spec at T0; refer to it whenever a "spec §..." reference appears.
- "Glossary: X" refers to a term defined in `docs/glossary.md`.
- "Blessed invariant N" refers to spec §21 (and `CLAUDE.md`'s blessed-invariants list after T44).

---

## Pre-flight

### T0: Establish baseline

**Files:** none (read-only).

**Steps:**

1. Read the spec: `docs/specs/2026-04-27-stores-redesign-v2-design.md`. End-to-end. The plan refers to its sections by number throughout.
2. Read the glossary: `docs/glossary.md`. Vocabulary from this is authoritative.
3. Read repo rules: `CLAUDE.md`, `.claude/rules/rules.md`, `.claude/rules/cold-read-cheatsheet.md`.
4. Read the package docstring and current interface: `core/store/doc.go`, `core/store/interface.go`, `core/store/types.go`, `core/store/lockholders.go`, `core/store/registry.go`.
5. Read the current store implementations to understand what's being rewritten: `core/store/filesystem/`, `core/store/claimstorepg/`, `core/store/stub/`.
6. Read the current supervisor acquisition flow: `core/supervisor/runner.go`.
7. Read the current schema: `core/migrations/001-initial.sql`.
8. Read at least one existing scenario test to understand the testcontainers setup pattern: `test/scenarios/lock_atomic_acquisition_test.go` (or any other under `test/scenarios/`). New scenario tests in T31–T42 follow the same shape.
9. Read the existing template parser to find where templates are parsed and persisted: `grep -rln 'rimsky_templates\|TemplateStore\|graph_data' core/` — this locates the template-store package referenced by T4 step 5 and T17.
10. Read the existing config loader to find the operator-config struct location: `grep -rln 'RIMSKY_STORES_CONFIG\|StoresConfig' core/cmd/ core/config/` — this locates the file T22 modifies.

**Verification:**
```sh
go build ./...
go test ./... -count=1 || true        # capture baseline; pre-existing failures OK
make lint || true                     # capture baseline
```

Note any pre-existing failures so they aren't attributed to this work later. Working tree may be dirty (gitStatus shows in-flight doc changes); don't act on those — proceed with the implementation regardless.

---

## Foundation: types, interface, schema

These must land together — the rest of the codebase depends on them.

### T1: Rewrite `core/store/types.go` per spec §11.3 / §11.4

**Files:** `core/store/types.go`

**Steps:**

1. Replace the file's contents. Drop everything (`LockHandle`, `ClaimResult` (old shape), `ReleaseAction`, `NativeHandle` sealed interface, `FilesystemDirectHandle`, `ClaimStoreHandle`, `LockSpec` interface, `RegionLockSpec`, `ClaimLockSpec`, `NamedLockSpec` (old shape), `LockMode` const block).
2. Define new types per spec §11.3 / §11.4:
   - `Intent` string type with constants `IntentRead = "r"`, `IntentReadWrite = "rw"`.
   - `ClaimSpec { StoreName, Selector, Intent, Alias }` — no `PolicyOverride` field (per spec §11.3 docstring).
   - `NamedLockSpec { Name }` — no `Mode`, no `Limit` (operator-configured per spec §15.2).
   - `ClaimResult { Address, Payload, Region }` — all three `json.RawMessage`.
   - `WriteSemantics` string type with constants `WriteSemanticsDirect = "direct"`, `WriteSemanticsStagedBlocking = "staged_blocking"`, `WriteSemanticsStagedAsync = "staged_async"`.
   - `Capabilities { WriteSemantics WriteSemantics }`.
3. Add file-level docstring referencing spec §5.1 (two primitives) and `docs/glossary.md`.
4. Annotate `ClaimResult` with the inertness invariant comment per spec §17.2 / blessed invariant 20:
   ```go
   // @blessed-invariant 20: claim content is inert in Rimsky.
   //
   //   Address, Payload, Region are substrate-supplied opaque bytes.
   //   Rimsky reads them by named-field path only at substitution-leaf
   //   extraction (core/attributes/substitution.go::walkPath); does not
   //   log, validate, transform, normalize, decrypt, hash, index,
   //   pattern-match, attach to traces, include in errors, or otherwise
   //   act on the content.
   ```

**Verification:**
```sh
cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky
go build ./core/store/...
```
Compilation will fail at this point (the rest of `core/store/` and dependents reference removed types). That's expected and fixed in subsequent tasks. Confirm the failures are exactly in the consumers — no failures inside `core/store/types.go` itself.

### T2: Rewrite `core/store/interface.go` per spec §11.5

**Files:** `core/store/interface.go`

**Steps:**

1. Replace the file's contents.
2. Define the new `Store` interface with five verbs per spec §11.5:
   ```go
   type Store interface {
       Kind() string
       Name() string
       Capabilities() Capabilities
       RegionsConflict(a, b []byte) bool
       UnmarshalRegion(raw []byte) ([]byte, error)
       Open(ctx context.Context, spec ClaimSpec) (ClaimResult, error)
       Commit(ctx context.Context, region []byte, address []byte, policyOverride string) error
       Abandon(ctx context.Context, region []byte, address []byte, policyOverride string) error
       Delete(ctx context.Context, region []byte) error
       Release(ctx context.Context, region []byte, address []byte) error
   }
   ```
3. Drop the old `ClaimableStore` and `ResumableStore` sub-interfaces entirely.
4. Annotate the `Store` interface with the two restated blessed invariants from spec §11.5 docstring (invariant 9a — lock state lives only in postgres; invariant 9b — store implementations do not internally serialize on lock-shaped predicates).

**Verification:**
```sh
go build ./core/store/...
```
Same expected failure pattern — consumers in `filesystem`, `claimstorepg`, `stub` reference old method names. Confirm `interface.go` itself compiles.

### T3: Rewrite `core/store/registry.go` to expose `Factory.MaxWriteSemantics`

**Files:** `core/store/registry.go`

**Steps:**

1. Update the `Factory` interface to add `MaxWriteSemantics() WriteSemantics`.
2. In `Registry.BuildAll`, after the factory's `Build` returns the `Store`, validate: read the operator's `write_semantics` config (passed in `cfg`), compare against `factory.MaxWriteSemantics()`. If operator's value exceeds the factory's max, return a clear error: `"store %q: configured write_semantics %q exceeds substrate max %q"`.
3. Define the comparison helper:
   ```go
   func writeSemanticsRank(ws WriteSemantics) int {
       switch ws {
       case WriteSemanticsDirect:         return 0
       case WriteSemanticsStagedBlocking: return 1
       case WriteSemanticsStagedAsync:    return 2
       }
       return -1
   }
   ```

**Verification:**
```sh
go build ./core/store/
```
This file should compile (factories haven't been updated yet, so `Registry` may have build-time issues if it instantiates anything; verify `registry.go` itself parses cleanly via `go vet ./core/store/`).

### T4: Rewrite migration `core/migrations/001-initial.sql` per spec §12

**Files:** `core/migrations/001-initial.sql`

**Steps:**

1. Pre-v1 break-freely: rewrite the migration in place. (Per `.claude/rules/rules.md`, dev DB will be nuked.)
2. For tables preserved verbatim (`rimsky_migrations`, `rimsky_templates`, `rimsky_instances`, `rimsky_supervisors`, `rimsky_dispatch`, `rimsky_schedules`, `rimsky_events`, `rimsky_node_attributes`, `rimsky_nodes`, `rimsky_frames`): leave their DDL unchanged.
3. Replace `rimsky_lock_holders` DDL with the schema in spec §12.10. Specifically:
   - `lock_kind` enum reduced to `('named', 'region')`.
   - Drop `claim_id` column.
   - Add `address JSONB` column.
   - Add `intent TEXT` column with check `intent IN ('r', 'rw')` for region rows; NULL for named rows.
   - CHECK constraint per spec.
   - Indexes per spec: `idx_rimsky_lock_holders_supervisor`, `_node`, `_named` (partial WHERE lock_kind='named'), `_region` (partial WHERE lock_kind='region'), `_expires` (partial WHERE expires_at IS NOT NULL).
4. Replace `rimsky_claim_holders` DDL with the schema in spec §12.11. Specifically:
   - Add `lock_holder_id UUID NOT NULL REFERENCES rimsky_lock_holders(id) ON DELETE CASCADE`.
   - Drop `claim_id` column.
   - Drop `actual_action` column.
   - Drop `delete_won` semantics (no separate column to drop if it was inline).
   - Drop `on_commit` and `on_give_up` columns from this table (resolution declarations live in template metadata per spec §12.11 changes-list).
   - `state` enum: `('active', 'completed', 'failed')`.
   - `UNIQUE (lock_holder_id, holder_node_id)`.
   - Indexes per spec: `idx_rimsky_claim_holders_lock_holder`, `_node`, `_active_subgraph` (partial WHERE state='active').
5. **`claim_resolutions` storage:** lives in the template's persisted graph data, not in a dedicated column. Per spec §14.3 / §18.1, `claim_resolutions:` is declared on the acquiring node in the template; the parsed template is persisted in `rimsky_templates.graph_data` (existing JSONB column). The supervisor reads the template at runtime via the dispatch row's `template_id` to look up `claim_resolutions[alias]` for the firing node's claim. **No schema change to `rimsky_templates` is needed** — the template's existing graph-data column already carries the parsed YAML/JSON.

   Add a helper method on the **template-store package** — locate via T0 step 9 (`grep -rln 'rimsky_templates' core/`); typical location is `core/storage/postgres/templates.go` or `core/templates/store.go`. The exact file/package depends on the as-shipped layout; the implementer follows the grep. Helper signature:
   ```go
   func (s *TemplateStore) GetClaimResolution(ctx context.Context, templateID shared.UUID, nodeName, alias string) (ClaimResolution, error)
   ```
   That reads the template's `graph_data`, walks to the node's `claim_resolutions[alias]`, returns `{OnCommit, OnGiveUp string}`. Used by the supervisor's auto-terminal logic (T17) and non-held-claim release path (T19).

   `ClaimResolution` is a small struct; define it where it makes sense (likely `core/node/types.go` or whatever package owns the parsed template's Go shape).

**Verification:**
```sh
# Spin up a clean postgres via testcontainers and apply the migration
go test ./core/migrations/... -count=1 -run TestApply
```
If no such test exists, the migration is exercised by the scenario tests at T31–T42 (each spins a testcontainer postgres and applies migrations). For T4's verification, just confirm the SQL parses without error — `psql -f core/migrations/001-initial.sql <conn>` against a clean postgres, or run any single scenario test as a smoke check (any failure points to schema malformation).

### T5: Adapt `core/store/lockholders.go` to new schema

**Files:** `core/store/lockholders.go`

**Steps:**

1. Update `LockHolderKind` constant block:
   - Drop `LockHolderKindClaim`.
   - Keep `LockHolderKindNamed` and `LockHolderKindRegion`.
2. Update `LockHolderRow` struct:
   - Drop `ClaimID *string` field.
   - Add `Address []byte` (or `json.RawMessage`) field.
   - Add `Intent *string` field (nullable; populated for region rows).
3. Update the `lockHolderCols` SQL constant to reflect the new column set (drop `claim_id`; add `address`, `intent`).
4. Update `Insert` to include `address` and `intent` parameters.
5. Add a new helper method `UpdateAddress(ctx, tx, id, supervisorID, address)` for the §13.3 step-4e address-update path.
6. Update `scanLockHolder` to scan the new columns.
7. Update `RebindForResume` to include `address` and `intent` in the SELECT/RETURNING column list.
8. Update `ListExpired`, `ListByNodeAndStore`, `ListByHolderNode`, `ListBySupervisor`, `ListByStoreRegion`, `Get` — all callers of `lockHolderCols` — to scan the new shape.
9. Drop any helpers that referenced `claim_id` directly.
10. Add or update `ListByLockHolderID` (or equivalent) for the auto-terminal flow's claim-holder sibling lookup.

**Verification:**
```sh
go build ./core/store/
go vet ./core/store/
```

---

## Store implementations (in-process)

### T6: Rewrite `core/store/stub/` to new verb set

**Files:** `core/store/stub/store.go`, `core/store/stub/factory.go`, `core/store/stub/store_test.go`

**Steps:**

1. Remove old methods (`AcquireLock`, `OpenHandle`, `Commit` (old shape), `ReleaseLock`).
2. Implement the five new verbs:
   - `Open(ctx, spec)` returns a stub `ClaimResult` with deterministic Address/Payload/Region (e.g., concatenations of spec fields as JSON).
   - `Commit(ctx, region, address, policyOverride)` — record the call (for assertions in tests); return nil unless a configurable error injector is set.
   - `Abandon`, `Delete`, `Release` — same shape (recorder + optional injector).
3. `Capabilities()` returns `{WriteSemantics: WriteSemanticsDirect}` by default; allow override for tests.
4. `Factory.MaxWriteSemantics()` returns `WriteSemanticsStagedAsync` (stub is permissive for testing).
5. Update `RegionsConflict` and `UnmarshalRegion` to operate on `[]byte` per the new interface.
6. Update `core/store/stub/store_test.go` to exercise the new verbs (call each, assert recorder state).

**Verification:**
```sh
go build ./core/store/stub/
go test ./core/store/stub/ -count=1
```

### T7: Adapt `core/store/filesystem/` to new verb set

**Files:** `core/store/filesystem/store.go`, `core/store/filesystem/factory.go`, `core/store/filesystem/region.go`, `core/store/filesystem/region_test.go`, `core/store/filesystem/store_test.go`

**Steps:**

1. Drop old methods and old `FilesystemDirectHandle` type (deprecated by spec §11.4).
2. Implement the five verbs for direct mode (the only mode v1 filesystem supports):
   - `Open(ctx, spec)` — resolve the selector globs to a concrete directory path; return `ClaimResult{Address: <path-as-json-bytes>, Payload: nil, Region: <selector-as-json-bytes>}`.
   - `Commit(ctx, region, address, policyOverride)` — substrate no-op (writes already on disk per spec §6.2 / §8.4); return nil. Discard `policyOverride`.
   - `Abandon(ctx, region, address, policyOverride)` — degenerate per spec §6.2; log a warning and return nil. Spec §22 documents this honestly.
   - `Delete(ctx, region)` — `os.RemoveAll` on the resolved region path. Return any I/O error.
   - `Release(ctx, region, address)` — direct mode never registers state at Open; return nil.
3. `Capabilities()` returns `{WriteSemantics: WriteSemanticsDirect}`.
4. `Factory.MaxWriteSemantics()` returns `WriteSemanticsDirect`. (Per spec §22 the filesystem store stays direct in v1; staged_blocking via atomic-rename is a stretch goal not committed to.)
5. Update `RegionsConflict` to handle `[]byte` glob-pattern format.
6. Update `region.go` purity: ensure `RegionsConflict` and `UnmarshalRegion` are pure (per blessed invariant 14 — no I/O, no external state).
7. Update tests:
   - `region_test.go` — verify the new byte-shape `RegionsConflict` and `UnmarshalRegion` are pure and correct.
   - `store_test.go` — exercise each of the five verbs; assert `Open` returns a path; assert `Commit`/`Abandon` are no-ops; assert `Delete` removes the region.

**Verification:**
```sh
go build ./core/store/filesystem/
go test ./core/store/filesystem/ -count=1 -race
```

### T8: Rename `core/store/claimstorepg/` → `core/store/postgres/` per spec J1 / §11.1

**Files:**
- Move: `core/store/claimstorepg/` → `core/store/postgres/` (preserve history via `git mv`)
  - Files within: `store.go`, `factory.go`, `acquire.go`, `release.go`, `holders.go`, `insert.go`, `store_test.go`, `factory_test.go`, `holders_test.go`

**Steps:**

1. Use `git mv` to preserve history:
   ```sh
   cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky
   git mv core/store/claimstorepg core/store/postgres
   ```
   Update package declarations in each `.go` file: `package claimstorepg` → `package postgres`.
2. Update the kind name: `Kind() string` returns `"postgres"` (was `"claim_store"`).
3. Restructure the store to support optional `pick_policies` config block per spec §15.1:
   - Parse `pick_policies` map from the `cfg` passed to `Factory.Build`.
   - Each entry is keyed by selector form (e.g., `"@review-queue"`); value is a substrate-specific config struct with `type` (queue|ring|...), `items_table`, `on_commit_default`, `on_give_up_default`, `visibility_timeout_seconds`.
4. Implement the five verbs:
   - `Open(ctx, spec)`:
     - If `spec.Selector` matches a configured pick-policy key: invoke pick (FOR UPDATE SKIP LOCKED on the items table per spec §12.12); flip row to `'in_progress'`; return `ClaimResult{Address: <substrate-pointer>, Payload: <item.payload>, Region: <item_id-as-json>}`.
     - Else (regional access): resolve the selector to a substrate region; return `ClaimResult{Address: <substrate-pointer>, Region: <selector-bytes>}`. Direct mode for v1; no staging area created.
     - Use `store.TxFromContext(ctx)` to get the supervisor's open `pgx.Tx`; substrate writes participate in the same transaction (invariant 10/15).
   - `Commit(ctx, region, address, policyOverride)`:
     - For pick-policy claims: apply the action specified by `policyOverride` (default to the policy's `on_commit_default`):
       - `delete`: `DELETE FROM <items_table> WHERE item_id = $1`.
       - `release_to_back`: update row to `'available'`, reset claim_token/claimed_at, advance `sequence`.
       - `release_to_head`: update row to `'available'`, reset claim_token/claimed_at, set priority to push to head (e.g., `priority = priority + 1`, or define a head-marker mechanism).
     - For regional claims: substrate no-op for direct mode (writes already in the DB).
   - `Abandon(ctx, region, address, policyOverride)`:
     - For pick-policy claims: apply the action specified by `policyOverride` (default to `on_give_up_default`).
     - For regional claims: degenerate for direct mode.
   - `Delete(ctx, region)`: regional only; not applicable to pick-policy claims.
   - `Release(ctx, region, address)`: nothing to release for direct-mode read claims.
5. Drop the old `ResolveOnTerminal` algorithm (first-delete-wins / last-released-wins / `actual_action` / `delete_won` sentinels). Auto-terminal in the supervisor handles resolution; this store just exposes the per-action verbs.
6. Update `Capabilities()` → `{WriteSemantics: WriteSemanticsDirect}`.
7. `Factory.MaxWriteSemantics()` returns `WriteSemanticsDirect`. (Spec §M1 — no `staged_async` substrate in v1.)
8. Update `holders.go` (or successor file): ensure no first-delete-wins / last-released-wins logic remains.
9. Update tests:
   - `factory_test.go` — exercise pick-policy config parsing.
   - `store_test.go` — exercise each verb against a testcontainer postgres.
   - `holders_test.go` — verify the simplified release semantics.
10. Update all importers in `core/cmd/`, `core/config/`, etc. to import the new path. `grep -rl claimstorepg core/ deploy/` and update each reference.

**Verification:**
```sh
git status            # confirm rename detected
go build ./core/store/postgres/
go test ./core/store/postgres/ -count=1 -race
grep -r 'claimstorepg' core/ deploy/  # should produce no hits
```

---

## Substitution engine

### T9: Update `core/attributes/substitution.go` per spec §16

**Files:**
- `core/attributes/substitution.go`
- `core/attributes/substitution_test.go`
- `core/attributes/store.go` (if it references `ResolveContext`)

**Steps:**

1. Update `ResolveContext`:
   ```go
   type ResolveContext struct {
       Deps   map[string]json.RawMessage
       Claim  map[string]ClaimResult  // keyed by alias; ClaimResult from core/store
       Params map[string]json.RawMessage
   }
   ```
2. Add new substitution paths handled by `Resolve`:
   - `{{claim.<alias>.address}}` — extract the alias's `ClaimResult.Address`.
   - `{{claim.<alias>.payload.<field>}}` — extract via `walkPath` into payload.
   - `{{claim.<alias>.region}}` — extract the alias's `ClaimResult.Region`.
3. Update `walkPath` to:
   - Accept `json.RawMessage` (or equivalent opaque-bytes shape) and a path slice.
   - Lazy-unmarshal into a transient `map[string]any` *only inside the function*; discard after extraction.
   - Annotate as the sole sanctioned introspection site per blessed invariant 20:
     ```go
     // @blessed-invariant 20: this is the only sanctioned introspection
     //   site for claim content. All other code paths must treat
     //   ClaimResult fields as opaque bytes.
     ```
4. Update `resolveDeps` and `resolveParams` to handle `json.RawMessage` shape — same lazy-unmarshal pattern.
5. Add error path: when a substitution path is invalid in shape, the error message must NOT include the value being walked (per inertness audit, spec §17.3).
6. Update tests:
   - `substitution_test.go` — exercise the three new paths (`address`, `payload.<f>`, `region`).
   - Add a test that `walkPath` does not log claim content on error.

**Verification:**
```sh
go build ./core/attributes/
go test ./core/attributes/ -count=1 -race
```

---

## Template parser & deploy-time validation

The current template parsing lives in `core/node/` (per spec §11.1: "`core/node/` — template parsing imports `core/attributes/` for the substitution-grammar types").

### T10: Add `inherits:` parsing to template DSL

**Files:** template parser source files in `core/node/`. Locate the relevant files with:
```sh
grep -ln 'stores' core/node/*.go
```
Typical files: `core/node/template.go`, `core/node/parse.go`, or whichever file owns the per-node template struct (the one with `Stores []StoreEntry` or analog field).

**Steps:**

1. Add an `Inherits` field to the node-template Go struct:
   ```go
   Inherits []InheritEntry  // omitempty
   ```
   where `InheritEntry { Claim string }` (just a claim alias for v1).
2. Update YAML/JSON tags as appropriate.
3. Update the parser to accept `inherits:` blocks at the node level.

**Verification:**
```sh
go build ./core/node/
go test ./core/node/ -count=1 -run TestParseInherits  # add this test if missing
```

### T11: Add `claim_resolutions:` parsing on acquiring node

**Files:** template parser source files in `core/node/`.

**Steps:**

1. Add a `ClaimResolutions` field to the node-template struct:
   ```go
   ClaimResolutions map[string]ClaimResolution  // keyed by alias
   ```
   where `ClaimResolution { OnCommit, OnGiveUp string }`.
2. Update the parser. The block lives on the acquiring node (not on terminals; spec §14.3).
3. If a node has no `inherits:` and no downstream node inherits, `ClaimResolutions` is optional. If any inheritance edge exists for the claim, validation in T14 ensures the acquirer has `ClaimResolutions` for that alias.

**Verification:**
```sh
go build ./core/node/
go test ./core/node/ -count=1 -run TestParseClaimResolutions
```

### T12: Add per-claim `alias:` field to claim entries

**Files:** template parser source files in `core/node/`.

**Steps:**

1. Add `Alias string` to the claim-entry struct (the per-`stores:` entry shape).
2. Default behavior: if `Alias` is empty after parsing, set it to the store name (`StoreName`) at parse time or at first-use.

**Verification:**
```sh
go build ./core/node/
go test ./core/node/ -count=1 -run TestParseAlias
```

### T13: Drop `held: true` flag handling

**Files:** template parser source files in `core/node/`; any other code paths checking the flag (`grep -rn 'held' core/`).

**Steps:**

1. Remove the `Held` field from the claim-entry struct.
2. Remove any code paths that switched on `Held`. Held semantics are now derived from `Inherits` per spec §14.
3. Remove related fields like `OnCommit`/`OnGiveUp` from the per-claim entry if they exist (these now live in `ClaimResolutions` on the acquiring node).

**Verification:**
```sh
grep -rn '\bHeld\b' core/  # should produce no hits in functional code
go build ./...
```

### T14: Holding-subgraph computation and deploy-time validation per spec §18

**Files:** new file `core/node/inheritance.go` (or equivalent within the existing template parser package).

**Dependency note:** the pick-policy validation (step 1, last bullet) needs the operator's store registry — built in T22. Two options:
- Land T22 before T14. Reorder if needed.
- OR: split T14 into T14a (deploy-time validation that doesn't need the registry — undeclared aliases, missing dep paths, missing claim_resolutions) and T14b (registry-dependent pick-policy validation, after T22).

The plan as written executes T14 before T22 in sequence. Treat T14 as T14a for now; the pick-policy intent validation is added as a follow-up in T23 once the registry is built. Update T23 accordingly (see T23 below).

**Steps (T14a):**

1. Implement the algorithm in spec §18.4:
   - Walk all nodes in the template; record acquirers per alias.
   - Walk all `inherits:` declarations; for each, find the acquirer reachable via deps; record the inheritance edge.
   - For each (acquirer, alias) with at least one inheritance edge: compute holding subgraph = `{acquirer} ∪ {direct inheritors}`.
   - Validate: every `inherits:` reference resolves; if not, reject template with a clear error.
   - Validate: every `{{claim.<alias>.<...>}}` substitution in any node is in the alias's holding subgraph (or the acquirer); else reject.
   - Validate: for held claims (subgraph size > 1), the acquirer's `ClaimResolutions[alias]` is declared with both `on_commit` and `on_give_up`; else reject.
   - **Defer:** pick-policy `intent: rw` enforcement (per spec §14.5) — added in T23 after the store registry is built in T22.
2. Persist the holding-subgraph membership in template metadata (serialize as part of the parsed graph data already stored on `rimsky_templates.graph_data`).
3. Reject templates that fail any validation rule.

**Verification:**
```sh
go build ./core/node/
go test ./core/node/ -count=1 -run TestHoldingSubgraph
```
Add tests covering: undeclared alias inheritance, missing dep path, missing claim_resolutions, pick-policy with intent=r.

### T15: Drop or rewrite `core/node/` validation paths that referenced removed fields

**Files:** any other validation code in `core/node/`.

**Steps:**

1. `grep -rn 'OnCommit\|OnGiveUp\|RestoreVersion' core/node/` — for each hit, decide: keep (if migrated to new shape) or drop. Spec §10 confirms `RestoreVersion` is permanently out.
2. Drop validation rules tied to removed concepts (`held: true`, `RestoreVersion`).

**Verification:**
```sh
go build ./core/node/
go test ./core/node/ -count=1
```

---

## Supervisor

### T16: Adapt `core/supervisor/runner.go` atomic acquisition flow per spec §13.3

**Files:** `core/supervisor/runner.go` and any helper files in `core/supervisor/`.

**Boundary with T21 (queue eligibility predicate):** the conflict predicate has two layers — region overlap (substrate-side `Store.RegionsConflict`) and mode coexistence (supervisor-side §8.5 matrix). The mode-coexistence matrix is a small pure helper; place it in `core/store/conflict.go` (new file) so both the supervisor (T16) and the queue (T21) can call it without circular imports. The supervisor's acquisition flow consumes both layers; the queue's eligibility predicate consumes both layers. T21 references the same helper.

**Steps:**

1. Add a new file `core/store/conflict.go` with a pure helper:
   ```go
   // ModeCoexists reports whether two claims with given intents on stores
   // with given write_semantics can coexist on overlapping regions, per
   // spec §8.5 matrix.
   func ModeCoexists(intentA Intent, semA WriteSemantics, intentB Intent, semB WriteSemantics) bool
   ```
   Implement per the §8.5 matrix:
   - Different stores → not relevant (caller filters by store_name first; if reached, return true).
   - Sync block (semA in {direct, staged_blocking}, semB matches): r×r → true; r×rw / rw×r / rw×rw → false.
   - Async block (semA == staged_async, semB matches): r×r / r×rw / rw×r → true; rw×rw → false.
2. Update the acquisition transaction (the spec §13.3 flow):
   - Step 4 sub-flow:
     - 4a/4b: re-load existing region holders for the store; re-evaluate conflict (region overlap via `Store.RegionsConflict` AND mode coexistence via `ModeCoexists`).
     - 4c: insert lock-holder row with `address = NULL`, `intent = spec.Intent`.
     - 4d: call `Store.Open(ctx, spec)` with the open `pgx.Tx` shared via `store.TxFromContext(ctx)`.
     - 4e: `UPDATE rimsky_lock_holders SET address = $1 WHERE id = $2` claimant-guarded on `holder_supervisor_id`.
     - 4f: if the claim is held (lookup template metadata for the claim's holding subgraph; size > 1), insert one `rimsky_claim_holders` row per holding-subgraph member, all `state='active'`, FK'd to the new lock-holder row.
3. Verify-before-run path (blessed invariant 5) unchanged in shape — re-reads `claimed_by` immediately before calling the executor; bails as `orphaned_claim_lost_race` if ownership moved.
4. Drop any references to `LockHandle`, `NativeHandle`, `ReleaseAction` (dead types per T1).
5. Drop `held: true` flag handling.
6. Update the executor envelope construction: package the address bytes into the `ExecuteRequest.stores[<name>].handle` field (substrate-native; opaque to Rimsky).

**Verification:**
```sh
go build ./core/supervisor/
go vet ./core/supervisor/
go test ./core/supervisor/ -count=1 -race
```

(Existing scenario tests under `test/scenarios/` — `lock_atomic_acquisition_test.go`, `verify_before_run_race_test.go`, `lock_claimant_guarded_release_test.go` — will likely fail until T17 + T19 also complete; they're run as a group at T42 and the final verification phase. Capture failures here; the failures are expected to clear once T19 completes.)

### T17: New `core/supervisor/auto_terminal.go` per spec §14.4

**Files:** `core/supervisor/auto_terminal.go` (new), `core/supervisor/auto_terminal_test.go` (new).

**Steps:**

1. Implement the auto-terminal mechanism per spec §14.4 / §14.4.1 / §14.4.2:
   - Function `CheckAndFireResolution(ctx, tx, nodeID) error`:
     - Find every held claim this node was part of (acquirer or inheritor) — query `rimsky_claim_holders WHERE holder_node_id = $1`.
     - For each: lock the lock-holder row (`SELECT … FOR UPDATE`).
     - Query: are all `rimsky_claim_holders WHERE lock_holder_id = $1 AND state='active'` zero? If not, no-op; auto-terminal fires when the last one terminates.
     - Compute aggregate outcome: any row with `state='failed'` → fire `on_give_up`; else fire `on_commit`.
     - Look up `claim_resolutions` on the acquirer's template metadata (passed via supervisor context).
     - Resolve the action per spec §14.4.1 routing table:
       - `"commit"` (or empty) → `Store.Commit(region, address, "")`.
       - `"abandon"` (or empty for failure path) → `Store.Abandon(region, address, "")`.
       - `"delete"` → `Store.Delete(region)`.
       - `"release_to_back"` / `"release_to_head"` → `Store.Commit(region, address, action)` (success) or `Store.Abandon(region, address, action)` (failure).
     - Delete the lock-holder row claimant-guarded. Cascade FK cleans up `rimsky_claim_holders` rows.
   - All the above runs in a single SQL transaction (the caller's tx, passed in).
2. Ensure verb invocation uses `region` and `address` from the lock-holder row (read at SELECT FOR UPDATE time).
3. Tests in `auto_terminal_test.go`:
   - All-success path fires `on_commit`.
   - Mixed success-failure fires `on_give_up`.
   - All-failure fires `on_give_up`.
   - `on_commit = "delete"` routes to `Store.Delete`.
   - `on_commit = "release_to_back"` routes to `Store.Commit(policyOverride="release_to_back")`.
   - Concurrent-termination race safety: two goroutines both calling `CheckAndFireResolution` for the same lock-holder; only one fires.

**Verification:**
```sh
go build ./core/supervisor/
go test ./core/supervisor/ -count=1 -race -run TestAutoTerminal
```

### T18: Update `core/supervisor/runner.go` heartbeat per spec §13.4

**Files:** `core/supervisor/runner.go` (or wherever heartbeat lives).

**Steps:**

1. Update the heartbeat tick query to handle both standard and held-claim cases per spec §13.4. Use the SQL query from the spec (UNION of `holder_node_id IN running-nodes` and `EXISTS (claim_holders ... AND n.state='running')`).
2. Add a new scenario test `test/scenarios/heartbeat_held_claim_test.go` that:
   - Acquires a held claim with one acquirer and one inheritor (different nodes).
   - Terminates the acquirer (so its lock-holder row's `holder_node_id` is no longer running).
   - Confirms the lock-holder row continues to be heartbeated while the inheritor is running.
   - Confirms the row stops being heartbeated after the inheritor terminates (and is reaped by the orphan-reap path or finalized by auto-terminal — auto-terminal should fire first).

**Verification:**
```sh
go test ./core/supervisor/... -count=1 -race
```

### T19: Update `core/supervisor/runner.go` release flow per spec §13.6

**Files:** `core/supervisor/runner.go`.

**Steps:**

1. Implement the four-step release flow per spec §13.6:
   - Step 1 (region claims acquired by this node): if held with active inheritors, do NOT delete the lock-holder row; just mark the acquirer's `rimsky_claim_holders` row `'completed'`/`'failed'`. Else, fire substrate verb (Commit/Abandon/Delete based on `template_store.GetClaimResolution(ctx, templateID, nodeName, alias)` — helper from T4 — applied per spec §14.4.1 routing table) and delete the lock-holder row.
   - Step 2 (region claims where this node is an inheritor): mark the node's `rimsky_claim_holders` row.
   - Step 3 (named lock holders owned by this node): claimant-guarded delete.
   - Step 4 (auto-terminal check): call `CheckAndFireResolution` from T17.
2. All four steps run in a single SQL transaction with the node's terminal state transition. Concretely: a single `BEGIN…COMMIT` brackets steps 1–4. If the substrate verb in step 1 returns an error, the entire transaction rolls back — no claim-holders rows updated, no lock-holder rows deleted, no node state transition. Caller (terminal handler) then routes through the standard error-handling path.
3. Pass the `pgx.Tx` correctly through to substrate verb calls via `store.WithTx(ctx, tx)`.
4. Look up `claim_resolutions` via `template_store.GetClaimResolution(ctx, templateID, acquirerNodeName, alias)` (helper from T4) for both held and non-held claims.

**Verification:**
```sh
go build ./core/supervisor/
go test ./core/supervisor/ -count=1 -race
```

Atomic-rollback assertion — covered by scenario test `auto_terminal_aggregate_outcome_test.go` (T32) which exercises a substrate-error path during step 1 and verifies (a) lock-holder row is unchanged, (b) claim-holders rows are unchanged, (c) node state did not transition. Add this assertion explicitly in T32 if not already there; the spec §13.6 closing line "All four steps commit atomically" is load-bearing.

---

## Scheduler

### T20: Update `core/scheduler/scheduler.go` pick-policy sweep per spec §12.12

**Files:** `core/scheduler/scheduler.go`.

**Steps:**

1. Update the visibility-timeout sweep to iterate each store's configured `pick_policies` block (was: only claim-store-postgres).
2. For each pick-policy entry: run the sweep query from spec §12.12 (`UPDATE <items_table> SET state='available' WHERE state='in_progress' AND claimed_at < ... AND NOT EXISTS (rimsky_lock_holders match)`).
3. Confirm sweep is gated by the existing `pg_try_advisory_lock(SCHEDULER_TICK_KEY)` (blessed invariant 7).

**Verification:**
```sh
go build ./core/scheduler/
go test ./core/scheduler/ -count=1
```

---

## Queue

### T21: Update `core/queue/postgres/queue.go` conflict predicate per spec §13.2

**Files:** `core/queue/postgres/queue.go`.

**Steps:**

1. Update the eligibility predicate to use the two-layer conflict check:
   - Region overlap: `Store.RegionsConflict` per existing pattern.
   - Mode coexistence: call `store.ModeCoexists(intentA, semA, intentB, semB)` (helper defined in T16). Read `intent` from each lock-holder row's new `intent` column; read `write_semantics` from the store's static config (passed via the queue's constructor or accessed via the registry).
2. The predicate result feeds into `SelectCandidates` / `ClaimDispatchRow` (existing functions; signature unchanged).
3. Drop any references to the old `concurrency_tags` / `LockMode` / claim-vs-region kind branching.

**Verification:**
```sh
go build ./core/queue/postgres/
go test ./core/queue/postgres/ -count=1 -race
```

---

## Operator config

### T22: Add `named_locks:` top-level block to the operator config bundle per spec §15.3

**Files:** `core/config/`, `core/cmd/rimsky-supervisor/main.go`, `core/cmd/rimsky-control-api/main.go`, `core/cmd/rimsky-scheduler/main.go`, `deploy/stores.yml`.

**Steps:**

1. **One file.** Per spec §15.3, stores and named locks live in **one** operator config bundle, loaded from the same `RIMSKY_STORES_CONFIG` env-var path. Extend the existing single YAML file with a top-level `named_locks:` block alongside `stores:`. There is no separate `named_locks.yml` file.
2. Add a `NamedLocks` field to the operator-config struct (probably in `core/config/`):
   ```go
   type OperatorConfig struct {
       Stores     map[string]map[string]any  `yaml:"stores"`
       NamedLocks map[string]NamedLockConfig `yaml:"named_locks"`
   }
   type NamedLockConfig struct {
       Limit int  `yaml:"limit"`  // limit=1 for mutex; limit=N for counting semaphore
   }
   ```
3. Update the YAML loader to parse the `named_locks:` block.
4. Validate at startup: every `NamedLockConfig.Limit >= 1`. Reject invalid configs with a clear error.
5. Build a registry of named locks accessible to the supervisor for deploy-time validation (T23) and dispatch eligibility (T21).
6. Update `deploy/stores.yml` (the reference operator config) to include a sample `named_locks:` block at the top level alongside `stores:`.
7. Update each binary in `core/cmd/` that loads `RIMSKY_STORES_CONFIG` to also load named locks from the same config bundle (control-api, supervisor, scheduler).

**Verification:**
```sh
go build ./core/cmd/...
go test ./core/config/ -count=1
docker compose -f deploy/docker-compose.yml config  # YAML parses
```

### T23: Operator-registry-dependent template validation (named locks + pick-policy intent)

**Files:** `core/node/` (validation code; same module as T14).

**Steps:**

1. Add named-lock-reference validation: the template's `locks: [{name: ...}]` references are validated at template-deploy time against the operator's `named_locks:` block (built in T22). Reject deploys that reference an undeclared named lock.
2. Add pick-policy-intent validation per spec §14.5: for each claim entry in any node, look up the store in the registry (built in T22); inspect the store's `pick_policies` config; if `claim.Selector` matches any configured pick-policy key, the claim's `Intent` must be `IntentReadWrite`. Else reject the template.
3. Both checks run after the T14a validations and use the registry passed into the validator.

**Verification:**
```sh
go build ./core/node/
go test ./core/node/ -count=1 -run TestNamedLockReferenceValidation
go test ./core/node/ -count=1 -run TestPickPolicyIntentValidation
```

---

## Deletion sweep

### T24: Drop dead code

**Files:** various.

**Steps:**

1. `grep -rn 'LockSpec\b\|LockHandle\b\|NativeHandle\b\|FilesystemDirectHandle\|ClaimStoreHandle\|ReleaseAction\|ClaimableStore\|ResumableStore\|HasPriorWork\|RestoreVersion' core/`. For each hit:
   - If it's a definition, confirm it's been removed in T1, T2, T8, or one of the implementation tasks (T1–T23).
   - If it's a usage, update or delete per the spec.
2. `grep -rn 'concurrency_tags\|claim_id\|on_commit\|on_give_up\|actual_action\|delete_won' core/migrations/`. Confirm clean.
3. Drop the `held: true` flag handling sites (already done in T13; verify no remnants).
4. Drop any dropped capability flags (`SupportsRegionLock`, `SupportsClaim`, `SupportsResume`, `SupportsRestore`, `SupportsAtomicMulti`, `SupportsDiscard`, `KeepVersionsMax`, `payload_encryption`, `async_supports_dynamic_selectors`, `commit_atomicity_scope`).

**Verification:**
```sh
go build ./...
go vet ./...
```

### T25: `inline-jsonb` and stale `Resource` reference sweep per spec §20.5

**Files:** `docs/`, `core/`, `proto/v1/`.

**Steps:**

1. `grep -rn 'inline-jsonb' docs/ core/ proto/`. Remove or update each occurrence.
2. `grep -rn '\bResource\b' docs/ core/ proto/`. The lower-case `resource` is fine as English; flag instances of capital `Resource` that escaped the prior stores-redesign.
3. Update or delete each stale reference.

**Verification:**
```sh
grep -rn 'inline-jsonb' docs/ core/ proto/  # zero hits
```

---

## Inertness audit per spec §17.3

### T26: Audit `proto/v1/events.proto` and supervisor's emit path

**Files:** `proto/v1/events.proto`, `core/supervisor/events.go` (or wherever events emit).

**Steps:**

1. Inspect every field in `events.proto` and confirm no event-payload field has a type that could carry claim content (`Address`, `Payload`, `Region` from `ClaimResult`). For each field that names anything resembling claim content, trace its source: is it built from `ClaimResult` fields anywhere? If yes, fix.
2. Inspect every `slog.` call in `core/supervisor/`, `core/store/`, `core/queue/`, `core/scheduler/`, `core/node/`. Confirm no `slog.Any("payload", ...)`, `slog.Any("address", ...)`, `slog.Any("region", ...)`, or analogous; no `%+v` formatting of values that could contain claim content.
3. If any leak found: fix by redacting or omitting the field.

**Verification:**
```sh
# Comprehensive grep for likely leak shapes
grep -rn 'slog\.Any' core/supervisor/ core/store/ core/queue/ core/scheduler/ core/node/ | \
    grep -E '(payload|address|region|claim|attribute)'
# Should produce zero hits.

grep -rn '%\+v\|%v\|%#v' core/supervisor/ core/store/ core/queue/ core/scheduler/ core/node/ | \
    grep -E '(ClaimResult|Address|Payload|Region)'
# Should produce zero hits.
```

The behavioral assertion lives in T39 (`inertness_audit_test.go`), which captures slog output during exercised verb paths and asserts no claim-content bytes appear.

### T27: Audit `core/attributes/substitution.go` error paths per spec §17.3

**Files:** `core/attributes/substitution.go`.

**Steps:**

1. Read every error-return path in `Resolve`, `walkPath`, `resolveDeps`, etc.
2. Confirm error messages do NOT include the value being walked (the substrate-supplied bytes).
3. Confirm error messages may include path tokens (e.g., `"field 'foo.bar' not found"`) but not values.

**Verification:**
```sh
go test ./core/attributes/ -run TestSubstitutionErrorRedaction -count=1
```
Add the test in `core/attributes/substitution_test.go` if missing: walk a path with a deliberately-wrong-shape value; assert the returned error contains the path tokens but does NOT contain a known sentinel bytestring planted in the value.

### T28: Audit `rimsky_events.event_detail` JSON column

**Files:** wherever event-detail is built (likely `core/supervisor/events.go`).

**Steps:**

1. Confirm no event-detail value flows from `ClaimResult` fields.
2. If any flow exists: refactor to extract only event-relevant fields (e.g., node_id, supervisor_id, frame_id, error class) — never claim content.

**Verification:**
```sh
grep -rn 'event_detail' core/ | xargs -I{} grep -l 'Address\|Payload\|Region' {}  # zero hits
```

### T29: Audit `core/store/` debug log paths

**Files:** `core/store/postgres/*.go`, `core/store/filesystem/*.go`, `core/store/stub/*.go`.

**Steps:**

1. `grep -n 'slog\.' core/store/postgres/ core/store/filesystem/ core/store/stub/`.
2. Confirm no `slog.Any("payload", ...)`, `slog.Any("address", ...)`, `slog.Any("region", ...)` etc.
3. Confirm structured fields use only operator-relevant identifiers (store name, node id, supervisor id, error class).

**Verification:**
```sh
# Verify each scrutinized file has no slog.Any patterns naming claim content
for f in core/store/postgres/*.go core/store/filesystem/*.go core/store/stub/*.go; do
    if grep -E 'slog\.Any\(.*(payload|address|region|claim)' "$f"; then
        echo "LEAK in $f"; exit 1
    fi
done
```
The behavioral assertion lives in T39 (`inertness_audit_test.go`); this command catches the static patterns.

### T30: Codify blessed invariant 20 in CLAUDE.md and source annotations

**Files:** `CLAUDE.md`, `core/store/types.go` (already done in T1), `core/attributes/substitution.go` (already done in T9).

**Steps:**

1. Update `CLAUDE.md`'s blessed-invariants list to add invariant 20 with the wording from spec §21:
   ```
   20. Claim content (payload, address, region) is inert in Rimsky.
   ```
2. Reference `docs/glossary.md` from CLAUDE.md vocabulary section.
3. Confirm the invariant 20 annotations are in place at:
   - `core/store/types.go` on `ClaimResult` (T1).
   - `core/attributes/substitution.go::walkPath` (T9).

**Verification:**
```sh
grep -F 'invariant 20' CLAUDE.md                                # >= 1 hit
grep -F 'docs/glossary.md' CLAUDE.md                             # >= 1 hit
grep -F '@blessed-invariant 20' core/store/types.go              # >= 1 hit
grep -F '@blessed-invariant 20' core/attributes/substitution.go  # >= 1 hit
```

---

## Tests

The spec's §22.1 lists scenario tests to add. Each below.

**Scenario-test pattern (T0 step 8 — read first):** scenario tests use testcontainers-go via the helpers in `core/internal/pgtest` (real postgres). Each test boots its own container. The general shape:
1. `pgtest.New(t)` to spin a fresh postgres + run migrations.
2. Construct a `core/store.Registry` with the test stores configured.
3. Spawn a supervisor (or a minimal supervisor harness) with the test pool.
4. Construct a template with the desired graph; persist via the template store.
5. Create an instance; let the scheduler/supervisor dispatch nodes.
6. Assert state via direct DB queries or via the supervisor's exposed observers.

Reuse harness helpers from existing scenarios (e.g., `lock_atomic_acquisition_test.go`) where possible. New scenario tests below follow the same shape.

### T31: Scenario test — `verify_open_inside_acquisition_tx`

**Files:** `test/scenarios/verify_open_inside_acquisition_tx_test.go` (new).

**Steps:** Implement per spec §22.1 first bullet — `Store.Open` is called inside the §13.3 atomic transaction; substrate-side state and lock-holder row commit atomically; on substrate error, both roll back.

**Verification:** `go test ./test/scenarios/ -run TestOpenInsideAcquisitionTx -count=1 -race`.

### T32: Scenario test — `auto_terminal_aggregate_outcome`

**Files:** `test/scenarios/auto_terminal_aggregate_outcome_test.go` (new).

**Steps:** Held claim with N-node holding subgraph; mixed terminal outcomes (all-success vs. any-failure) drive correct aggregate action.

**Verification:** `go test ./test/scenarios/ -run TestAutoTerminalAggregateOutcome -count=1 -race`.

### T33: Scenario test — `auto_terminal_failure_propagation`

**Files:** `test/scenarios/auto_terminal_failure_propagation_test.go` (new).

**Steps:** Non-terminal node in holding subgraph fails; auto-terminal fires `on_give_up` when subgraph completes (closes prior spec's Case 6 gap).

**Verification:** `go test ./test/scenarios/ -run TestAutoTerminalFailurePropagation -count=1 -race`.

### T34: Scenario test — `inheritance_validation`

**Files:** `test/scenarios/inheritance_validation_test.go` (new).

**Steps:** Deploy-time validation rejects: `inherits:` against undeclared aliases, missing dep paths, missing `claim_resolutions` for held aliases, pick-policy `intent: r`.

**Verification:** `go test ./test/scenarios/ -run TestInheritanceValidation -count=1`.

### T35: Scenario test — `address_inheritance_lifetime`

**Files:** `test/scenarios/address_inheritance_lifetime_test.go` (new).

**Steps:** `{{claim.<alias>.address}}` in inheriting node resolves to live address; substrate-side state survives until subgraph completion.

**Verification:** `go test ./test/scenarios/ -run TestAddressInheritanceLifetime -count=1 -race`.

### T36: Scenario test — `value_pass_lifetime`

**Files:** `test/scenarios/value_pass_lifetime_test.go` (new).

**Steps:** `{{deps.<source>.<field>}}` works after the source's claim has closed.

**Verification:** `go test ./test/scenarios/ -run TestValuePassLifetime -count=1 -race`.

### T37: Scenario test — `pick_policy_selector`

**Files:** `test/scenarios/pick_policy_selector_test.go` (new).

**Steps:** Substrate-recognized selector forms (`@queue`, `@ring`) trigger configured pick policies; multiple policies per store.

**Verification:** `go test ./test/scenarios/ -run TestPickPolicySelector -count=1 -race`.

### T38: Scenario test — `frame_id_observability_only`

**Files:** `test/scenarios/frame_id_observability_only_test.go` (new).

**Steps:** Held-claim algorithm matches by `lock_holder_id`, not `frame_id`; recycled identifiers across frames don't collide.

**Verification:** `go test ./test/scenarios/ -run TestFrameIDObservabilityOnly -count=1 -race`.

### T39: Scenario test — `inertness_audit`

**Files:** `test/scenarios/inertness_audit_test.go` (new).

**Steps:** Exercise every code path that handles claim content; capture slog output and assert no claim-content bytes present.

**Verification:** `go test ./test/scenarios/ -run TestInertnessAudit -count=1`.

### T40: Scenario test — `single_writer_per_region`

**Files:** `test/scenarios/single_writer_per_region_test.go` (new).

**Steps:** Under all three `write_semantics` values, two `rw` claims on overlapping regions never coexist.

**Verification:** `go test ./test/scenarios/ -run TestSingleWriterPerRegion -count=1 -race`.

### T41: Scenario test — `staged_async_protocol_present_no_substrate`

**Files:** `test/scenarios/staged_async_protocol_present_no_substrate_test.go` (new).

**Steps:** Protocol verbs `Open(read)` + `Release` exist in the interface; no v1 implementation registers state for `staged_async`; supervisor-side handling is correct (would route through `Release` if a substrate did register state).

**Verification:** `go test ./test/scenarios/ -run TestStagedAsyncProtocolPresent -count=1`.

### T42: Update existing scenario tests for new vocabulary

**Files:** all under `test/scenarios/`.

**Steps:**

1. `grep -rn 'claim-store-postgres\|claim_store\|held: true\|RestoreVersion\|LockSpec\|RegionLockSpec\|ClaimLockSpec\|ReleaseAction' test/scenarios/`. For each hit: update to new vocabulary.
2. Ensure existing scenario tests still pass:
   - `verify_before_run_race_test.go` (invariant 5).
   - `state_machine_same_state_rejected_test.go` (invariant 1).
   - `lock_atomic_acquisition_test.go` (invariant 10).
   - `lock_claimant_guarded_release_test.go` (invariant 4).
   - `claim_hold_fan_out_first_delete_wins_test.go` — rewrite per the new auto-terminal semantics (first-delete-wins / last-released-wins reconciliation is removed per spec §10 / §22.1). Rename to `claim_hold_fan_out_auto_terminal_test.go`. The replacement test exercises auto-terminal aggregate-outcome resolution: fan-out subgraph with mixed terminal outcomes; verify the supervisor fires `on_commit` (all-success) or `on_give_up` (any-failure) per spec §14.4 — never first-delete-wins.

**Verification:** `go test ./test/scenarios/ -count=1 -race`.

### T43: Update smoke fixture per spec §22.2

**Files:** `test/smoke/`.

**Steps:**

1. Update the smoke fixture to exercise:
   - A queue-worker pipeline using `@review-queue` selector with multi-node holding subgraph (acquirer + 2 inheritors + 1 value-pass-only downstream).
   - Multiple pick policies on the same postgres store.
   - 100 sequential force-fires through `POST /admin/scheduled-nodes/{id}/force-fire`.

**Verification:**
```sh
docker compose -f deploy/docker-compose.yml up -d
for i in $(seq 1 30); do
    if curl -fs http://localhost:8080/health > /dev/null; then break; fi
    sleep 2
done  # wait for healthy
curl http://localhost:8080/health  # expect 200
go test ./test/smoke/... -count=1
docker compose -f deploy/docker-compose.yml down
```

---

## Documentation

### T44: Update `CLAUDE.md`

**Files:** `CLAUDE.md`.

**Steps:**

1. Update the blessed-invariants list (already partially done in T30): add invariant 20.
2. Update gotchas section with vocabulary changes:
   - "Claim store" no longer a kind name.
   - `held: true` flag dissolved; held is implicit from `inherits:`.
   - Auto-terminal at subgraph completion replaces first-delete-wins.
   - `claim_id` column dropped (folds into `region_data`); `address` column added; `lock_holder_id` FK on `rimsky_claim_holders`.
   - Capability struct has one field: `write_semantics`.
3. Reference `docs/glossary.md` as the authoritative naming source.
4. Note: invariant 10 preserved for in-process; OOP cycle will revisit.

**Verification:**
```sh
grep -F 'invariant 20' CLAUDE.md
grep -F 'docs/glossary.md' CLAUDE.md
grep -F 'inherits:' CLAUDE.md
grep -F 'auto-terminal' CLAUDE.md
grep -F 'write_semantics' CLAUDE.md
grep -F 'address column' CLAUDE.md
# All should produce ≥ 1 hit each. None should be the only hit (i.e., no orphan mention).
# Negative checks:
! grep -F 'held: true' CLAUDE.md  # flag dissolved
! grep -F 'claim store' CLAUDE.md  # kind name dissolved
```

### T45: Update `docs/protocol.md`

**Files:** `docs/protocol.md`.

**Steps:**

1. Update protocol verb list: 5 verbs (Open, Commit, Abandon, Delete, Release).
2. Update payload/address/region — distinct outputs of `Open`.
3. Drop versioned-mode references.
4. Reference `proto/v1/node_executor.proto` as authoritative source for the wire contract.

**Verification:**
```sh
grep -F 'Open' docs/protocol.md
grep -F 'Commit' docs/protocol.md
grep -F 'Abandon' docs/protocol.md
grep -F 'Delete' docs/protocol.md
grep -F 'Release' docs/protocol.md
! grep -iF 'versioned mode' docs/protocol.md
```

### T46: Update `docs/architecture.md`

**Files:** `docs/architecture.md`.

**Steps:**

1. Update `core/store/` description: 5 verbs, two primitives (claim, named lock), no `claim` kind.
2. Update package-import-rules section: reference the new file layout (`core/store/postgres/` instead of `claimstorepg`).
3. Update blessed-invariants section to mention 9 → 9a/9b, 13 revised, 15/20 added.

**Verification:**
```sh
grep -F 'core/store/postgres' docs/architecture.md
grep -F 'named lock' docs/architecture.md
! grep -F 'claimstorepg' docs/architecture.md
grep -F '9a' docs/architecture.md
grep -F '9b' docs/architecture.md
grep -F '20.' docs/architecture.md  # invariant 20 listed
```

### T47: Update `docs/operator-guide.md`

**Files:** `docs/operator-guide.md`.

**Steps:**

1. Document `stores.yml` new shape: `pick_policies` block per store; `write_semantics` field; `named_locks:` top-level block.
2. Document auth-blind philosophy and encrypt-before-pass as recommended practice.
3. State that per-region overrides are not supported in v1 (use two stores pointing at the same underlying storage if needed).
4. Document deploy-time validation surface (what's checked, what's not).
5. Document the inertness invariant — claim content not under operator's logging discretion; store-config bytes are.

**Verification:**
```sh
grep -F 'pick_policies' docs/operator-guide.md
grep -F 'named_locks' docs/operator-guide.md
grep -F 'write_semantics' docs/operator-guide.md
grep -F 'auth-blind' docs/operator-guide.md
grep -iF 'encrypt-before-pass' docs/operator-guide.md
grep -F 'invariant 20' docs/operator-guide.md
```

### T48: Update `docs/store-author-guide.md`

**Files:** `docs/store-author-guide.md`.

**Steps:**

1. Document the 5-verb contract per spec §6.
2. Document pick policies as substrate-side configuration.
3. Document store-side serialization is forbidden (invariant 9b restated): no internal lock-state caches; honest `write_semantics` reporting.
4. Document the `staged_async` strategies (snapshot delegation, native MVCC pass-through; reader-lease serialization forbidden).
5. Document the address shape recommendation: substrate-native; opaque to Rimsky.

**Verification:**
```sh
grep -F 'Open' docs/store-author-guide.md
grep -F 'Commit' docs/store-author-guide.md
grep -F 'pick_policies' docs/store-author-guide.md
grep -F 'invariant 9' docs/store-author-guide.md  # 9b restatement
grep -F 'snapshot delegation' docs/store-author-guide.md
grep -F 'MVCC' docs/store-author-guide.md
```

### T49: Update `docs/executor-author-guide.md`

**Files:** `docs/executor-author-guide.md`.

**Steps:**

1. Document handle is the substrate-native `Address` from `Open`; opaque to Rimsky.
2. Document payload propagation via attributes (value-pass) vs claim inheritance (claim-pass).
3. Document encrypt-before-pass — executors decrypt at point of use.
4. Document attribute-substitution paths the executor consumes (`{{deps.<node>.<f>}}`, `{{claim.<alias>.<...>}}`, `{{params.<k>}}`).

**Verification:**
```sh
grep -F '{{deps.' docs/executor-author-guide.md
grep -F '{{claim.' docs/executor-author-guide.md
grep -F '{{params.' docs/executor-author-guide.md
grep -iF 'encrypt-before-pass' docs/executor-author-guide.md
```

### T50: Update `docs/node-graph-design.md`

**Files:** `docs/node-graph-design.md`.

**Steps:**

1. Update vocabulary: two primitives (claim, named lock); inheritance model; auto-terminal; two propagation modes.
2. Update §3 / §4 / §5 / §6 / §8 references per spec.
3. Reference `docs/glossary.md`.

**Verification:**
```sh
grep -F 'named lock' docs/node-graph-design.md
grep -F 'inherits' docs/node-graph-design.md
grep -F 'auto-terminal' docs/node-graph-design.md
grep -F 'docs/glossary.md' docs/node-graph-design.md
! grep -F 'held: true' docs/node-graph-design.md
! grep -F 'claim store' docs/node-graph-design.md
```

### T51: Update `CHANGELOG.md`

**Files:** `CHANGELOG.md`.

**Steps:**

1. Append an entry under `## Unreleased`:
   ```
   - Stores Redesign v2 (third major rewrite of core/store/):
     - 5 protocol verbs (Open, Commit, Abandon, Delete, Release) replace the prior AcquireLock/OpenHandle/Commit/ReleaseLock shape.
     - Two-noun primitives split: claim (substrate-bound) vs named lock (non-substrate).
     - Pick policies are substrate-side via substrate-recognized selector forms (`@policy-name` convention).
     - Held claims via explicit `inherits:` declarations; auto-terminal at holding-subgraph completion.
     - Capability struct collapsed to one field (write_semantics).
     - Schema: rimsky_lock_holders gains address column, drops claim_id; rimsky_claim_holders gains lock_holder_id FK, drops actual_action/delete_won.
     - Inertness invariant 20 added; pre-sweep type-hardening of claim-content fields to json.RawMessage.
     - Operator config gains named_locks: top-level block.
     - Versions permanently eliminated; versioned mode does not exist.
     - claim-store-postgres renamed to postgres; pick_policies block configures multiple named pick policies per store.
   ```

**Verification:**
```sh
grep -F 'Stores Redesign v2' CHANGELOG.md
grep -F 'auto-terminal' CHANGELOG.md
```

### T52: Update `docs/glossary.md` references in CLAUDE.md and other docs

**Files:** various.

**Steps:**

1. Confirm `docs/glossary.md` is referenced from at least: `CLAUDE.md`, `docs/architecture.md`, `docs/store-author-guide.md`, `docs/executor-author-guide.md`, `docs/operator-guide.md`, `docs/node-graph-design.md`, `docs/protocol.md`.

**Verification:**
```sh
for doc in CLAUDE.md docs/architecture.md docs/store-author-guide.md docs/executor-author-guide.md docs/operator-guide.md docs/node-graph-design.md docs/protocol.md; do
    if ! grep -F 'docs/glossary.md' "$doc" > /dev/null; then
        echo "MISSING: $doc does not reference docs/glossary.md"; exit 1
    fi
done
```

---

## Final verification

### T53: Proto regeneration if `proto/v1/node_executor.proto` changed

**Steps:**

1. `git diff main -- proto/v1/`. If any proto source file changed:
   ```sh
   make proto-gen
   ```
2. Confirm `proto/v1/gen/` is up-to-date with the proto source.

**Verification:** `git status proto/v1/gen/` — generated files reflect proto sources.

### T54: Full test suite

**Steps:**

```sh
cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky
go build ./...
go test ./... -count=1
go test ./test/scenarios/... -count=3 -race
make lint
make tidy   # confirm go.mod / go.sum unchanged or only-additive
```

**Verification:** all pass.

### T55: Docker images rebuild and smoke test

**Steps:**

```sh
docker compose -f deploy/docker-compose.yml build
docker compose -f deploy/docker-compose.yml up -d

# Poll until /health returns 200 (or fail after 60s)
for i in $(seq 1 30); do
    if curl -fs http://localhost:8080/health > /dev/null; then break; fi
    sleep 2
done
curl -fs http://localhost:8080/health   # final assertion: 200

docker compose -f deploy/docker-compose.yml down
```

**Verification:** all containers reach healthy; `/health` returns 200; teardown clean.

### T56: Conformance tests against in-process executors

**Steps:**

```sh
# Bring up the stack
docker compose -f deploy/docker-compose.yml up -d
for i in $(seq 1 30); do
    if curl -fs http://localhost:8080/health > /dev/null; then break; fi
    sleep 2
done

# Run conformance against the http-node executor
go run ./core/cmd/rimsky-executor-conformance \
    --endpoint http://localhost:9090 \
    --transport grpc \
    --require-stub-mode

# Run conformance against claude-agent (TypeScript)
go run ./core/cmd/rimsky-executor-conformance \
    --endpoint http://localhost:9091 \
    --transport http \
    --require-stub-mode

docker compose -f deploy/docker-compose.yml down
```

**Verification:** both conformance runs pass.

### T57: TypeScript executor build and tests

**Steps:**

```sh
cd /Users/patrick/Documents/projects/research/verantel/submodules/rimsky/executors/claude-agent
npm install
npm test
npm run build
```

**Verification:** all pass; `dist/` is produced.

### T58: Final `grep` sweep for vocabulary leakage

**Steps:**

```sh
grep -rn 'claim-store-postgres\|inline-jsonb\|held: true\|RestoreVersion\|LockSpec\|RegionLockSpec\|ClaimLockSpec\|ReleaseAction\|ClaimableStore\|ResumableStore\|HasPriorWork\|SupportsRegionLock\|SupportsEmptySelector\|SupportsResume\|SupportsRestore\|SupportsAtomicMulti\|SupportsDiscard\|KeepVersionsMax\|payload_encryption\|async_supports_dynamic_selectors\|commit_atomicity_scope\|actual_action\|delete_won\|first-delete-wins\|last-released-wins' docs/ core/ proto/ test/ deploy/
```

**Verification:** zero hits in active code/docs (occurrences inside historical changelogs or design discussion docs are acceptable; flag any new occurrences inside `core/`, `proto/`, `test/`, `docs/specs/`, `docs/operator-guide.md`, `docs/store-author-guide.md`, `docs/executor-author-guide.md`, `docs/architecture.md`, `docs/protocol.md`, `docs/node-graph-design.md`, `CLAUDE.md`).

### T59: Spec-implementation cross-check

**Steps:**

1. Verify the verb signatures in `core/store/interface.go` match spec §11.5 textually:
```sh
grep -E '^\s*(Open|Commit|Abandon|Delete|Release)\(' core/store/interface.go
```
Should produce 5 method declarations matching spec §11.5.

2. Verify `ClaimSpec` and `NamedLockSpec` field sets match spec §11.3:
```sh
go doc ./core/store ClaimSpec
go doc ./core/store NamedLockSpec
```
`ClaimSpec` should have exactly: `StoreName`, `Selector`, `Intent`, `Alias`. `NamedLockSpec` should have exactly: `Name`.

3. Verify the schema matches spec §12.10 / §12.11:
```sh
grep -E 'lock_kind|claim_id|address|intent|lock_holder_id|actual_action|delete_won' core/migrations/001-initial.sql
```
Expected hits: `lock_kind` (with values `('named', 'region')`), `address`, `intent`, `lock_holder_id`. Forbidden hits in the migration: `claim_id`, `actual_action`, `delete_won`.

4. Verify the inertness annotation per spec §17.2 is in place:
```sh
grep -F '@blessed-invariant 20' core/store/types.go core/attributes/substitution.go
```
Expected: ≥ 1 hit per file.

5. Verify `Capabilities` struct matches spec §11.2 (one field):
```sh
go doc ./core/store Capabilities
```
Should show a single field: `WriteSemantics`.

**Verification:** all five `grep`/`go doc` commands above produce the expected output. Any deviation indicates a spec-implementation drift to fix before declaring the work complete.

---

## Manual checks after completion

(None. All verification is automated. The spec's §3 explicitly specifies "no interactive prompts" and the spec's design surface is fully testable via automated scenarios + the smoke fixture.)
