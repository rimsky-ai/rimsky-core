# Nomenclature Resolution Implementation Plan

**Spec:** `.ok-planner/specs/2026-05-12-nomenclature-resolution-design.md`
**Goal:** Apply all 19 cross-layer nomenclature decisions plus per-concept ride-along renames from the 2026-05-12 audit walkthrough. End-state: one canonical name per concept across code, schema, proto, YAML, binaries, concept docs; migration chain collapsed to one baseline; root module split from `modeling/` into `graph/` + `control/`.
**Architecture:** Spec organizes changes into 9 implementation groups (A-I). This plan executes those groups in dependency order: migration baseline first, then YAML+vocabulary code renames, then schema-residue code sweeps, then proto restructure, then comment/doc sweeps, then ride-alongs, then layer reorganization, then concept-doc mutations, then tension-file moves, then full-stack verification.
**Tech Stack:** Go (3 modules: `foundation/`, `protocols/`, root `github.com/fallguyconsulting/rimsky`; plus `mcp-servers/` module), TypeScript (`executors/claude-agent/`), Protobuf v3, PostgreSQL 14+ + SQLite (modernc.org/sqlite, pure-Go), YAML config (`rimsky.yml`).

---

## Pre-flight notes for the implementer

1. **Read the spec in full** before starting: `.ok-planner/specs/2026-05-12-nomenclature-resolution-design.md`. The spec is the source of truth for every decision; this plan translates it into discrete tasks. Tasks below cite specific spec groups (A through I) for cross-reference.
2. **Wire-format breaks are sanctioned** (pre-v1; no consumer pin). Proto wire shape changes, YAML alias retirements, and migration history collapse all land in this plan.
3. **Dev Postgres requires hard reset** after the migration baseline rebase lands. The reset command is documented in Task A.5 and lands in the CHANGELOG.
4. **Build commands** used throughout:
   - `make build-all` — builds all three Go modules.
   - `make test-all` — runs unit + scenario tests across the three modules (testcontainers spins up Postgres; Docker must be running).
   - `make lint` — golangci-lint with the depguard rules.
   - `make proto-gen` — regenerates proto bindings under `protocols/proto/v1/gen/`.
   - `make tidy` — `go mod tidy` across all modules.
   - `cd executors/claude-agent && npm install && npm test && npm run build` — TS executor.
5. **Citation grammar** in this plan: `code:path::Symbol`, `proto:file.proto::Message`, `table:foo`, `col:foo.bar`, `cfg:foo.bar`, `concept:slug`, `tension:slug`, `invariant:N`, `file:relative/path`, `cmd:make foo`. See `.claude/rules/citation-grammar.md`.
6. **The plan covers the spec end-to-end in a single run.** No checkpoint pauses; no human-in-the-loop reviews; no commits along the way. The user owns commit decisions and runs them when the plan completes.
7. **Concept-doc mutations** are first-class plan tasks per the spec's `## Tensions resolved` marker; they ship in the same run as the code changes that conform to them.

### Key references the implementer should pin to a side buffer

- The new `proto:executor.proto::ExecuteEvent` shape (full Protobuf block, Task E.4 reproduces it).
- The post-rename table inventory (Task A.2 reproduces it).
- The concept rename/drop/add table (Task G.1 reproduces it).

---

## Section A — Migration baseline rebase (spec Group A)

Covers cross-layer #3, absorbing schema portions of #4, #5, #7, #13, #14.

### Task A.1 — Survey current Postgres migration chain

**Files:** read-only.

**Steps:**

1. List the current migration files: `ls foundation/persistence/postgres/migrations/` and read each one in numerical order. Capture in a scratch note (mental or in `.ok-planner/plans/2026-05-12-nomenclature-resolution-notes.md` if it eases tracking) the cumulative CREATE TABLE / ALTER TABLE / CREATE INDEX statements that result in the current applied schema.
2. List the current SQLite migration files: `ls foundation/persistence/sqlite/migrations/` and read each one. Note the SQLite-syntax variations that differ from Postgres.
3. Run `grep -rn 'rimsky_dispatch\|consumer_key\|rimsky_lock_holders\|lock_holder_id\|regions_data\|frame_resolution\|rimsky_claim_handle\b\|rimsky_worker_request\b\|rimsky_lifecycle_idempotency\b' foundation/persistence/` to find any source files referencing legacy names. Keep this list — Sections B, D, and J consume it.

**Verification:** `ls foundation/persistence/postgres/migrations/ | wc -l` returns the count; `ls foundation/persistence/sqlite/migrations/ | wc -l` returns the count. No edits yet.

### Task A.2 — Write the new Postgres baseline migration

**Files:** `foundation/persistence/postgres/migrations/001-baseline.sql` (new); all existing `foundation/persistence/postgres/migrations/NNN-*.sql` files (to be deleted in Task A.4).

**End-state table inventory** (post-rename, post-plural):

| Table | Notes |
|---|---|
| `rimsky_nodes` | (already plural) |
| `rimsky_node_runs` | renamed from `rimsky_worker_request` (#14); plural per #13 |
| `rimsky_claim_handles` | pluralized from `rimsky_claim_handle` (#13); columns include `is_held BOOLEAN`, `worker_request_id` renamed to `node_run_id` |
| `rimsky_claim_holders` | (already plural); FK col `claim_handle_id` (already renamed) |
| `rimsky_frames` | column `mode` renamed to `frame_resolution_mode` (#4) |
| `rimsky_schedules` | (already plural) |
| `rimsky_instances` | column `instance_key` (consumer_key rename history erased) |
| `rimsky_templates` | (already plural) |
| `rimsky_template_tags` | (already plural) |
| `rimsky_events` | (already plural); column `kind` stays |
| `rimsky_node_events` | (already plural) |
| `rimsky_node_attributes` | (already plural) |
| `rimsky_supervisors` | (already plural) |
| `rimsky_lifecycle_idempotencies` | pluralized from `rimsky_lifecycle_idempotency` (#13) |
| `rimsky_blob_orphans` | (already plural) |

**Steps:**

1. Write `foundation/persistence/postgres/migrations/001-baseline.sql` containing CREATE TABLE statements for every table in the inventory above. The schema must exactly reflect the cumulative effect of all existing migrations PLUS the renames in this plan. Specifically:
   - `rimsky_node_runs` replaces `rimsky_worker_request` (table-name rename). Keep all columns: `id`, `instance_id`, `node_id`, `frame_id`, `phase` (values `pending` / `active` / `held` / `completed` / `parked`), `claimed_by`, `last_heartbeat_at`, `claim_token`, `payload`, `last_progress_at`, etc. — confirm exact column set from existing migrations.
   - `rimsky_claim_handles` replaces `rimsky_claim_handle` (plural). FK column `worker_request_id` renames to `node_run_id` with `ON DELETE SET NULL` preserving the post-Phase-5 semantics (`invariant:14`-retired notwithstanding; the `ON DELETE SET NULL` lets held claim handles outlive their node-run's active terminal).
   - `rimsky_frames.frame_resolution_mode` replaces `rimsky_frames.mode`.
   - `rimsky_lifecycle_idempotencies` replaces `rimsky_lifecycle_idempotency`.
   - All FKs referencing renamed tables update.
2. Include all CREATE INDEX, CREATE TYPE (if any enum types), and CREATE FUNCTION (if any plpgsql sweep helpers) from the cumulative existing chain — flattened, no `ALTER`s.
3. Add a top-of-file SQL comment: `-- Baseline migration. Reflects post-2026-05-12 nomenclature resolution. Pre-v1; no rolling-deploy compat. Dev DBs require DROP SCHEMA public CASCADE; CREATE SCHEMA public; before applying.`
4. Reference: the migration script that orchestrates `cmd:rimsky-migrate` uses the numbered-file-glob pattern (`cmd/rimsky-migrate/main.go` and the migration logic in `foundation/persistence/migrations.go`). The single `001-baseline.sql` is the only file in the directory after Task A.4.

**Verification:** `cat foundation/persistence/postgres/migrations/001-baseline.sql | head -50` shows the header comment + first CREATE TABLE. `grep -c 'CREATE TABLE' foundation/persistence/postgres/migrations/001-baseline.sql` returns ≥ 15 (matching the table count above).

### Task A.3 — Write the new SQLite baseline migration

**Files:** `foundation/persistence/sqlite/migrations/001-baseline.sql` (new); all existing files (to be deleted in Task A.4).

**Steps:**

1. Write the SQLite equivalent of `001-baseline.sql` adjusting for SQLite syntax: no `pg_advisory_*`-related helpers (those live in the `AdvisoryLocker` interface impls, not in migrations); use `INTEGER` for IDs where appropriate (or keep `TEXT` UUIDs if that's the current convention — match the existing pattern from the current chain); use `STRICT` table mode where consistent.
2. Mirror the same table set + column set + FKs as Postgres baseline.
3. Add the same top-of-file SQL comment with the dev-reset reminder adapted to SQLite (`rm /var/lib/rimsky/state.db` instead of `DROP SCHEMA`).

**Verification:** `cat foundation/persistence/sqlite/migrations/001-baseline.sql | head -50` shows the header. `grep -c 'CREATE TABLE' foundation/persistence/sqlite/migrations/001-baseline.sql` returns the same count as Postgres.

### Task A.4 — Delete the obsolete migration files

**Files:** `foundation/persistence/postgres/migrations/00[2-9]*.sql`, `foundation/persistence/postgres/migrations/0[1-9][0-9]*.sql` (every file except `001-baseline.sql`); same in `foundation/persistence/sqlite/migrations/`.

**Steps:**

1. Run `ls foundation/persistence/postgres/migrations/` — confirm the list matches what Task A.1 surveyed.
2. Delete every file except `001-baseline.sql` in both Postgres and SQLite migrations directories.
3. Run `ls foundation/persistence/postgres/migrations/` and `ls foundation/persistence/sqlite/migrations/` — each must show exactly one file: `001-baseline.sql`.

**Verification:** `ls foundation/persistence/postgres/migrations/ foundation/persistence/sqlite/migrations/` lists exactly two files total.

### Task A.5 — Add CHANGELOG entry for the breaking migration

**Files:** `CHANGELOG.md`.

**Steps:**

1. Open `CHANGELOG.md`. Locate the `## Unreleased` section (or create one at the top if absent).
2. Add the following bullet under `## Unreleased`:
   ```
   - Migration baseline rebase: the numbered migration chain collapses into a single `001-baseline.sql` reflecting the final post-rename schema. Dev Postgres requires `DROP SCHEMA public CASCADE; CREATE SCHEMA public;` before `cmd:rimsky-migrate` reapplies the baseline. Dev SQLite requires `rm /var/lib/rimsky/state.db`. Pre-v1; no production pin. See spec at `.ok-planner/specs/2026-05-12-nomenclature-resolution-design.md`.
   ```

**Verification:** `head -30 CHANGELOG.md` shows the new entry under `## Unreleased`.

### Task A.6 — Update source files that hard-code legacy table names

**Files:** all files surfaced by Task A.1's grep that reference `rimsky_dispatch`, `rimsky_lock_holders`, `lock_holder_id`, `rimsky_worker_request`, `rimsky_claim_handle` (singular), `rimsky_lifecycle_idempotency` (singular), or `frame_resolution` (column-context) in Go source / test files.

**Steps:**

1. Run `grep -rln 'rimsky_dispatch\|rimsky_lock_holders\|\block_holder_id\b\|rimsky_worker_request\b\|rimsky_claim_handle\b\|rimsky_lifecycle_idempotency\b' foundation/ modeling/ cmd/ test/ stores/ executors/ mcp-servers/` and capture the list.
2. For each file, apply the rename per the post-rename inventory:
   - `rimsky_dispatch` → `rimsky_node_runs` (table) — only in non-test source; tests that name fixtures may have legacy strings; verify whether the test still passes after the SQL rename or needs the string update.
   - `rimsky_worker_request` → `rimsky_node_runs`.
   - `rimsky_lock_holders` → `rimsky_claim_handles`.
   - `lock_holder_id` → `claim_handle_id`.
   - `rimsky_claim_handle` (singular) → `rimsky_claim_handles` (plural). Use word-boundary regex (`\b`) to avoid matching the substring inside `rimsky_claim_handles` plural occurrences that already exist.
   - `rimsky_lifecycle_idempotency` → `rimsky_lifecycle_idempotencies`.
   - `frame_resolution` (in YAML/JSON-Path/column-name contexts; ignore `frame_resolution` in Go-identifier contexts, which Section D handles) → `frame_resolution_mode`.
3. Drop the rename-history comments in:
   - `foundation/persistence/postgres/instances.go` near line 8 (the "renamed from consumer_key" comment).
   - Any `// renamed from / Phase-5 history` comments elsewhere captured by Task A.1's grep.

**Verification:** Re-run the grep from Step 1; no occurrences remain except in `.ok-planner/`, `CHANGELOG.md`, `cmd/rimsky-docs-lint/vocabulary_test.go` (intentional fixture), and `docs/history/` archive files. Run `cmd:make build-all` — passes.

### Task A.7 — Verify the migration runner replays the baseline cleanly

**Files:** (no edits; verification only).

**Steps:**

1. `make build-all` — builds.
2. Run `go test ./foundation/persistence/postgres/... ./foundation/persistence/sqlite/... -count=1`. Testcontainers spins up a fresh Postgres per test; the test harness drops the schema and reapplies the baseline. The Postgres+SQLite schema-equivalence test in `foundation/persistence/conformance/` (or equivalent) exercises the rebased baseline end-to-end.
3. Run `go test ./foundation/persistence/conformance/... -count=1` to confirm the schema-equivalence assertion passes against both drivers.

**Verification:** Both `go test` invocations exit 0. The schema-equivalence test in the conformance package passes (it asserts Postgres and SQLite baselines describe the same logical schema).

---

## Section B — Foundation / protocol vocabulary alignment (spec Group B)

Covers #1 (Store→ClaimProducer alias retirement), #2 (persistence Store→Driver), #7 (delete region comment), #11 (transition-reason / last-outcome relationship docs).

### Task B.1 — Retire `code:foundation/locks/interface.go::Store` type alias

**Files:** `foundation/locks/interface.go`; every file that references `locks.Store` as a type.

**Steps:**

1. Run `grep -rn '\blocks\.Store\b' foundation/ modeling/ cmd/ test/ stores/ executors/ mcp-servers/` and capture the list.
2. For each call site, replace `locks.Store` with `locks.ClaimProducer`. Use Edit tool per file.
3. In `foundation/locks/interface.go`, find the `type Store = ClaimProducer` line (the legacy alias) and delete it entirely (the comment above it as well).

**Verification:** `grep -rn '\blocks\.Store\b' foundation/ modeling/ cmd/ test/ stores/ executors/ mcp-servers/` returns no matches. `cmd:make build-all` passes.

### Task B.2 — Rename `AcquiredLock.Store` field → `.Producer`

**Files:** `foundation/integration/runner.go`; every file referencing `AcquiredLock.Store`.

**Steps:**

1. Run `grep -rn '\.Store\b' foundation/integration/ | grep -i acquiredlock` to capture context; also `grep -rn 'AcquiredLock{' foundation/ modeling/ cmd/ test/` for struct literal sites.
2. In `foundation/integration/runner.go`, edit the `AcquiredLock` struct definition: rename the `Store` field to `Producer`. Update the field-level comment if it mentions "store".
3. Update every call site: `acquiredLock.Store` → `acquiredLock.Producer`; `AcquiredLock{Store: x}` → `AcquiredLock{Producer: x}`.

**Verification:** `grep -rn 'AcquiredLock.*\.Store' foundation/ modeling/ cmd/ test/` returns no matches. `cmd:make build-all` passes.

### Task B.3 — Rename `proto:claim_producer.proto::ClaimSpec.StoreName` → `.ProducerName`

**Files:** `protocols/proto/v1/claim_producer.proto`; Go-side re-export at `foundation/locks/types.go`; every call site.

**Steps:**

1. Edit `protocols/proto/v1/claim_producer.proto`: locate the `ClaimSpec` message, rename the `store_name` field to `producer_name` (keep the field number). Update the leading field-level comment.
2. Run `cmd:make proto-gen` to regenerate the bindings under `protocols/proto/v1/gen/`.
3. Run `grep -rn 'StoreName\b' foundation/ modeling/ cmd/ test/ stores/ executors/` to find call sites — both Go and TS.
4. For each Go call site, rename `StoreName` to `ProducerName`. For each `.proto`-comment reference, update.
5. Update the Go re-export in `foundation/locks/types.go` (if it explicitly re-declares the field name) and the runtime construction site (likely in `foundation/integration/runner_acquire.go` and `foundation/integration/runner_dispatch.go`).
6. For the TS executor, run `grep -rn 'storeName\|StoreName' executors/claude-agent/src/` and update each TS-side reference (the generated bindings will refresh when the executor rebuilds; manual references in handler code update by hand).

**Verification:** `grep -rn 'StoreName\b\|store_name\b' protocols/ foundation/ modeling/ cmd/ test/ stores/ executors/` returns no matches. `cmd:make proto-gen && make build-all` passes. `cd executors/claude-agent && npm run build` passes.

### Task B.4 — Rename `StoreObservability` proto service + Go type → `ClaimProducerObservability`

**Files:** `protocols/proto/v1/store_observability.proto` (rename to `claim_producer_observability.proto`); Go-side handshake code; generated bindings; executor implementations.

**Steps:**

1. Run `git mv protocols/proto/v1/store_observability.proto protocols/proto/v1/claim_producer_observability.proto`.
2. Edit the renamed file: `service StoreObservability` → `service ClaimProducerObservability`. Update the leading service-level comment to reference "claim-producer" instead of "store". Update `option go_package` if it references the old name.
3. Run `cmd:make proto-gen` to regenerate `protocols/proto/v1/gen/store_observability.pb.go` (which should now be `claim_producer_observability.pb.go` — verify the generator's output).
4. Run `grep -rn 'StoreObservability' foundation/ modeling/ cmd/ test/ stores/ executors/ mcp-servers/` and rename every reference to `ClaimProducerObservability`.
5. Update the Go interface declaration in `protocols/storeobservability/` (or wherever the hand-rolled interface lives — verify via grep). Likely the directory itself renames; if so:
   - `git mv protocols/storeobservability protocols/claimproducerobservability` (or the canonical directory name from the build).
6. Update the YAML config-key references where applicable. The `cfg:claim_producers[].protocols` array value for "observability" subscription stays as a string token — verify what the current token is and update if it mentions "store_observability".

**Verification:** `grep -rn 'StoreObservability\|store_observability' protocols/ foundation/ modeling/ cmd/ test/ stores/ executors/ mcp-servers/` returns no matches (excluding the `.ok-planner/` directory). `cmd:make proto-gen && make build-all` passes.

### Task B.5 — Rename `makeStoreHandle` → `makeClaimHandle`

**Files:** `foundation/integration/runner_dispatch.go`; any call sites.

**Steps:**

1. Run `grep -rn 'makeStoreHandle' foundation/ modeling/ cmd/ test/` to capture call sites.
2. Rename the function definition and every call site to `makeClaimHandle`.

**Verification:** `grep -rn 'makeStoreHandle' foundation/ modeling/ cmd/ test/` returns no matches. `cmd:make build-all` passes.

### Task B.6 — Retire `cfg:stores[]` YAML alias

**Files:** `modeling/config/` (the YAML loader); reference configs at `deploy/rimsky.yml`; sample / test configs under `test/` and `deploy/`.

**Steps:**

1. Run `grep -rn '^stores:\|^\s*stores:' modeling/config/ deploy/ test/ examples/` to find places where the YAML loader currently accepts `stores:` as an alias for `claim_producers:`. Capture both the parser logic and the operator-config examples.
2. In `modeling/config/` (or post-#19 `control/config/`), find the parsing logic that maps the legacy `stores:` key to `claim_producers:`. Remove that mapping. The parser should reject `stores:` with a precise error: "unknown config key `stores`; rename to `claim_producers` (the `stores:` alias was retired in the 2026-05-12 nomenclature resolution)".
3. Update `deploy/rimsky.yml` to use `claim_producers:` exclusively (it likely already does; verify).
4. Update test fixtures under `test/` and any sample configs under `examples/` (or wherever they live) to use `claim_producers:` exclusively.

**Verification:** `grep -rn '^stores:\|^\s*stores:' deploy/ test/ examples/` returns no matches in active configs (legacy YAML strings inside test-fixture *strings* are fine if they exist for round-trip-rejection tests; otherwise rename). `cmd:make test-all` — config-parsing tests pass; new test verifies the alias is rejected.

### Task B.7 — Rename `code:foundation/persistence/store.go::Store` umbrella → `Driver`

**Files:** `foundation/persistence/store.go` (rename to `driver.go`); every file referencing `persistence.Store` as the umbrella interface type.

**Steps:**

1. Run `git mv foundation/persistence/store.go foundation/persistence/driver.go`.
2. Edit the renamed file: rename the `Store` interface declaration to `Driver`. Update the leading interface-level comment to reflect the rename. (Note: the file may also contain the per-feature sub-interfaces; those names stay — only the umbrella renames.)
3. Run `grep -rn '\bpersistence\.Store\b' foundation/ modeling/ cmd/ test/ stores/` and rename every reference to `persistence.Driver`.
4. Sweep variable names: `grep -rn 'var \w* persistence\.Store\b\|: persistence\.Store\b\| persistence\.Store{' foundation/ modeling/ cmd/ test/` and rename the receivers and instances similarly. Common patterns: `var s persistence.Store` → `var d persistence.Driver`; `store persistence.Store` parameter → `driver persistence.Driver`. Update parameter names from `store` / `s` to `driver` / `d` for clarity where the param represents the umbrella.

**Verification:** `grep -rn '\bpersistence\.Store\b' foundation/ modeling/ cmd/ test/ stores/` returns no matches. `cmd:make build-all` passes. After Task B.1 + B.7 both land, `grep -rn '\bStore\b' foundation/locks/ foundation/persistence/ foundation/integration/` returns only `stores/` directory references or unrelated mentions in comments — no live type/alias collision.

### Task B.8 — Delete legacy `region` comment in conflict.go

**Files:** `foundation/locks/conflict.go`.

**Steps:**

1. Open `foundation/locks/conflict.go` and locate lines 14-18 (the paragraph citing "v2's per-store RegionsConflict").
2. Delete the entire paragraph (the multi-line comment).

**Verification:** `grep -n -i 'region' foundation/locks/conflict.go` returns no matches. `cmd:make build-all` passes.

### Task B.9 — Add cross-link section to `concept:transition-reason.md`

**Files:** `.ok-planner/design/concepts/transition-reason.md`.

**Steps:**

1. Read `.ok-planner/design/concepts/transition-reason.md` in full.
2. Add a new top-level section `## Relationship to sibling concept` after the existing Boundaries / Invariants sections. Content:
   ```markdown
   ## Relationship to sibling concept

   `concept:transition-reason` is the audit-grade "why did this transition happen" enum carried on every node-state transition. Sibling concept `concept:last-outcome` is the cascade-firing gate enum carried on the row in `col:rimsky_nodes.last_outcome`. The two are complementary, not duplicative — they record different facets of the same transition.

   | transition_reason | typical last_outcome |
   |---|---|
   | `HandlerComplete` + handler resolved `by_changed` | `fresh_changed` or `fresh_unchanged` (depending on executor's `changed` verdict) |
   | `HandlerComplete` + handler resolved `always_propagate` | `fresh_changed` (forced) |
   | `HandlerComplete` + handler resolved `never_propagate` | `fresh_unchanged` (forced) |
   | `OperatorReset` | unchanged from prior run |
   | `Invalidate` (graph-level message) | no write (stale state has no outcome) |
   | `HeartbeatLost` | no write (transition is administrative) |
   | `AppErrorTerminal` (failed) | `failed` |

   See `concept:last-outcome` for the symmetric section on the sibling.
   ```
3. Update the concept's Adjacent list to ensure `last-outcome` is present.

**Verification:** `grep -n 'Relationship to sibling concept' .ok-planner/design/concepts/transition-reason.md` returns one match.

### Task B.10 — Add cross-link section to `concept:last-outcome.md`

**Files:** `.ok-planner/design/concepts/last-outcome.md`.

**Steps:**

1. Read `.ok-planner/design/concepts/last-outcome.md` in full.
2. Add the symmetric `## Relationship to sibling concept` section. Content:
   ```markdown
   ## Relationship to sibling concept

   `concept:last-outcome` is the cascade-firing gate (read by the supervisor's terminal-complete path to decide whether to fire cascade propagation). Sibling concept `concept:transition-reason` is the audit-grade enum carried on every node-state transition.

   The cascade-fire predicate is `last_outcome == fresh_changed`, regardless of `transition_reason`. The two enums describe different facets of the same transition: `transition_reason` records "what kind of transition this was" (HandlerComplete, OperatorReset, Invalidate, etc.); `last_outcome` records "what cascade effect, if any, this transition has."

   See `concept:transition-reason` for the typical pairing table (`HandlerComplete` + handler resolution → outcome mapping).
   ```
3. Ensure `transition-reason` is in the Adjacent list.

**Verification:** `grep -n 'Relationship to sibling concept' .ok-planner/design/concepts/last-outcome.md` returns one match.

### Task B.11 — Add `@concept:` annotations to TransitionReason + last-outcome write sites

**Files:** `foundation/cascade/state.go`; any source file that writes to `last_outcome` (commonly `foundation/integration/terminal_outcome.go`).

**Steps:**

1. Open `foundation/cascade/state.go`. Find the `TransitionReason` type declaration. Add a leading comment block:
   ```go
   // @concept: transition-reason
   //
   // Audit-grade enum carried on every node-state transition. Sibling to
   // `last_outcome` (see `.ok-planner/design/concepts/transition-reason.md`
   // Relationship section for the pairing table). The cascade-fire predicate
   // reads `last_outcome`, not `transition_reason` — the two enums describe
   // different facets of the same transition.
   ```
2. Open `foundation/integration/terminal_outcome.go` (or wherever `resolveLastOutcome` writes the column). At the function declaration, add:
   ```go
   // @concept: last-outcome
   //
   // Writes the cascade-firing gate enum on every terminal. Sibling to
   // `transition_reason` (see `.ok-planner/design/concepts/last-outcome.md`).
   ```

**Verification:** `grep -n '@concept: transition-reason\|@concept: last-outcome' foundation/cascade/state.go foundation/integration/terminal_outcome.go` returns the two new annotations. `cmd:make lint && make build-all` passes.

---

## Section C — YAML cleanup (spec Group C)

Covers #6.

### Task C.1 — Retire `write_semantics:` single-value YAML shortcut

**Files:** `modeling/config/` (YAML loader); test/sample configs.

**Steps:**

1. Run `grep -rn '^\s*write_semantics:' deploy/ test/ examples/ modeling/config/` to capture sites.
2. In the YAML loader, find the parser-side support that accepts `write_semantics: <single value>` as a shortcut for `write_semantics_envelope: [<single value>]`. Remove the shortcut path; the parser must reject single-value form.
3. Update reference and sample YAMLs to use the list form: `write_semantics_envelope: [sync]`.
4. Add a parser-side test asserting rejection of the single-value form with a precise error message.

**Verification:** `grep -rn '^\s*write_semantics:' deploy/ test/ examples/` returns no matches except in test fixtures that intentionally trigger rejection. `cmd:make test-all` — config-test packages pass; the new rejection test passes.

### Task C.2 — Rename `write_semantics_envelope` → `write_semantics_allowed`

**Files:** `protocols/proto/v1/claim_producer.proto`; YAML loader in `modeling/config/`; reference configs; all Go call sites that touch `WriteSemanticsEnvelope`.

**Steps:**

1. Edit `protocols/proto/v1/claim_producer.proto`: in the `Capabilities` message, rename field `write_semantics_envelope` to `write_semantics_allowed`. Keep the field number. Update the field-level comment to reflect the new name.
2. Run `cmd:make proto-gen` to regenerate bindings.
3. Run `grep -rn 'WriteSemanticsEnvelope\|write_semantics_envelope' foundation/ modeling/ cmd/ test/ stores/ executors/ mcp-servers/` to capture call sites.
4. Rename every Go reference: `Capabilities.WriteSemanticsEnvelope` → `Capabilities.WriteSemanticsAllowed`; similarly for any internal types that mirror this.
5. Rename the YAML config key in the loader (currently `write_semantics_envelope:`) to `write_semantics_allowed:`. Update reference YAMLs at `deploy/rimsky.yml` and any sample/test configs.
6. For the TS executor, run `grep -rn 'writeSemanticsEnvelope\|write_semantics_envelope' executors/claude-agent/src/` and update.

**Verification:** `grep -rn 'WriteSemanticsEnvelope\|write_semantics_envelope' protocols/ foundation/ modeling/ cmd/ test/ stores/ executors/ mcp-servers/` returns no matches (excluding `.ok-planner/`). `cmd:make proto-gen && make build-all` passes. `cd executors/claude-agent && npm run build` passes.

### Task C.3 — Update `concept:write-semantics.md` and CLAUDE.md prose

**Files:** `.ok-planner/design/concepts/write-semantics.md`; `CLAUDE.md`.

**Steps:**

1. In `concept:write-semantics.md`: replace all occurrences of `write_semantics_envelope` with `write_semantics_allowed`. Update the "envelope" metaphor reference in the body to remove the metaphor language and use plain-English "allowed values" framing. Keep any historical-context note that mentions the rename in a single Notes entry.
2. In `CLAUDE.md` "Non-obvious gotchas" section: find the bullet referencing `write_semantics_envelope` and update to `write_semantics_allowed`.

**Verification:** `grep -n 'write_semantics_envelope' .ok-planner/design/concepts/write-semantics.md CLAUDE.md` returns no matches.

---

## Section D — Code-side schema-rename residue (spec Group D)

Covers #4 (code/Go for frame_resolution_mode), #5 (code residue), #14 (code-side worker_request → node-run sweep).

### Task D.1 — Rename `FrameMode` Go type → `FrameResolutionMode`

**Files:** `modeling/frame/types.go`; any file referencing `FrameMode`.

**Steps:**

1. Run `grep -rn '\bFrameMode\b' foundation/ modeling/ cmd/ test/` to capture sites.
2. Rename the type declaration and every reference. Common appearances: `var m FrameMode`; `FrameMode("serial_queue")`; struct field types.
3. Rename the helper `LookupFrameMode` (likely in `foundation/persistence/postgres/frames.go`) → `LookupFrameResolutionMode`. Update all call sites.

**Verification:** `grep -rn '\bFrameMode\b\|\bLookupFrameMode\b' foundation/ modeling/ cmd/ test/` returns no matches. `cmd:make build-all` passes.

### Task D.2 — Rename `TemplateSpec.FrameResolution` field → `.FrameResolutionMode`

**Files:** `modeling/node/template.go`; YAML template-author paths; canonical-spec hash logic.

**Steps:**

1. Edit `modeling/node/template.go`: rename the `FrameResolution` field on `TemplateSpec` to `FrameResolutionMode`.
2. Update the YAML-key tag (likely `yaml:"frame_resolution"` or similar struct tag) to `yaml:"frame_resolution_mode"`. Similarly for JSON tags if present.
3. Run `grep -rn '\.FrameResolution\b\|t\.spec.*frame_resolution\|"frame_resolution"' foundation/ modeling/ cmd/ test/` to capture every reference (Go field access, JSON-path reads, string literals).
4. Update every reference. The JCS-canonicalization layer at `modeling/template/canonical/` should read `frame_resolution_mode` after this change — verify the canonicalization output for an example template.

**Verification:** `grep -rn '\.FrameResolution\b\|"frame_resolution"\b' foundation/ modeling/ cmd/ test/` returns no matches (excluding `.ok-planner/`). `cmd:make build-all && make test-all` — template canonicalization tests pass.

### Task D.3 — Sweep YAML template fixtures from `frame_resolution:` → `frame_resolution_mode:`

**Files:** test fixtures, smoke YAMLs, example YAMLs.

**Steps:**

1. Run `grep -rn '^\s*frame_resolution:\b' test/ examples/ deploy/` to capture every template YAML using the legacy key.
2. Edit each to use `frame_resolution_mode:`.

**Verification:** `grep -rn '^\s*frame_resolution:\b' test/ examples/ deploy/` returns no matches.

### Task D.4 — Rename `SweepOrphanedClaims` → `SweepOrphanedNodeRuns`

**Files:** `foundation/integration/conductor.go`; any caller (notably the supervisor's tick loop).

**Steps:**

1. Run `grep -rn '\bSweepOrphanedClaims\b' foundation/ modeling/ cmd/ test/` to capture sites.
2. Rename the function declaration in `foundation/integration/conductor.go`. Update every caller.
3. Update the `OrphanedClaimTimeout` constant's leading doc comment from "the cutoff used by SweepOrphanedClaims" to **"the cutoff used by `SweepOrphanedNodeRuns` and `SweepOrphanedClaimHandles`"** (per spec D.2 — the constant is the shared 5×heartbeat cutoff per `invariant:6`). The constant name itself stays `OrphanedClaimTimeout`.

**Verification:** `grep -rn '\bSweepOrphanedClaims\b' foundation/ modeling/ cmd/ test/` returns no matches. `grep -n 'OrphanedClaimTimeout is' foundation/integration/conductor.go` shows the updated comment referencing both renamed sweeps. `cmd:make build-all && make test-all` passes.

### Task D.5 — Rename `SweepClaimHandles` → `SweepOrphanedClaimHandles`

**Files:** `foundation/integration/orphan_reaper.go`; callers.

**Steps:**

1. Run `grep -rn '\bSweepClaimHandles\b' foundation/ modeling/ cmd/ test/` to capture sites.
2. Rename the function definition and every caller.

**Verification:** `grep -rn '\bSweepClaimHandles\b' foundation/ modeling/ cmd/ test/` returns no matches. `cmd:make build-all && make test-all` passes.

### Task D.6 — Rename `concept:worker-request.md` → `concept:node-run.md`

**Files:** `.ok-planner/design/concepts/worker-request.md` (rename); body content (full rewrite).

**Steps:**

1. Run `git mv .ok-planner/design/concepts/worker-request.md .ok-planner/design/concepts/node-run.md`.
2. Open the renamed file. Rewrite the body so every "worker-request" / "worker_request" reference becomes "node-run" / "node_run" / `rimsky_node_runs`:
   - Definition: "One execution of one node within a frame. Persisted as a row in `table:rimsky_node_runs`."
   - Body explains the `frame ⊃ node-run` hierarchy: `concept:frame` is "one run of the cascade"; `concept:node-run` is the per-node execution within that frame.
   - Reference all column citations to the new table name.
3. Append a Notes entry: `- Renamed from concept:worker-request per spec:2026-05-12-nomenclature-resolution (audit cross-layer #14).`

**Verification:** `test -f .ok-planner/design/concepts/node-run.md` and `! test -f .ok-planner/design/concepts/worker-request.md`. `grep -i 'worker.request\|worker_request' .ok-planner/design/concepts/node-run.md` returns no matches.

### Task D.7 — Sweep Go variable / function names that include `workerRequest`

**Files:** all Go source under `foundation/`, `modeling/`, `cmd/`, `test/`.

**Steps:**

1. Run `grep -rn '\bworkerRequest\b\|\bWorkerRequest\b\|\bworker_request_id\b' foundation/ modeling/ cmd/ test/` to capture call sites. (The proto field `dispatch_id` is intentionally left unchanged per spec D.2 last bullet; the comment on it updates separately in Task D.10.)
2. Rename variables and functions:
   - `workerRequest` (lowerCamel local) → `nodeRun`.
   - `WorkerRequest` (struct or exported type name; e.g., `WorkerRequestRow`) → `NodeRunRow`.
   - `workerRequestID` → `nodeRunID`.
   - `worker_request_id` (in SQL-parameter-name positions in Go strings) → `node_run_id`.
3. Rename interface methods on `persistence.Driver` (from Task B.7's renamed interface) that include `WorkerRequest` in the name. Common examples: `ClaimWorkerRequest` → `ClaimNodeRun`, `GetWorkerRequest` → `GetNodeRun`, `ListWorkerRequests` → `ListNodeRuns`. Update both Postgres and SQLite implementations.

**Verification:** `grep -rn '\bworkerRequest\b\|\bWorkerRequest\b\|worker_request' foundation/ modeling/ cmd/ test/` returns no matches (excluding `.ok-planner/`, `CHANGELOG.md`, and historic docs). `cmd:make build-all && make test-all` passes.

### Task D.8 — Rename HTTP route `/dispatches` → `/node-runs`

**Files:** `modeling/controlapi/` (handler registration); any handler file with `/dispatches`; tests; smoke fixtures.

**Steps:**

1. Run `grep -rn '"/dispatches"\|"/dispatches/' modeling/ cmd/ test/ docs/` to capture sites.
2. Update the route registration in the control-api handler (likely in `modeling/controlapi/routes.go` or similar) from `/dispatches` to `/node-runs`.
3. Update any handler function names that include `dispatches` (`handleListDispatches` → `handleListNodeRuns`, etc.).
4. Update test cases that hit the route. Update smoke fixture (`test/smoke/`).
5. Update CLI subcommand if `rimsky-cli` has a `dispatches` subcommand: rename to `node-runs`.

**Verification:** `grep -rn '"/dispatches"\|"/dispatches/' modeling/ cmd/ test/ docs/` returns no matches. `cmd:make build-all && make test-all` passes.

### Task D.9 — Update `OrphanedClaimTimeout` cross-references and `invariant:6` citation

**Files:** `foundation/integration/conductor.go`; any doc that cites the cutoff.

**Steps:**

1. Already covered in D.4. This task verifies coverage: `grep -n 'OrphanedClaimTimeout' foundation/integration/ test/` lists every site and each references it correctly post-rename.
2. Verify the `@blessed-invariant 6` annotation site (likely in `conductor.go` or `orphan_reaper.go`) still cites the cutoff correctly. The invariant's text in `CLAUDE.md` stays correct (cutoff is `5 × heartbeat_interval` applied to both reapers) — no edit needed.

**Verification:** `cmd:make build-all` passes. No grep needed beyond D.4's verification.

### Task D.10 — Update `proto:executor.proto::ExecuteRequest.dispatch_id` doc comment

**Files:** `protocols/proto/v1/executor.proto`.

**Steps:**

1. Open `protocols/proto/v1/executor.proto`. Find the comment on `ExecuteRequest.dispatch_id` (currently mentions "rimsky_worker_request post-Phase-5 consolidation").
2. Update the comment to reference `rimsky_node_runs` instead. The wire field name `dispatch_id` stays unchanged (per spec D.2 — preserved for executor-observability protocol correlation continuity).

**Verification:** `grep -n 'dispatch_id' protocols/proto/v1/executor.proto` shows the field with the updated comment; no `rimsky_worker_request` mention nearby.

### Task D.11 — Update `.claude/rules/rules.md` path references

**Files:** `.claude/rules/rules.md`.

**Steps:**

1. Open `.claude/rules/rules.md`.
2. Find any references to pre-Phase-5 paths like `core/queue/...`, `core/scheduler/...`, `core/storage/...`. Update to current canonical paths:
   - `core/queue/...` → `foundation/persistence/postgres/queue.go` or `foundation/integration/...` depending on context.
   - `core/scheduler/...` → `modeling/scheduler/...` (until Section H renames to `graph/scheduler/`).
   - `core/storage/...` → `foundation/persistence/...`.

**Verification:** `grep -n 'core/queue\|core/scheduler\|core/storage' .claude/rules/rules.md` returns no matches.

---

## Section E — Proto restructure (spec Group E)

Covers #8 (NodeExecutor → Executor service), #9 (ExecuteEvent + outcome oneof + Snooze + lifecycle 4→3), #12 (drop legacy fallback), #15 (Capabilities harmonization).

### Task E.1 — Rename proto service `NodeExecutor` → `Executor`

**Files:** `protocols/proto/v1/executor.proto`.

**Steps:**

1. In `protocols/proto/v1/executor.proto`, rename `service NodeExecutor` → `service Executor`. Update the leading service-level doc comment from "NodeExecutor is the protocol..." to "Executor is the protocol...". Keep the historical citation to `docs/history/2026-04-27-stores-redesign-v2-design.md §12`.
2. Run `cmd:make proto-gen` to regenerate `protocols/proto/v1/gen/executor.pb.go` and `executor_grpc.pb.go`. The generator emits `ExecutorServer` / `ExecutorClient` (previously `NodeExecutorServer` / `NodeExecutorClient`).

**Verification:** `grep -n 'NodeExecutor' protocols/proto/v1/executor.proto` returns no matches. `cmd:make proto-gen` succeeds.

### Task E.2 — Sweep Go-side references to `NodeExecutor`

**Files:** every Go file under `foundation/`, `modeling/`, `cmd/`, `test/`, `executors/`, `mcp-servers/` that references the proto type names `NodeExecutorServer`, `NodeExecutorClient`, `RegisterNodeExecutorServer`, etc.

**Steps:**

1. Run `grep -rn 'NodeExecutorServer\|NodeExecutorClient\|RegisterNodeExecutorServer\|NodeExecutor_ExecuteServer\|NodeExecutor_ExecuteClient' foundation/ modeling/ cmd/ test/ executors/ mcp-servers/` to capture sites.
2. Update each: drop the `Node` prefix. E.g., `genv1.RegisterNodeExecutorServer(s, h)` → `genv1.RegisterExecutorServer(s, h)`.
3. Verify the Go interface at `protocols/executor/executor.go::Executor` is untouched — it lives in a different package than the codegen `ExecutorServer`, so no collision. (The package import path stays `protocols/executor/`.)

**Verification:** `grep -rn 'NodeExecutorServer\|NodeExecutorClient\|RegisterNodeExecutorServer' foundation/ modeling/ cmd/ test/ executors/ mcp-servers/` returns no matches. `cmd:make build-all` passes.

### Task E.3 — Sweep TS-side references to `NodeExecutor`

**Files:** `executors/claude-agent/src/`.

**Steps:**

1. Run `grep -rn 'NodeExecutor' executors/claude-agent/src/` to capture sites.
2. Update each. The TS gRPC binding library likely emits class names like `NodeExecutorService`; after regenerating the TS bindings from the renamed proto, those become `ExecutorService`. Update hand-written code that imports / extends them.
3. Refresh TS proto bindings: follow the TS-side proto-regeneration command (commonly `npm run proto-gen` in the executor's package.json or `make proto-gen` if it's wired at the top level — verify which).

**Verification:** `grep -rn 'NodeExecutor' executors/claude-agent/src/` returns no matches. `cd executors/claude-agent && npm install && npm test && npm run build` passes.

### Task E.4 — Restructure `executor.proto::ExecuteEvent` to channel-mechanics + outcome oneof

**Files:** `protocols/proto/v1/executor.proto`.

**The new proto block** (replaces the current `ExecuteEvent` + per-terminal messages):

```protobuf
message ExecuteEvent {
  oneof event {
    Heartbeat   heartbeat   = 1;
    NamedEvent  named_event = 2;
    StreamClose stream_close = 3;
  }
}

message StreamClose {
  oneof outcome {
    Success            success      = 1;
    Error              error        = 2;
    Snooze             snooze       = 3;
    AwaitAsyncCallback await_async  = 4;
  }
}

message Success {
  bool changed = 1;
  string change_summary = 2;
  google.protobuf.Struct attributes_delta = 3;
}

message Error {
  string error_class = 1;
  google.protobuf.Struct payload = 2;
}

message Snooze {
  string reason = 1;
  bytes payload = 2;
  google.protobuf.Timestamp resume_at = 3;
  string session_token = 4;
}

message AwaitAsyncCallback {
  string async_ack_id = 1;
  int64 expected_completion_ms = 2;
}

message AsyncCallbackBody {
  // Optional NamedEvent stream replayed before the outcome verdict.
  // Field number 1 stays reserved for events; oneof outcome fields
  // start at 2 to keep events numerically first.
  repeated NamedEvent events = 1;
  oneof outcome {
    Success success = 2;
    Error   error   = 3;
    Snooze  snooze  = 4;
    // No AwaitAsyncCallback — webhook is the second half; can't chain.
  }
}
```

**Steps:**

1. Open `protocols/proto/v1/executor.proto`.
2. Delete the current `ExecuteEvent` message body, the `AsyncCallbackBody`, and the per-terminal message types (`Complete`, `Blocked`, `Errored`, `AsyncAccepted`, `ParkRequested`).
3. Insert the new proto block above (the seven messages: `ExecuteEvent`, `StreamClose`, `Success`, `Error`, `Snooze`, `AwaitAsyncCallback`, `AsyncCallbackBody`).
4. Update the `Execute` RPC's leading doc comment to reflect the new event-stream contract: "The response stream carries zero or more `Heartbeat` events, zero or more `NamedEvent` records, and exactly ONE `StreamClose` event; the executor MUST close the stream immediately after the `StreamClose` event."
5. The `ResumeContext`, `Heartbeat`, `NamedEvent`, **and `StoreHandle`** messages stay unchanged. `StoreHandle` is the per-store reference handed to the executor at dispatch (carried on `ExecuteRequest.stores`); its `handle` field's `Store` substring is the bundled-services-layer colloquial usage, which is layer-appropriate per spec B.1's "keep at bundled-services layer" decision. The proto's `StoreHandle` message stays as the wire-level name — sweep its leading doc comment if it references retired vocabulary, but the message name itself doesn't rename.
6. The `Execute` RPC signature stays `rpc Execute(ExecuteRequest) returns (stream ExecuteEvent)`.
7. `proto:executor.proto::ExecuteRequest` itself stays unchanged at this stage (D.10 already updated its `dispatch_id` field comment).
8. Run `cmd:make proto-gen` to regenerate bindings.

**Verification:** `grep -n '^message' protocols/proto/v1/executor.proto` lists the new message names plus the unchanged `ExecuteRequest`, `ResumeContext`, `StoreHandle`, `Heartbeat`, `NamedEvent`. `cmd:make proto-gen` succeeds.

### Task E.5 — Update Go supervisor to consume the new `ExecuteEvent` shape

**Files:** `foundation/integration/` (supervisor dispatch + terminal handling).

**Steps:**

1. Run `grep -rn 'Complete{\|Blocked{\|Errored{\|AsyncAccepted{\|ParkRequested{' foundation/ modeling/ cmd/ test/` to find every construction or type-switch over the legacy message types.
2. Update every match. Patterns to apply:
   - `ev.GetComplete()` → `ev.GetStreamClose().GetSuccess()`. Type-switch on `genv1.StreamClose_Success`.
   - `ev.GetBlocked()` and `ev.GetErrored()` → `ev.GetStreamClose().GetError()`. Type-switch on `genv1.StreamClose_Error`. The supervisor synthesizes the executor-blocked case from `Error{error_class: "executor_blocked"}` (preserving today's default mapping).
   - `ev.GetAsyncAccepted()` → `ev.GetStreamClose().GetAwaitAsync()`. Type-switch on `genv1.StreamClose_AwaitAsync_`.
   - `ev.GetParkRequested()` → `ev.GetStreamClose().GetSnooze()`. Type-switch on `genv1.StreamClose_Snooze_`.
3. Update the supervisor's terminal-dispatch code: the entry point that processes a terminal event now uses one outcome-oneof discriminator. The unified `ResolveClaimHandleTerminal` engine still drives the verb-fire-and-delete sequence; only the wire-event shape changes.
4. `foundation/integration/runner_terminal.go::applyTerminal` and related functions update their event-extraction logic.

**Verification:** `grep -rn 'GetComplete\|GetBlocked\|GetErrored\|GetAsyncAccepted\|GetParkRequested' foundation/ modeling/ cmd/ test/` returns no matches. `cmd:make build-all` passes.

### Task E.6 — Drop the legacy `{type: ...}` async-callback fallback parser

**Files:** `foundation/integration/` (async-callback HTTP handler — likely `callback_server.go` or named similar); `protocols/proto/v1/executor.proto` (comment was already updated by E.4).

**Steps:**

1. Run `grep -rn '"type":\s*"complete"\|"type":\s*"blocked"\|"type":\s*"errored"' foundation/ executors/' to find the legacy-fallback parse logic.
2. Locate the chi HTTP handler that parses `AsyncCallbackBody`. Find the fallback-on-parse-error code path that accepts the legacy `{type: "..."}` shape. Delete that code path.
3. Replace with a precise error response when the body fails to parse as `AsyncCallbackBody`: respond with HTTP 400 and a body `{"error": "expected AsyncCallbackBody; outcome oneof must be set"}`.
4. Update the existing test in `executors/claude-agent/src/server.test.ts` that exercises the legacy shape to instead test that the legacy shape is rejected with the precise error. Add a new positive test verifying the new oneof shape is accepted.

**Verification:** `grep -rn '"type":\s*"complete"\|"type":\s*"blocked"\|"type":\s*"errored"' foundation/ executors/` returns no matches in non-test code; test-fixtures-as-strings used to verify rejection are acceptable. `cd executors/claude-agent && npm test` passes the new tests.

### Task E.7 — Update executor implementations (`http-node`, `stub`, `claude-agent`) for the new shape

**Files:** `executors/http-node/`, `executors/stub/`, `executors/claude-agent/`.

**Steps:**

1. For each executor: find every place it constructs a terminal event (commonly in the handler that wraps the work logic).
2. Update to construct `StreamClose` with the appropriate outcome variant:
   - Successful completion: `StreamClose{outcome: Success{changed: true, change_summary: "...", attributes_delta: <Struct>}}`.
   - Error: `StreamClose{outcome: Error{error_class: "...", payload: <Struct>}}`.
   - Snooze (formerly park): `StreamClose{outcome: Snooze{reason: "...", payload: <bytes>, resume_at: <Timestamp>, session_token: "..."}}`.
   - Await async callback: `StreamClose{outcome: AwaitAsyncCallback{async_ack_id: "...", expected_completion_ms: <int>}}`.
3. For executors that previously emitted `Blocked`: emit `Error{error_class: "executor_blocked", payload: <previous Blocked.context>}` instead. Keep the operator-side default-mapping behavior intact.
4. The TS claude-agent's `AsyncCallbackBody`-POST helper updates to send the new shape: `{events: [...], outcome: {success: {...}} | {error: {...}} | {snooze: {...}}}`.

**Verification:** Run unit tests in each executor:
- `go test ./executors/stub/... && go test ./executors/http-node/...` passes.
- `cd executors/claude-agent && npm test && npm run build` passes.

### Task E.8 — Update conformance fixture for the new event shape

**Files:** `cmd/rimsky-executor-conformance/` (will rename to `rimsky-executor-conformance` in Section I).

**Steps:**

1. Run `grep -rn '\bComplete\b\|\bBlocked\b\|\bErrored\b\|\bAsyncAccepted\b\|\bParkRequested\b' cmd/rimsky-executor-conformance/` to capture sites.
2. Update the conformance test sequences: assertions about emitted events now expect `StreamClose{outcome: ...}` shape; rejection tests use the new shape.

**Verification:** `go build ./cmd/rimsky-executor-conformance/...` passes (or `rimsky-executor-conformance` after Section I).

### Task E.9 — Collapse `applyTerminalBlockedOrErrored` → `applyTerminalError`

**Files:** `foundation/integration/runner_terminal_handlers.go` (or whichever file holds the function).

**Steps:**

1. Locate `applyTerminalBlockedOrErrored` (use grep if needed: `grep -rn 'applyTerminalBlockedOrErrored' foundation/`).
2. Rename to `applyTerminalError`. Update every caller.
3. Simplify the function body: there is no longer a blocked-vs-errored fork; every error flows through one path. Delete the dead branch that handled `Blocked` separately. The `error_types` policy is the sole discriminator (by `error_class`).

**Verification:** `grep -rn 'applyTerminalBlockedOrErrored' foundation/ modeling/ cmd/ test/` returns no matches. `cmd:make build-all && make test-all` passes — relevant tests under `test/scenarios/` exercising error-handling paths still pass.

### Task E.10 — Drop `on_executor_blocked` lifecycle-handler slot

**Files:** `modeling/node/template.go` (or wherever lifecycle-handler slots are declared); template validation logic; example templates and tests.

**Steps:**

1. Run `grep -rn 'on_executor_blocked\b' foundation/ modeling/ cmd/ test/ examples/ executors/' to capture sites.
2. Remove the `on_executor_blocked` slot from the template-author surface:
   - Delete the field from the struct that holds lifecycle handlers (likely `LifecycleHandlers` struct in `modeling/node/template.go`).
   - Update the template-validation logic that allow-lists handler slots: the allowed set is `{on_acquire_unavailable, on_executor_complete, on_executor_errored}` plus the `on_event` map.
   - Update any error message that says "handler must be one of 4 slots" to "3 slots plus `on_event` map".
3. Sweep template-definition tests and example YAMLs that declared `on_executor_blocked`. Migrate to `on_executor_errored` with appropriate `error_types` mapping (executor's previous `Blocked` emission becomes `Error{error_class: "executor_blocked"}`; the template's `on_executor_errored` handler covers it).

**Verification:** `grep -rn 'on_executor_blocked\b' foundation/ modeling/ cmd/ test/ examples/ executors/` returns no matches. `cmd:make build-all && make test-all` passes.

### Task E.11 — Rename `proto:executor_observability.proto::ExecutorObservability.GetCapabilities` → `.Capabilities`

**Files:** `protocols/proto/v1/executor_observability.proto`; generated bindings; Go-side handshake code; TS executor.

**Steps:**

1. Edit `protocols/proto/v1/executor_observability.proto`: rename the RPC `GetCapabilities` to `Capabilities`. Update the RPC's doc comment.
2. Run `cmd:make proto-gen`.
3. Run `grep -rn 'GetCapabilities\b' foundation/ modeling/ cmd/ test/ executors/ mcp-servers/' to capture sites.
4. Update each:
   - Go: `client.GetCapabilities(ctx, req)` → `client.Capabilities(ctx, req)`.
   - Server handlers: method receivers named `GetCapabilities` rename to `Capabilities`.
   - TS: corresponding method calls update after refresh of TS proto bindings.
5. The `code:modeling/observability/handshake.go` discovery-cache populator updates its call site to the new method name.

**Verification:** `grep -rn 'GetCapabilities\b' foundation/ modeling/ cmd/ test/ executors/ mcp-servers/` returns no matches. `cmd:make proto-gen && make build-all` passes. `cd executors/claude-agent && npm test && npm run build` passes.

### Task E.12 — Update `ParkRequested` proto-citation in `concept:parked-state.md`

**Files:** `.ok-planner/design/concepts/parked-state.md`.

**Steps:**

1. Open `.ok-planner/design/concepts/parked-state.md`. Find references to `proto:executor.proto::ParkRequested`. Replace with `proto:executor.proto::Snooze`. Update any prose that says "the executor emits `ParkRequested`" to "the executor emits `proto:executor.proto::Snooze` (formerly `ParkRequested`)".
2. Note: per spec E.2, the state-machine value `'parked'` and the concept slug `parked-state` STAY. Only the proto-event citation updates. The function names `SweepParkedNodes`, `wakeParkedNode`, `runner_terminal_park.go` stay unchanged.

**Verification:** `grep -n 'ParkRequested\|Snooze' .ok-planner/design/concepts/parked-state.md` shows the new reference; no bare `ParkRequested` remains.

---

## Section F — Cascade vocabulary (spec Group F)

Covers #10.

### Task F.1 — Rewrite `concept:cascade.md` with three-word vocabulary

**Files:** `.ok-planner/design/concepts/cascade.md`.

**Steps:**

1. Read the current `concept:cascade.md` in full.
2. Rewrite the body section so the "two walks" framing is replaced with "one walk; two node-level behaviors (propagation, fallthrough)":
   - Define **walk**: scheduler-tick-driven traversal of the graph (topology-ordered). The mechanism.
   - Define **propagation**: cascade-of-stale on `fresh_changed`; mark dependents stale and recurse. Driven by `code:foundation/integration/cascade_invalidate.go::InvalidateNode` (handler for `concept:invalidate`).
   - Define **fallthrough**: no-dispatch fresh-roll on `pure_cascade`; roll fresh state forward without running the node. Detected by `code:foundation/integration/cascade_recalculate.go::RecalculateNode`; executed by the scheduler's pure-cascade sweep.
3. Append a Notes entry: `- Three-word vocabulary (walk / propagation / fallthrough) introduced per spec:2026-05-12-nomenclature-resolution (audit cross-layer #10). Resolves tension:cascade-walks-overloaded.`
4. Sweep the Adjacent list: ensure `concept:invalidate`, `concept:last-outcome`, `concept:transition-reason`, `concept:frame` are present.

**Verification:** `grep -n 'walk\|propagation\|fallthrough' .ok-planner/design/concepts/cascade.md` shows the three-word vocabulary used throughout the body.

### Task F.2 — Refresh doc comments in `cascade_invalidate.go` and `cascade_recalculate.go`

**Files:** `foundation/integration/cascade_invalidate.go`, `foundation/integration/cascade_recalculate.go`.

**Steps:**

1. Open `foundation/integration/cascade_invalidate.go`. Update the leading doc comment and any in-line comments that say "cascade walk" ambiguously. Use precise vocabulary: this file handles **propagation** (cascade-of-stale on `fresh_changed`).
2. Open `foundation/integration/cascade_recalculate.go`. Update doc comments similarly. This file handles per-node **fallthrough** detection (pure_cascade fresh-roll); the per-node logic detects `all-deps-fresh + no-executor`; the scheduler's pure-cascade sweep executes the fresh-roll.
3. Add `@concept: cascade` annotations at both file-level package-doc comments (if not present).
4. Verify file names stay: do NOT rename to `cascade_propagation.go` / `cascade_fallthrough.go` (per spec F).

**Verification:** `grep -n 'propagation\|fallthrough' foundation/integration/cascade_invalidate.go foundation/integration/cascade_recalculate.go` shows the vocabulary in comments. `grep -n '@concept: cascade' foundation/integration/cascade_invalidate.go foundation/integration/cascade_recalculate.go` returns annotations. `cmd:make build-all && make lint` passes.

### Task F.3 — Sweep ambiguous "cascade" mentions in adjacent concept docs

**Files:** `.ok-planner/design/concepts/invalidate.md`, `.ok-planner/design/concepts/last-outcome.md`, `.ok-planner/design/concepts/transition-reason.md`.

**Steps:**

1. For each file, run `grep -n 'cascade walk\|cascade-walk\|two walks' <file>` to capture ambiguous usages.
2. For each match, replace with the precise vocabulary from F.1 (walk / propagation / fallthrough).

**Verification:** `grep -rn 'cascade walk\|cascade-walk\|two walks' .ok-planner/design/concepts/invalidate.md .ok-planner/design/concepts/last-outcome.md .ok-planner/design/concepts/transition-reason.md` returns no matches.

---

## Section G — Concept-doc reorganization (spec Group G)

Covers #16 (drop held-claim; fold into claim-handle), #17 (opacity → inertness; reinforce userdata), #18 (peer → service; promote service).

### Task G.1 — Fold `concept:held-claim.md` content into `concept:claim-handle.md`; delete held-claim

**Files:** `.ok-planner/design/concepts/claim-handle.md` (expand); `.ok-planner/design/concepts/held-claim.md` (delete).

**Steps:**

1. Read `.ok-planner/design/concepts/held-claim.md` in full. Capture the content that needs to migrate.
2. Open `.ok-planner/design/concepts/claim-handle.md`. Add two new subsections:
   - **`### Held variant`** — content from `held-claim.md` covering: `col:rimsky_claim_handles.is_held = TRUE` marker; what the column means; per-member state tracked in `table:rimsky_claim_holders`; FK column `claim_handle_id` on the holders table.
   - **`### Authoring: held vs unheld`** — content from `held-claim.md` covering: how a template declares inheritors → the claim becomes held through them; example YAML if any.
3. Update `concept:claim-handle.md` Adjacent list: ensure `auto-terminal`, `claim`, `claim-holder` are present. Remove any reference to `held-claim` (which is dropped).
4. Append a Notes entry to `claim-handle.md`: `- Held-variant content folded in from former concept:held-claim per spec:2026-05-12-nomenclature-resolution (audit cross-layer #16).`
5. Delete `.ok-planner/design/concepts/held-claim.md`: `rm .ok-planner/design/concepts/held-claim.md`.

**Verification:** `! test -f .ok-planner/design/concepts/held-claim.md`; `grep -n '### Held variant\|### Authoring: held vs unheld' .ok-planner/design/concepts/claim-handle.md` returns both new subsection headers.

### Task G.2 — Rename `concept:opacity.md` → `concept:inertness.md`; rewrite body

**Files:** `.ok-planner/design/concepts/opacity.md` → `inertness.md`.

**Steps:**

1. `git mv .ok-planner/design/concepts/opacity.md .ok-planner/design/concepts/inertness.md`.
2. Open the renamed file. Rewrite the body to describe two sub-disciplines:
   - **Byte-opaque inertness** — rimsky never traverses (claim scope/address/payload, blob bytes).
   - **Structural inertness** — rimsky may traverse for transport mechanics (e.g., walkPath substitution, event-log persistence) but doesn't inspect or act on values (userdata, attribute values, named-event payloads, error payloads).
3. The Invariants section lists the five inert streams: `userdata`, `claim scope`, `claim payload`, `blob bytes`, `named-event payload`. (Plus the new `Error.payload` from spec Group E.2.)
4. Append a Notes entry: `- Renamed from concept:opacity per spec:2026-05-12-nomenclature-resolution (audit cross-layer #17). Adopts two-sub-discipline framing.`
5. Update Adjacent list: `userdata`, `claim`, `blob-backend`, `named-event`, `attribute`.

**Verification:** `test -f .ok-planner/design/concepts/inertness.md && ! test -f .ok-planner/design/concepts/opacity.md`. `grep -n 'Byte-opaque inertness\|Structural inertness' .ok-planner/design/concepts/inertness.md` returns both subsections.

### Task G.3 — Reword `@blessed-invariant 11` in source

**Files:** `modeling/attribute/substitution.go` (where invariant 11 is annotated); `CLAUDE.md` (which lists the invariants).

**Steps:**

1. Run `grep -rn '@blessed-invariant 11\|Userdata is opaque' foundation/ modeling/ cmd/ test/ executors/ docs/ CLAUDE.md` to find the invariant text.
2. Replace "Userdata is opaque to rimsky" with "Userdata is inert in Rimsky" at every site (source `@blessed-invariant 11` annotations, `CLAUDE.md` invariants list, public docs `docs/concepts/`-style citations).
3. Verify the discipline note that follows the headline stays unchanged — only the wording adjusts.

**Verification:** `grep -rn 'opaque' foundation/ modeling/ cmd/ test/' shows no `Userdata is opaque` matches. The phrase "Userdata is inert" appears wherever invariant 11 is cited. `cmd:make build-all && make lint` passes.

### Task G.4 — Reinforce `concept:userdata.md` Purpose

**Files:** `.ok-planner/design/concepts/userdata.md`.

**Steps:**

1. Open `.ok-planner/design/concepts/userdata.md`.
2. Find the Purpose section (or add one if absent). Reinforce it with explicit framing:
   ```markdown
   ## Purpose

   Escape-hatch for executor-specific config that rimsky should not need to learn about. Three primary uses in practice:

   1. **Synthetic-blocker scenarios** — executor configures internal sleep / wait state via opaque per-node tuning.
   2. **Per-run trace artifacts** — caller threads correlation IDs, span contexts, or audit hooks the executor consumes.
   3. **Ad-hoc tuning** — per-node knobs the executor recognizes that rimsky does not (e.g., retry budgets, output format flags).

   Per-instance overrides via `code:rimsky_instances.userdata_overrides` extend this with operator-level customization at instance-creation time (see `route:POST /instances`).
   ```
3. Add a cross-link to `concept:inertness` (the umbrella for the inertness discipline that applies here).
4. Verify the Boundaries section says explicitly: "Rimsky never substitutes, validates, or otherwise interprets userdata. The per-instance overrides merge is the only structural traversal of userdata content (handled by `code:modeling/shared/jsonmerge.go::DeepMergeJSON`)."
5. Update Adjacent: add `inertness`.

**Verification:** `grep -n 'Synthetic-blocker\|trace artifacts\|inertness' .ok-planner/design/concepts/userdata.md` returns matches in the Purpose and Adjacent.

### Task G.5 — Create `concept:service.md` (new umbrella concept)

**Files:** `.ok-planner/design/concepts/service.md` (new).

**Steps:**

1. Write a new file at `.ok-planner/design/concepts/service.md` with the following content:

```markdown
# concept:service

**Aliases:** peer (legacy), peer service (legacy)

## Definition

An out-of-process gRPC binary that implements one or more rimsky service protocols and is orchestrated by rimsky.

## Purpose

Extensibility (third-party implementations are first-class) and modularity (reference implementations are decoupled from rimsky core). A service is the orchestrated-resource side of rimsky's runtime; rimsky itself runs the supervisor / scheduler / control-api binaries that orchestrate services.

## Boundaries

The specific service protocols are sibling concepts: `concept:executor`, `concept:claim-producer`, `concept:lifecycle-subscriber`, `concept:blob-backend`. Orchestration mechanics (dispatch, acquisition, supervisor coordination, terminal resolution) live in their own concepts: `concept:supervisor`, `concept:terminal-resolution`, `concept:auto-terminal`, `concept:orphan-reaper`.

`concept:service` owns:

- How a binary declares its protocol membership in `cfg:rimsky.yml` (the `protocols:` list per service entry).
- The `Capabilities` startup handshake (one RPC per protocol; see `concept:observability` for the discovery-cache that consumes them).
- Conformance-validation entry points (`code:cmd/rimsky-executor-conformance`, `code:cmd/rimsky-claim-producer-conformance`, `code:cmd/rimsky-blob-backend-conformance`).
- The multi-protocol composition pattern: a binary implementing N rimsky protocols uses N handler types, one per protocol interface. Method-name collisions across protocols (e.g., both `ClaimProducer.Capabilities()` and `ExecutorObservability.Capabilities()`) are resolved at the composition site, not by interface unification. Each handler implements one interface; the binary registers each separately at the gRPC server.

## Invariants

- Services are declared in `cfg:rimsky.yml` with an explicit `protocols: [...]` list per service.
- Protocol membership is advertised at startup via the per-protocol `Capabilities` RPC.
- Per-protocol conformance binaries validate compliance.
- Multi-protocol binaries use distinct Go handler types per protocol; no shared `CapabilitiesProvider` Go interface (per spec:2026-05-12-nomenclature-resolution E.4 — the response types are protocol-specific and the downstream code is already protocol-specific).

## Adjacent

- `concept:executor`
- `concept:claim-producer`
- `concept:lifecycle-subscriber`
- `concept:blob-backend`
- `concept:rimsky-yml`
- `concept:conformance`
- `concept:observability`
- `concept:discovery-cache`

## Notes

- Promoted as new umbrella concept per spec:2026-05-12-nomenclature-resolution (audit cross-layer #18). Replaces the colloquial "peer" framing, which implied peer-to-peer equivalence that doesn't match rimsky's orchestrator-to-orchestrated relationship.
```

**Verification:** `test -f .ok-planner/design/concepts/service.md && wc -l .ok-planner/design/concepts/service.md` returns a positive line count.

### Task G.6 — Sweep "peer" → "service" in CLAUDE.md

**Files:** `CLAUDE.md`.

**Steps:**

1. Run `grep -n '\bpeer\b\|\bPeer\b' CLAUDE.md` to capture every occurrence and surrounding context.
2. For each match, replace with "service" / "Service" — every occurrence of the noun in `CLAUDE.md` refers to the orchestrated-binary sense (rimsky's prose does not use "peer" for peer-to-peer networking).
3. After replacement, sweep the prose around each replacement so adjacent grammar reads naturally (e.g., "peer-reachable" → "service-reachable", "peer binaries" → "service binaries").

**Verification:** `grep -c '\bpeer\b\|\bPeer\b' CLAUDE.md` returns `0`.

### Task G.7 — Sweep "peer" → "service" in `docs/glossary.md`

**Files:** `docs/glossary.md`.

**Steps:**

1. Find every occurrence of "peer" in `docs/glossary.md`.
2. Replace with "service" where appropriate. Add a glossary entry for `service` if not present (and remove any standalone `peer` glossary entry, redirecting to `service`).

**Verification:** `grep -ni 'peer' docs/glossary.md` returns few or no matches.

### Task G.8 — Sweep "peer" → "service" in concept docs

**Files:** the 10 concept docs explicitly listed in spec G.3:
- `.ok-planner/design/concepts/claim-producer.md`
- `.ok-planner/design/concepts/executor.md`
- `.ok-planner/design/concepts/cascade-graph.md`
- `.ok-planner/design/concepts/discovery-cache.md`
- `.ok-planner/design/concepts/control-api.md`
- `.ok-planner/design/concepts/invalidate.md`
- `.ok-planner/design/concepts/conformance.md`
- `.ok-planner/design/concepts/observability.md`
- `.ok-planner/design/concepts/lifecycle-subscriber.md`
- `.ok-planner/design/concepts/rimsky-yml.md`

**Steps:**

1. For each of the 10 files, run `grep -n '\bpeer\b\|\bPeer\b' <file>` to capture every match.
2. Replace each match: `peer` → `service`, `Peer` → `Service`. All ten files use "peer" in the orchestrated-binary sense (per spec G.3's explicit list); no per-match judgment needed.
3. Other concept docs not in the list above are out of scope for this task — leave them alone unless a separate task touches them.

**Verification:** `grep -c '\bpeer\b\|\bPeer\b' <each-of-the-10-files>` returns `0` for each.

### Task G.9 — Sweep Go code variable / function names containing `peer`

**Files:** all Go source under `foundation/`, `modeling/`, `cmd/`, `test/`, `stores/`, `executors/`, `mcp-servers/`.

**Steps:**

1. Run `grep -rn '\bpeer\b\|\bPeer\b' foundation/ modeling/ cmd/ test/ stores/ executors/ mcp-servers/` to capture sites.
2. **Disposition rules (apply mechanically):**
   - **Rename when the symbol is a rimsky-defined identifier** referring to the orchestrated-binary role. Examples to rename: `peerName` → `serviceName`; `peerKind` → `serviceKind`; `peerAddr` → `serviceAddr`; `getPeer()` → `getService()`; `Peer` struct/field → `Service`.
   - **Leave alone when** the symbol comes from a third-party library (`grpc.Peer{}`, `peer.FromContext()`), is part of a Go stdlib API, or appears in a comment that's a verbatim quote of a library doc.
   - **In doubt, leave it.** A Go variable name that doesn't get renamed is not load-bearing for vocabulary alignment; concept docs (Task G.8) are the canonical surface and they're tightly scoped.
3. Apply each rename via the Edit tool. After all edits, run `cmd:make build-all` to catch any missed call site.

**Verification:** `cmd:make build-all` passes. Re-run the grep from Step 1; remaining matches should be third-party library references or legitimate non-rimsky uses. No grep target count required (the disposition is partial by design).

---

## Section H — Layer reorganization (spec Group H)

Covers #19. Largest structural pass. Lands after all preceding sections so import paths and concept docs only churn once.

### Task H.1 — Create the `graph/` and `control/` directories via per-subdirectory moves

**Files:** all directories currently under `modeling/`.

**Layer split per spec H** (reproduced):

| New location | From `modeling/` |
|---|---|
| `graph/template/` | `modeling/template/` |
| `graph/node/` | `modeling/node/` |
| `graph/instance/` | (currently inside `modeling/`) |
| `graph/frame/` | `modeling/frame/` |
| `graph/scheduler/` | `modeling/scheduler/` |
| `graph/attribute/` | `modeling/attribute/` |
| `graph/qualityrule/` | `modeling/qualityrule/` |
| `graph/shared/` | `modeling/shared/` |
| `graph/scenario/` | `modeling/scenario/` |
| `graph/internal/pgtest/` | `modeling/internal/pgtest/` |
| `control/controlapi/` | `modeling/controlapi/` |
| `control/cli/` | `modeling/cli/` |
| `control/observability/` | `modeling/observability/` |
| `control/config/` | `modeling/config/` |

**Steps:**

1. Create the new parent directories: `mkdir -p graph control`.
2. For each row in the table, run `git mv modeling/<subdir> <new-location>`. Run them sequentially (git tracks each rename).
3. After all moves, run `ls modeling/` — the directory should be empty (or have only leftover files; sweep those into the appropriate location). If `modeling/` is fully empty, `rmdir modeling/`.

**Verification:** `! test -d modeling/`; `ls graph/ control/` shows the expected subdirectories.

### Task H.2 — Bulk-rename import paths from `modeling/` → `graph/` or `control/`

**Files:** every Go file across all three modules.

**Steps:**

1. Run `grep -rln '"github.com/fallguyconsulting/rimsky/modeling/' foundation/ protocols/ graph/ control/ cmd/ test/ stores/ executors/ mcp-servers/` to capture every importing file.
2. For each subdirectory mapping in the table (Task H.1), apply a corresponding import-path replacement via `sed -i '' -e 's|github.com/fallguyconsulting/rimsky/modeling/template|github.com/fallguyconsulting/rimsky/graph/template|g'` (and so on for each new path). Apply across all importing files. Use a small shell loop:
   ```bash
   for pair in "modeling/template:graph/template" "modeling/node:graph/node" "modeling/instance:graph/instance" "modeling/frame:graph/frame" "modeling/scheduler:graph/scheduler" "modeling/attribute:graph/attribute" "modeling/qualityrule:graph/qualityrule" "modeling/shared:graph/shared" "modeling/scenario:graph/scenario" "modeling/internal/pgtest:graph/internal/pgtest" "modeling/controlapi:control/controlapi" "modeling/cli:control/cli" "modeling/observability:control/observability" "modeling/config:control/config"; do
     old="${pair%%:*}"; new="${pair##*:}"
     grep -rln "github.com/fallguyconsulting/rimsky/${old}" foundation/ protocols/ graph/ control/ cmd/ test/ stores/ executors/ mcp-servers/ | xargs sed -i '' -e "s|github.com/fallguyconsulting/rimsky/${old}|github.com/fallguyconsulting/rimsky/${new}|g"
   done
   ```
   (Adjust path globs based on the actual file set; mcp-servers/ is its own module.)
3. Run `go mod tidy` in each module that was touched: `cd foundation && go mod tidy`; same for `protocols/`, root, and `mcp-servers/`.

**Verification:** `grep -rn '"github.com/fallguyconsulting/rimsky/modeling/' foundation/ protocols/ graph/ control/ cmd/ test/ stores/ executors/ mcp-servers/` returns no matches. `cmd:make build-all` passes.

### Task H.3 — Update `package modeling*` declarations to match new paths

**Files:** every Go file in `graph/` and `control/`.

**Steps:**

1. Run `grep -rn '^package' graph/ control/` to capture every package declaration.
2. For each, verify the package name matches the new directory (most likely no change — `package frame` stays `package frame` whether it's at `modeling/frame/` or `graph/frame/`). Only verify the file itself isn't broken.
3. If any file has a package name like `modelingInternal` or similar that referenced the old layer name, update.

**Verification:** `cmd:make build-all` passes after the rename.

### Task H.4 — Update depguard rules in `.golangci.yml`

**Files:** `.golangci.yml`.

**Steps:**

1. Open `.golangci.yml`. Locate the `depguard` configuration.
2. Update path-based rules per spec H "Depguard rules" section:
   - `pgx-isolation`: update allowed paths. Replace `modeling/internal/pgtest/` → `graph/internal/pgtest/`; replace `modeling/scenario/` → `graph/scenario/`. Other allowed paths (`foundation/persistence/postgres/`, `foundation/internal/pgtest/`, `cmd/`, `stores/`, `test/smoke/`) unchanged.
   - `foundation-internal-isolation`: unchanged (the rule references `foundation/internal/`).
3. Add a new `graph-control-isolation` rule:
   ```yaml
   graph-control-isolation:
     list-mode: lax
     files:
       - "$all"
       - "!**/*_test.go"
     deny:
       - pkg: github.com/fallguyconsulting/rimsky/control
         desc: 'graph/ must not import control/ (one-way: control reads graph; graph never reads control)'
     ignore-file-rules:
       - "control/**/*.go"
   ```
   (Adjust the exact YAML to match the existing `.golangci.yml` depguard schema; the intent is: only files under `control/` may import `control/...`; files anywhere else are denied.)

**Verification:** `cmd:make lint` passes. Verify the new rule's enforcement: temporarily add an import of `control/controlapi` into a file under `graph/` and confirm `make lint` errors; revert.

### Task H.5 — Update `CLAUDE.md` "Package import rules" section

**Files:** `CLAUDE.md`.

**Steps:**

1. Open `CLAUDE.md`. Locate the section "Package import rules (enforced; violations break the build)".
2. Rewrite the modeling-layer bullet to describe the two-way split:
   - Replace the `modeling/` bullet with two bullets — one for `graph/` and one for `control/`.
   - Describe the boundary: `control` → `graph` (one-way read access via `persistence.Driver` + small mutation set); `graph` → `control` is forbidden.
   - Update the `.golangci.yml depguard` summary to mention the new `graph-control-isolation` rule.
3. Sweep references to `modeling/...` elsewhere in the file. Update paths in:
   - "Repository layout" section.
   - The depguard summary block.
   - Any "Where to look first" entries.
   - The non-obvious gotchas if any cite `modeling/...`.

**Verification:** `grep -n 'modeling/' CLAUDE.md` returns no matches. Manual read of the updated Package import rules section confirms the split is clearly stated.

### Task H.6 — Update `concept:module-layout.md` body

**Files:** `.ok-planner/design/concepts/module-layout.md`.

**Steps:**

1. Open `.ok-planner/design/concepts/module-layout.md`.
2. Update the body to describe the four-directory root-module layout: `graph/`, `control/`, `cmd/`, `stores/`, `executors/` (plus the historical `protocols/` and `foundation/` as separate Go modules).
3. Update the aliases (currently includes `three-go-modules`) — clarify that the workspace has three Go modules + the MCP-server module, but the **root module** now has `graph/` + `control/` siblings.
4. Append a Notes entry: `- Two-way split of modeling/ → graph/ + control/ per spec:2026-05-12-nomenclature-resolution (audit cross-layer #19).`

**Verification:** `grep -n 'graph/\|control/' .ok-planner/design/concepts/module-layout.md` returns matches in the body.

---

## Section I — Ride-along renames (spec Group I)

### Task I.1 — Rename `rimsky-executor-conformance` binary → `rimsky-executor-conformance`

**Files:** `cmd/rimsky-executor-conformance/` (rename to `cmd/rimsky-executor-conformance/`); `Makefile`; CI workflows; `CLAUDE.md`.

**Steps:**

1. Run `git mv cmd/rimsky-executor-conformance cmd/rimsky-executor-conformance`.
2. Update the binary's `main.go` if any references self-reference the old name (typically the logger / version-info string).
3. Update `Makefile`: find `build-rimsky-executor-conformance` or similar targets and rename. Update `install` / `compose` references.
4. Update `deploy/docker-compose.yml` (if any service uses the binary).
5. Update `.github/workflows/` (or whichever CI config exists) — search for `rimsky-executor-conformance` and update to `rimsky-executor-conformance` where the executor-conformance binary is invoked.
6. Update `CLAUDE.md` "Build & test" section to reference the renamed binary.
7. The probe sidecar `cmd/rimsky-conformance-probe/` stays unchanged (generic name preserved per spec I.1).
8. The conformance entry `concept:conformance.md` updates in Section J's concept-doc sweep.

**Verification:** `test -d cmd/rimsky-executor-conformance && ! test -d cmd/rimsky-executor-conformance`. `cmd:make build-all` passes. The Makefile target builds the renamed binary.

### Task I.2 — Rename `runner_terminal_errors.go` → `runner_error_policy.go`; `applyTerminalAppError` → `applyErrorPolicy`

**Files:** `foundation/integration/runner_terminal_errors.go` (rename); call sites.

**Steps:**

1. `git mv foundation/integration/runner_terminal_errors.go foundation/integration/runner_error_policy.go`.
2. In the renamed file, find the function `applyTerminalAppError` (or close — verify exact name) and rename to `applyErrorPolicy`. Update the leading function doc comment.
3. Sweep call sites: `grep -rn 'applyTerminalAppError' foundation/ modeling/ cmd/ test/` and rename.
4. After Section E.9's `applyTerminalBlockedOrErrored` → `applyTerminalError` collapse, this function and `applyTerminalError` together are the post-collapse error-handling surface. Verify the call graph is sensible (`applyTerminalError` calls `applyErrorPolicy` for the error_types lookup, etc. — preserve the existing call shape).

**Verification:** `test -f foundation/integration/runner_error_policy.go && ! test -f foundation/integration/runner_terminal_errors.go`. `grep -rn 'applyTerminalAppError' foundation/ modeling/ cmd/ test/` returns no matches. `cmd:make build-all && make test-all` passes.

---

## Section J — Concept-doc body sweeps (per-concept cross-cutting changes)

Many concept docs need small body updates to reflect rename / restructure landed in earlier sections. Each task below applies one concept-doc's body update.

### Task J.1 — Update `concept:cascade.md`

Covered by Task F.1. Verify with `grep -n 'walk\|propagation\|fallthrough' .ok-planner/design/concepts/cascade.md`.

### Task J.2 — Update `concept:claim-handle.md`

Covered by Task G.1 (held-variant + authoring subsections folded in) and Task A.6 (sweep `rimsky_claim_handle` → `rimsky_claim_handles` plural). Verify with `grep -n '### Held variant\|rimsky_claim_handles' .ok-planner/design/concepts/claim-handle.md`.

### Task J.3 — Update `concept:claim-producer.md`

**Files:** `.ok-planner/design/concepts/claim-producer.md`.

**Steps:**

1. Sweep references to legacy `Store` alias. Update aliases list to drop `store (legacy)` if present, OR keep it as a Notes mention since it explains the colloquial-services-layer usage.
2. Update proto-citation: any `proto:store_observability.proto::StoreObservability` reference becomes `proto:claim_producer_observability.proto::ClaimProducerObservability`.
3. Sweep "peer" → "service" in prose.
4. Update `ClaimSpec.StoreName` reference → `.ProducerName`.
5. Append Notes entry: `- Store-alias retirement landed per spec:2026-05-12-nomenclature-resolution (audit cross-layer #1).`

**Verification:** `grep -n 'StoreObservability\|StoreName\|store_observability' .ok-planner/design/concepts/claim-producer.md` returns no matches.

### Task J.4 — Update `concept:claim.md`

**Files:** `.ok-planner/design/concepts/claim.md`.

**Steps:**

1. Update `ClaimSpec.StoreName` reference → `.ProducerName`.
2. Add Adjacent: `inertness`.
3. Append Notes entry citing this spec for the ProducerName rename.

**Verification:** `grep -n 'StoreName' .ok-planner/design/concepts/claim.md` returns no matches.

### Task J.5 — Update `concept:conformance.md`

**Files:** `.ok-planner/design/concepts/conformance.md`.

**Steps:**

1. Sweep binary citations: `code:cmd/rimsky-executor-conformance/main.go` → `code:cmd/rimsky-executor-conformance/main.go`.
2. Update the binary inventory (the four binaries; pattern `rimsky-<protocol>-conformance`). Mention the probe is generic by design.
3. Append Notes entry: `- Renamed executor-conformance binary per spec:2026-05-12-nomenclature-resolution (audit ride-along I.1).`

**Verification:** `grep -n 'rimsky-executor-conformance\b' .ok-planner/design/concepts/conformance.md` returns no matches (only `rimsky-executor-conformance`, `rimsky-claim-producer-conformance`, etc.).

### Task J.6 — Update `concept:control-api.md`

**Files:** `.ok-planner/design/concepts/control-api.md`.

**Steps:**

1. Update route citation: `route:GET /dispatches` → `route:GET /node-runs`.
2. Update directory citation: `code:modeling/controlapi/` → `code:control/controlapi/`.
3. Sweep "peer" → "service".
4. Append Notes entry.

**Verification:** `grep -n '/dispatches\|modeling/controlapi' .ok-planner/design/concepts/control-api.md` returns no matches.

### Task J.7 — Update `concept:cascade-graph.md`

**Files:** `.ok-planner/design/concepts/cascade-graph.md`.

**Steps:**

1. Route citations sweep: `/dispatches` → `/node-runs`.
2. Directory citation: `modeling/controlapi/` → `control/controlapi/`.
3. Sweep "peer" → "service".

**Verification:** `grep -n '/dispatches\|modeling/controlapi' .ok-planner/design/concepts/cascade-graph.md` returns no matches.

### Task J.8 — Update `concept:discovery-cache.md`

**Files:** `.ok-planner/design/concepts/discovery-cache.md`.

**Steps:**

1. Update Capabilities-handshake-method reference: `GetCapabilities` → `Capabilities` (uniform across protocols).
2. Update path citation: `code:modeling/observability/handshake.go` → `code:control/observability/handshake.go`.
3. Sweep "peer" → "service".
4. Append Notes entry.

**Verification:** `grep -n 'GetCapabilities\|modeling/observability' .ok-planner/design/concepts/discovery-cache.md` returns no matches.

### Task J.9 — Update `concept:error-policy.md`

**Files:** `.ok-planner/design/concepts/error-policy.md`.

**Steps:**

1. Add the three-name relationship subsection per spec I.2: "The design-log noun is `error-policy`; the operator-facing YAML field is `error_types:` (map of error_class → action); the implementation lives in `code:foundation/integration/runner_error_policy.go::applyErrorPolicy`."
2. Update file citations: `runner_terminal_errors.go::applyTerminalAppError` → `runner_error_policy.go::applyErrorPolicy`.
3. Document the four actions explicitly: `retry` / `invalidate(targets)` / `give_up` / `pass`.
4. Note that `Blocked` collapsed into `Error{error_class}` per spec E.2 — error_types is now the SINGLE decision surface for error routing.
5. Append Notes entry resolving `tension:error-action-count-drift`.

**Verification:** `grep -n 'runner_error_policy\|applyErrorPolicy\|retry / invalidate' .ok-planner/design/concepts/error-policy.md` returns matches.

### Task J.10 — Update `concept:executor.md`

**Files:** `.ok-planner/design/concepts/executor.md`.

**Steps:**

1. Update proto-service citation: `proto:executor.proto::NodeExecutor` → `proto:executor.proto::Executor`.
2. Update Capabilities reference: `GetCapabilities` → `Capabilities`.
3. Note the new event-stream shape (channel-mechanics `StreamClose` + outcome oneof) — high-level summary; refer to spec for full proto block.
4. Sweep "peer" → "service".
5. Append Notes entry.

**Verification:** `grep -n 'NodeExecutor\|GetCapabilities' .ok-planner/design/concepts/executor.md` returns no matches.

### Task J.11 — Update `concept:frame.md`

**Files:** `.ok-planner/design/concepts/frame.md`.

**Steps:**

1. Sweep `frame_resolution` → `frame_resolution_mode` across YAML / column / Go citations.
2. Confirm the table citation is plural (`rimsky_frames`).
3. Append Notes entry.

**Verification:** `grep -n '\bframe_resolution\b\(_mode\)\@!' .ok-planner/design/concepts/frame.md` (or equivalent — manually verify only `_mode` form remains).

### Task J.12 — Update `concept:instance.md`

**Files:** `.ok-planner/design/concepts/instance.md`.

**Steps:**

1. Update Column citation: drop the "(legacy `consumer_key`)" annotation since the rename history is erased at the schema-rebase level. The current canonical name is `instance_key`.
2. Append Notes entry citing the migration rebase (Task A.2).

**Verification:** `grep -n 'consumer_key' .ok-planner/design/concepts/instance.md` returns no matches (or only in a Notes-section historical reference).

### Task J.13 — Update `concept:lifecycle-handler.md`

**Files:** `.ok-planner/design/concepts/lifecycle-handler.md`.

**Steps:**

1. Update slot count from 4 to 3:
   - Template Fields list: `on_acquire_unavailable`, `on_executor_complete`, `on_executor_errored` (no `on_executor_blocked`).
   - Boundaries: clarify that `on_executor_blocked` was retired with the proto restructure (spec E.2). All error variants now route through `on_executor_errored`; `error_types` policy discriminates.
   - Invariants: "Three lifecycle-handler slots plus the `on_event` map" (formerly "four plus on_event").
2. Add a Notes entry citing this spec for the slot-count drop and the resolution of `tension:blocked-vs-errored-routing` + `tension:_resolved/handler-slot-count-drift`.
3. Update runtime-apply citation: `applyTerminalBlockedOrErrored` → `applyTerminalError`.

**Verification:** `grep -n 'on_executor_blocked\|four slots\|4 slots' .ok-planner/design/concepts/lifecycle-handler.md` returns no matches.

### Task J.14 — Update `concept:node-state.md`

**Files:** `.ok-planner/design/concepts/node-state.md`.

**Steps:**

1. Confirm the 5-state enum is correctly enumerated: `fresh`, `stale`, `running`, `failed`, `parked`. (Per spec E.2, the value `parked` STAYS; the proto event renames to `Snooze` but the state-machine value does not change.)
2. Update any prose that referenced `ParkRequested` proto-event → `proto:executor.proto::Snooze` (formerly `ParkRequested`).
3. Append Notes entry citing the proto rename (state-machine itself unchanged).

**Verification:** `grep -n 'ParkRequested' .ok-planner/design/concepts/node-state.md` returns either no matches OR only historic notes.

### Task J.15 — Update `concept:observability.md`

**Files:** `.ok-planner/design/concepts/observability.md`.

**Steps:**

1. Update proto-service citation: `proto:store_observability.proto::StoreObservability` → `proto:claim_producer_observability.proto::ClaimProducerObservability`.
2. Update Capabilities RPC reference: `GetCapabilities` → `Capabilities` (now uniform across both observability services).
3. Update path: `code:modeling/observability/handshake.go` → `code:control/observability/handshake.go`.

**Verification:** `grep -n 'StoreObservability\|GetCapabilities\|modeling/observability' .ok-planner/design/concepts/observability.md` returns no matches.

### Task J.16 — Update `concept:parked-state.md`

Covered by Task E.12. Verify with `grep -n 'ParkRequested\|Snooze' .ok-planner/design/concepts/parked-state.md`.

### Task J.17 — Update `concept:persistence-driver.md`

**Files:** `.ok-planner/design/concepts/persistence-driver.md`.

**Steps:**

1. Update Go-interface citation: `code:foundation/persistence/store.go::Store` → `code:foundation/persistence/driver.go::Driver`. (File and interface both renamed in Task B.7.)
2. Document the row-struct convention (Go-side row structs stay singular even though tables are plural: `NodeRow`, `FrameRow`, `ClaimHandleRow`, `NodeRunRow`).
3. Append Notes entry citing the Store→Driver rename and the schema-rebase.

**Verification:** `grep -n 'persistence/store.go\|::Store\b' .ok-planner/design/concepts/persistence-driver.md` returns no matches.

### Task J.18 — Update `concept:rimsky-yml.md`

**Files:** `.ok-planner/design/concepts/rimsky-yml.md`.

**Steps:**

1. Document the retired `stores:` alias and the retired `write_semantics:` single-value form.
2. Update `write_semantics_envelope` → `write_semantics_allowed`.
3. Sweep "peer" → "service"; note the `protocols:` block per service entry.
4. Append Notes entry.

**Verification:** `grep -n 'stores:\|write_semantics_envelope' .ok-planner/design/concepts/rimsky-yml.md` returns no matches.

### Task J.19 — Update `concept:rimsky-cli.md`

**Files:** `.ok-planner/design/concepts/rimsky-cli.md`.

**Steps:**

1. Update route citation if any: `/dispatches` → `/node-runs`.
2. Update directory citation: `code:modeling/cli/` → `code:control/cli/`.

**Verification:** `grep -n '/dispatches\|modeling/cli' .ok-planner/design/concepts/rimsky-cli.md` returns no matches.

### Task J.20 — Update `concept:scope.md`

**Files:** `.ok-planner/design/concepts/scope.md`.

**Steps:**

1. Drop any "Legacy term: region" reference from the Surfaces table since the region rename is fully erased.
2. Keep the `concept:scope` definition prominent; expand the byte-equal-conflict rationale if not present (since Task B.8 deleted the in-code comment that previously hosted it).

**Verification:** `grep -i 'region' .ok-planner/design/concepts/scope.md` returns no matches.

### Task J.21 — Update `concept:supervisor.md`

**Files:** `.ok-planner/design/concepts/supervisor.md`.

**Steps:**

1. Sweep `worker_request` / `WorkerRequest` / `workerRequest` → `node_run` / `NodeRun` / `nodeRun` per Task D.7's rename pattern.
2. Sweep `Store = ClaimProducer` alias usage and `StoreName` field references.

**Verification:** `grep -n 'worker_request\|WorkerRequest\|workerRequest' .ok-planner/design/concepts/supervisor.md` returns no matches.

### Task J.22 — Update `concept:template.md`

**Files:** `.ok-planner/design/concepts/template.md`.

**Steps:**

1. Sweep `FrameResolution` → `FrameResolutionMode`; `frame_resolution` → `frame_resolution_mode`.
2. Update directory citation: `code:modeling/template/` → `code:graph/template/`.
3. Update `LifecycleHandlers` field reference (post-3-slot collapse).

**Verification:** `grep -n 'FrameResolution\b\|modeling/template' .ok-planner/design/concepts/template.md` returns no matches.

### Task J.23 — Update `concept:write-semantics.md`

Covered by Task C.3. Verify with `grep -n 'write_semantics_envelope' .ok-planner/design/concepts/write-semantics.md`.

### Task J.24 — Update remaining concept docs with `modeling/` → `graph/` / `control/` path renames

**Files:** every concept doc that cites a `code:modeling/...` path. List from spec G (the "Concept files modified in body" list).

**Steps:**

1. Run `grep -rln 'code:modeling/\|modeling/' .ok-planner/design/concepts/` to capture every file with a `modeling/` citation.
2. For each file, edit each citation to the new path per Section H's mapping table.

**Verification:** `grep -rn 'code:modeling/\|modeling/' .ok-planner/design/concepts/` returns no matches.

### Task J.25 — Add `concept:inertness` cross-references on the five inert-stream concepts

**Files:** `concept:userdata.md`, `concept:claim.md`, `concept:blob-backend.md`, `concept:named-event.md`, `concept:attribute.md`.

**Steps:**

1. For each file, ensure the Adjacent list contains `inertness`.
2. If the body has a relevant paragraph about how the data is read/handled, add a one-line cross-link: "Inertness discipline cross-linked at `concept:inertness`."

**Verification:** `grep -l 'inertness' .ok-planner/design/concepts/userdata.md .ok-planner/design/concepts/claim.md .ok-planner/design/concepts/blob-backend.md .ok-planner/design/concepts/named-event.md .ok-planner/design/concepts/attribute.md | wc -l` returns 5.

### Task J.26 — Sweep concept-doc Adjacent lists that referenced dropped `held-claim`

**Files:** every concept doc whose Adjacent list mentions `held-claim`.

**Steps:**

1. Run `grep -rn 'held-claim' .ok-planner/design/concepts/` to find references.
2. For each: replace `held-claim` reference with `claim-handle#held-variant` if the context references the row variant, or `auto-terminal` if the context references the runtime mechanism.

**Verification:** `grep -rn 'held-claim' .ok-planner/design/concepts/` returns no matches (or only in workflow-scratch / historical contexts).

### Task J.27 — Update `concept:orphan-reaper.md`

**Files:** `.ok-planner/design/concepts/orphan-reaper.md`.

**Steps:**

1. Update sweep-function citations: `SweepLockHolders` → `SweepOrphanedClaimHandles`; `SweepOrphanedClaims` → `SweepOrphanedNodeRuns`. (Note: `SweepLockHolders` is already named `SweepClaimHandles` in the actual source; the audit's mention of `SweepLockHolders` was stale. Verify by reading the current file content.)
2. Append Notes entry citing this spec.

**Verification:** `grep -n 'SweepLockHolders\|SweepClaimHandles\b\|SweepOrphanedClaims\b' .ok-planner/design/concepts/orphan-reaper.md` returns no matches that aren't the renamed names.

### Task J.28 — Update `concept:auto-terminal.md`

**Files:** `.ok-planner/design/concepts/auto-terminal.md`.

**Steps:**

1. Sweep `rimsky_claim_handle` → `rimsky_claim_handles` (plural).
2. Remove any `held-claim` Adjacent reference (since the concept is dropped).
3. Confirm cross-links to `concept:claim-handle#held-variant` if relevant.

**Verification:** `grep -n 'held-claim\b\|rimsky_claim_handle\b' .ok-planner/design/concepts/auto-terminal.md` returns no matches.

### Task J.29 — Update `concept:named-event.md`

**Files:** `.ok-planner/design/concepts/named-event.md`.

**Steps:**

1. Update proto-service citation if it mentions `NodeExecutor` → `Executor`.
2. Ensure `inertness` is in Adjacent.

**Verification:** `grep -n 'NodeExecutor' .ok-planner/design/concepts/named-event.md` returns no matches.

### Task J.30 — Update `concept:advisory-lock.md`

**Files:** `.ok-planner/design/concepts/advisory-lock.md`.

**Steps:**

1. Update interface citation: `code:foundation/persistence/store.go::AdvisoryLocker` → `code:foundation/persistence/driver.go::AdvisoryLocker` (after Task B.7's file rename, AdvisoryLocker is in the renamed file).

**Verification:** `grep -n 'persistence/store.go' .ok-planner/design/concepts/advisory-lock.md` returns no matches.

### Task J.31 — Update `concept:event-log.md` and remaining unmentioned concepts

**Files:** `.ok-planner/design/concepts/event-log.md`, `.ok-planner/design/concepts/on-event-handler.md`, `.ok-planner/design/concepts/terminal-resolution.md`, `.ok-planner/design/concepts/lifecycle-subscriber.md`, `.ok-planner/design/concepts/tag.md`, `.ok-planner/design/concepts/node.md`, `.ok-planner/design/concepts/schedule.md`, `.ok-planner/design/concepts/quality-rule.md`, `.ok-planner/design/concepts/named-lock.md`, `.ok-planner/design/concepts/blob-backend.md`, `.ok-planner/design/concepts/attribute.md`.

**Steps:**

1. Run one cross-file grep to capture every residual legacy reference at once:
   ```bash
   grep -rn '\bmodeling/\|\bworker_request\b\|\bWorkerRequest\b\|\bNodeExecutor\b\|\bGetCapabilities\b\|\bStoreObservability\b\|\bStoreName\b\|\bwrite_semantics_envelope\b\|\bpeer\b\|\bPeer\b\|\bopaque\b' .ok-planner/design/concepts/event-log.md .ok-planner/design/concepts/on-event-handler.md .ok-planner/design/concepts/terminal-resolution.md .ok-planner/design/concepts/lifecycle-subscriber.md .ok-planner/design/concepts/tag.md .ok-planner/design/concepts/node.md .ok-planner/design/concepts/schedule.md .ok-planner/design/concepts/quality-rule.md .ok-planner/design/concepts/named-lock.md .ok-planner/design/concepts/blob-backend.md .ok-planner/design/concepts/attribute.md
   ```
2. For each match, apply the rename per earlier sections' mapping (e.g., `modeling/observability/` → `control/observability/`, `worker_request` → `node_run`, `NodeExecutor` → `Executor`, `GetCapabilities` → `Capabilities`, `StoreObservability` → `ClaimProducerObservability`, `StoreName` → `ProducerName`, `write_semantics_envelope` → `write_semantics_allowed`, `peer` → `service`, `opaque` (in invariant 11 context) → `inert`).
3. For `concept:terminal-resolution.md` specifically: add a short clarification paragraph that "terminal" is no longer a wire-protocol term post-E.2 — it retains its meaning only for the state-machine + decision-engine senses (`code:foundation/integration/terminal_decision.go::*`, `concept:node-state` terminal-state property). Wire-level events use `StreamClose` + outcome `oneof`.

**Verification:** Re-run the grep from Step 1 — it must return zero matches across the 11 files (excluding legitimate-context matches in Notes / history sections, which should be flagged with explicit `(historical)` annotation if kept).

---

## Section K — Tension file moves

### Task K.1 — Move resolved tension files to `tensions/_resolved/`

**Files:** various tension files in `.ok-planner/design/tensions/`.

**Steps:**

For each tension below, run `git mv .ok-planner/design/tensions/<slug>.md .ok-planner/design/tensions/_resolved/<slug>.md`, then prepend (or update) a one-line top-of-file `Resolved by: spec:2026-05-12-nomenclature-resolution (<group>)` note inside the frontmatter.

The 14 resolved tensions:

1. `store-vs-claim-producer-vocabulary` (group B + C)
2. `yaml-stores-alias` (group B.6, C.1)
3. `yaml-write-semantics-alias` (group C)
4. `frame-resolution-vs-mode-vocabulary` (group D.1)
5. `lock-holder-vs-claim-handle-legacy` (group A, D)
6. `consumer-key-vs-instance-key` (group A)
7. `region-vs-scope-legacy` (group A, B.8)
8. `terminal-event-overloaded` (group E.2)
9. `cascade-walks-overloaded` (group F)
10. `transition-reason-vs-last-outcome` (group B.9, B.10)
11. `blocked-vs-errored-routing` (group E.2, E.9, E.10)
12. `async-callback-body-key` (group E.6 — superseded by E.2)
13. `error-action-count-drift` (group I.2 / J.9)
14. `executor-conformance-binary-asymmetry` (group I.1) — note: this tension did not exist as a file before; if there is no `.ok-planner/design/tensions/executor-conformance-binary-asymmetry.md`, skip the move for this entry (it was a new-and-resolved-in-same-spec tension).

**Verification:** `ls .ok-planner/design/tensions/_resolved/ | grep -c .md` increased by the count of resolved tensions (≥ 13 new entries). Each moved file has a `Resolved by:` note at the top.

---

## Section L — Final cross-module verification

### Task L.1 — Full build across all modules

**Steps:**

1. From the repository root, run:
   ```bash
   make proto-gen && make build-all && make tidy
   ```

**Verification:** All commands exit 0. No build errors, no `go vet` warnings, no `go mod tidy` divergence.

### Task L.2 — Full test across all modules

**Steps:**

1. Ensure Docker is running (testcontainers requires it).
2. Run:
   ```bash
   make test-all
   ```

**Verification:** Test summary shows 0 failures. Scenario tests in `test/scenarios/...` pass (they spin up Postgres via testcontainers and validate the renamed schema and the new event shape end-to-end).

### Task L.3 — Lint

**Steps:**

1. Run `make lint`.

**Verification:** golangci-lint exits 0. Depguard rules pass:
- `pgx-isolation` honors the new allowed paths.
- `foundation-internal-isolation` unchanged.
- `graph-control-isolation` enforces the new boundary.

### Task L.4 — TypeScript executor

**Steps:**

1. Run:
   ```bash
   cd executors/claude-agent && npm install && npm test && npm run build
   ```

**Verification:** All commands exit 0. The TS test suite validates the new `AsyncCallbackBody` shape end-to-end against the Go supervisor.

### Task L.5 — Conformance binaries build

**Steps:**

1. Build each conformance binary:
   - `go build ./cmd/rimsky-executor-conformance/...`
   - `go build ./cmd/rimsky-claim-producer-conformance/...`
   - `go build ./cmd/rimsky-blob-backend-conformance/...`
   - `go build ./cmd/rimsky-conformance-probe/...`

**Verification:** Each `go build` exits 0. (End-to-end conformance runs against live stub binaries require manual stand-up of stub endpoints; that's covered in the "Manual checks after completion" section, not here. Build-only verification is sufficient for the automated plan — it catches every type-shape regression introduced by the proto restructure and the renames.)

### Task L.6 — Smoke fixture test (in-process)

**Steps:**

1. Run `go test ./test/smoke/... -count=1`. The smoke fixture in `test/smoke/setup.go` brings up the rimsky processes in-process and exercises end-to-end dispatch via the modeling-scenario harness against pre-launched stub services on ephemeral ports — no Docker stack required.

**Verification:** `go test ./test/smoke/... -count=1` exits 0. The fixture exercises the post-rename surface end-to-end (dispatch → executor stub → terminal → cascade).

### Task L.7 — Vocabulary-lint fixture verification

**Files:** `cmd/rimsky-docs-lint/vocabulary_test.go`.

**Steps:**

1. Read the existing fixture entries. Verify each entry is still active — i.e., the legacy word it guards against would be found by the lint if a regression occurred.
2. Add new fixture entries for terms newly retired by this spec, where the term is no longer expected to appear in active code:
   - `Store` (in the foundation-locks-alias sense; care for false-positives against `stores/` directory and bundled-impl binary names — scope the lint to non-`stores/` source paths)
   - `stores:` (in YAML key context)
   - `write_semantics:` (single-value form, in YAML context)
   - `frame_resolution:` (template YAML key, NOT `frame_resolution_mode:`)
   - `worker_request`, `WorkerRequest` (in active code; allowed in CHANGELOG / `.ok-planner/`)
   - `NodeExecutor` (in active code)
   - `GetCapabilities` (in active code)
   - `opacity` (where it appears in `@blessed-invariant 11` style contexts)
   - `peer` (where the noun is the orchestrated-binary; allow legitimate peer-to-peer usages)
   - `ParkRequested` (in active proto / code; allowed in concept docs as historical mention)
   - `on_executor_blocked` (in active templates)
   - `Blocked`, `Errored`, `Complete`, `AsyncAccepted` (in proto-event-name contexts; care for false-positives in `Complete` since it's a common English word)
3. Run `go test ./cmd/rimsky-docs-lint/...`.

**Verification:** The lint test passes. Add a regression line specifically for each retired term that confirms the lint catches a hypothetical re-introduction.

### Task L.8 — Refresh `.ok-planner/design/concepts.md` TOC by hand-editing

**Steps:**

1. Open `.ok-planner/design/concepts.md` (the auto-generated TOC; this plan refreshes it by hand since no separate regeneration tool is available at plan-execution time).
2. Apply these mechanical edits to the concepts list:
   - **Remove** the `held-claim` entry.
   - **Remove** the `opacity` entry.
   - **Remove** the `worker-request` entry.
   - **Add** an `inertness` entry — definition line: `Cross-cutting discipline making the five byte streams (userdata, claim scope, claim payload, blob content, named-event payload) inert in rimsky; two sub-disciplines: byte-opaque inertness and structural inertness.`
   - **Add** a `node-run` entry — definition line: `One execution of one node within a frame; persisted as a row in rimsky_node_runs.`
   - **Add** a `service` entry — definition line: `Out-of-process gRPC binary that implements one or more rimsky service protocols and is orchestrated by rimsky; umbrella for executor, claim-producer, lifecycle-subscriber, blob-backend.`
3. Keep the entries alphabetized within the existing list.
4. The header note at the top of `concepts.md` already explains "Do not edit by hand — changes will be overwritten" but the next regeneration by `discover-design` or `execute-plan`'s post-archive step is when the warning would apply. For this run, hand-edit is the path; the next regeneration will reproduce the same set deterministically.

**Verification:** `grep -c '^- \`' .ok-planner/design/concepts.md` returns 46 (the final concept count per spec G). `grep -E '^- \`(inertness|node-run|service|held-claim|opacity|worker-request)\`' .ok-planner/design/concepts.md` returns exactly three lines (the three new entries; the three removed slugs do not appear).

---

## Manual checks after completion

The plan above is fully automated; no manual checks are required between tasks. After all tasks complete, the user owns:

1. **Review the diff** (`git diff main`) for sanity. The diff is large; expect to walk through it in chunks.
2. **Decide commit strategy.** Suggest grouping commits per section (A through L) for reviewability; a single squash commit also works pre-v1.
3. **Deploy-stack smoke** (cannot be automated — requires Docker daemon + free port 8080 + image build). Run:
   ```bash
   cd deploy && ./build-images.sh && docker compose -f docker-compose.yml up -d
   curl http://localhost:8080/health   # expect 200
   ```
   Exercise the renamed operator-facing surfaces via `rimsky-cli` (the renamed `/node-runs` route, the renamed Capabilities handshake, the new `AsyncCallbackBody` shape) end-to-end. This duplicates what the in-process smoke fixture (L.6) covers but exercises the actual deployed images.
4. **End-to-end conformance against live stubs** (cannot be automated without endpoint stand-up). For each conformance binary, start the corresponding stub binary on an ephemeral port and run the conformance against it. Example for the executor:
   ```bash
   ./bin/rimsky-executor-conformance --transport grpc --endpoint <stub-endpoint> --require-stub-mode
   ```
   The in-process build verification (L.5) is sufficient to catch type-shape regressions; this manual step confirms the conformance binary runs end-to-end against an actual stub.
5. **Optionally regenerate public docs** (`cmd:rimsky-docs-glossary`, `cmd:rimsky-docs-llms-full`) to refresh `docs/glossary.md` and `docs/agents/llms-full.txt` with the new vocabulary.

These are not part of the automated run.
