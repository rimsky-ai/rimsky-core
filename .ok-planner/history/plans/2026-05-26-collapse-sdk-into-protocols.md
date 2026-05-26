# Collapse `sdk/go` into `protocols` — Implementation Plan

**Spec:** .ok-planner/specs/2026-05-26-collapse-sdk-into-protocols-design.md
**Goal:** Make `protocols` the single public-facing Go module a service implementer depends on by folding `sdk/go`'s contract + convenience packages into it, carving the postgres/docker test helper into its own opt-in module, relocating generic ops glue to core-internal, and closing the `foundation/locks` contract-type leak.
**Architecture:** Four Go modules tied by `go.work`: `.` (root), `./foundation`, `./protocols`, and a new opt-in `./testpg`. The deleted `sdk/go` module's packages move into `protocols` (`action`, `conformance`, `serverkit`, `publisherkit`), into a standalone `testpg` module (the testcontainer helper), and into `internal/ops` (the generic glue). The `foundation/locks` package stops re-exporting claim-producer contract types; `protocols/claimproducer` becomes their only Go home.
**Tech Stack:** Go 1.25, go workspaces, golangci-lint (depguard), `cmd/rimsky-license-check` (multi-license boundary).

---

## Background the implementer needs (read before starting)

You are executing a module-topology refactor. No prior context is assumed. Key facts grounded from the live tree:

**Current modules** (`go.work`): `use ( . ./foundation ./protocols ./sdk/go )`.

**`sdk/go` package inventory and destinations:**

| Current path | Destination | Notes |
|---|---|---|
| `sdk/go/stores/action` (package `action`) | `protocols/action` (package `action`) | drops the `stores/` intermediate; imports `gopkg.in/yaml.v3` |
| `sdk/go/conformance/{blobbackend,claimproducer,dataprocessing,executor,publisher,validation}` | `protocols/conformance/{...}` | `executor/` has a `scenarios/` subpkg; imports `github.com/google/uuid` |
| `sdk/go/server` (package `server`) | `protocols/serverkit` (package `serverkit`) | **package identifier renamed** `server` → `serverkit` |
| `sdk/go/publisher` (package `publisher`) | `protocols/publisherkit` (package `publisherkit`) | **package identifier renamed**; zero in-repo importers |
| `sdk/go/ops` (package `ops`) | `internal/ops` (package `ops`) | core-internal; headers flip Apache → AGPL |
| `sdk/go/testpg` (package `testpg`) | new module `./testpg/`, import path `github.com/fallguyconsulting/rimsky/testpg` | carries testcontainers + pgx; imports no rimsky package |
| `sdk/go/doc.go` | deleted | carries `// @concept: sdk` |

**Import-path rewrites** (apply with care; some files take more than one):

- `github.com/fallguyconsulting/rimsky/sdk/go/stores/action` → `github.com/fallguyconsulting/rimsky/protocols/action`
- `github.com/fallguyconsulting/rimsky/sdk/go/conformance/<x>` → `github.com/fallguyconsulting/rimsky/protocols/conformance/<x>`
- `github.com/fallguyconsulting/rimsky/sdk/go/server` → `github.com/fallguyconsulting/rimsky/protocols/serverkit` (and `server.` → `serverkit.` unless the import is aliased)
- `github.com/fallguyconsulting/rimsky/sdk/go/publisher` → `github.com/fallguyconsulting/rimsky/protocols/publisherkit`
- `github.com/fallguyconsulting/rimsky/sdk/go/testpg` → `github.com/fallguyconsulting/rimsky/testpg`
- `github.com/fallguyconsulting/rimsky/sdk/go/ops` → `github.com/fallguyconsulting/rimsky/internal/ops`

**Verified importer sites** (counts are exact as of authoring; re-derive with `grep -rl` before editing — do not trust stale counts):

- `sdk/go/stores/action`: 13 sites — `stores/{filesystem,postgres}/testfixture/testfixture.go`, `stores/stub/cmd/main.go`, `stores/stub/store/{store.go,store_test.go}`, and 8 files under `test/scenarios/`.
- `sdk/go/conformance/*`: the 7 `cmd/rimsky-*-conformance/main.go` binaries (some alias the import, e.g. `conformance "...conformance/executor"` and `_ "...conformance/executor/scenarios"`).
- `sdk/go/server`: `executors/stub/cmd/main.go` (uses `server.`) and `stores/stub/server/server.go` (aliased `bridge "...server"`).
- `sdk/go/testpg`: `internal/pgmigrate/migrate.go`.
- `sdk/go/ops`: `stores/stub/cmd/main.go`.

**`make` targets that hardcode `sdk/go`** (`Makefile`): the `lint`, `test-all`, and `build-all` targets each contain a `cd sdk/go && …` line. These break the moment `sdk/go` is deleted and MUST be retargeted to `testpg` in the same pass.

**Module wiring** (`go.mod` files): root `go.mod` has `require` + `replace` entries for `foundation`, `protocols`, and `sdk/go` (`replace … /sdk/go => ./sdk/go`). `sdk/go/go.mod` has `replace … /protocols => ../../protocols`. `protocols/go.mod` currently requires only grpc + protobuf.

**Verification commands** (from the project's "After Code Changes" rule):
- `make build-all` — `go build ./...` in root + each module.
- `make test-all` — `go test ./...` in root + each module (testcontainer-backed tests need Docker running).
- `make lint` — `golangci-lint run` in root + each module.
- `make license-lint` — `go run ./cmd/rimsky-license-check` (header + import-direction boundary).
- `make license-stamp` — `go run ./cmd/rimsky-license-check --stamp` (idempotent; re-stamps headers per `licensing.yml`).

---

## Pass 1: Move `action` into `protocols`

**Goal:** Relocate the claim-producer action vocabulary from `sdk/go/stores/action` to `protocols/action`, repoint all 13 importers, and pull `gopkg.in/yaml.v3` into `protocols/go.mod`.
**Scope:** Tasks 1–3
**End state:** working
**Verification:** `cd protocols && go mod tidy && cd .. && make build-all && make test-all`

### Task 1: Relocate the package

**Files:** `sdk/go/stores/action/{action.go,yaml.go,parity_test.go,yaml_test.go}` → `protocols/action/`

**Steps:**
1. `git mv sdk/go/stores/action protocols/action`. (This moves the four files; the `stores/` intermediate directory under `sdk/go` is left empty — remove it with `rmdir sdk/go/stores` if empty.)
2. Confirm the package declaration in each moved file is still `package action` (no change needed — the package name is unchanged, only its module/import path).
3. Run `grep -rn 'rimsky/sdk/go' protocols/action/` to confirm the moved files do not import any other `sdk/go` package (they should not). If any appear, note them — they indicate a cross-package dependency that this pass must also resolve.

**Verification:** `ls protocols/action/action.go` succeeds and `git status` shows the move.

### Task 2: Add `yaml.v3` to `protocols/go.mod` and repoint importers

**Files:** `protocols/go.mod`, the 13 importer files listed under "Verified importer sites"

**Steps:**
1. Re-derive the importer list: `grep -rl 'rimsky/sdk/go/stores/action' --include='*.go' .` (exclude the now-moved `protocols/action` files themselves).
2. In each importer, rewrite the import path `github.com/fallguyconsulting/rimsky/sdk/go/stores/action` → `github.com/fallguyconsulting/rimsky/protocols/action`. The package identifier used in code stays `action` (unchanged), so only the import line changes.
3. Run `cd protocols && go mod tidy`. This adds `gopkg.in/yaml.v3` (required by `protocols/action/yaml.go`) to `protocols/go.mod`. Confirm with `grep yaml.v3 protocols/go.mod`.

**Verification:** `grep -rn 'rimsky/sdk/go/stores/action' --include='*.go' .` returns nothing.

### Task 3: Tidy the vacated module and build

**Files:** `sdk/go/go.mod`

**Steps:**
1. Run `cd sdk/go && go mod tidy` to drop any requires that `action`'s departure made unused.
2. Run `make build-all && make test-all`.

**Verification:** `make build-all && make test-all` exits 0.

---

## Pass 2: Move `conformance` into `protocols`

**Goal:** Relocate the entire conformance library from `sdk/go/conformance` to `protocols/conformance`, repoint the 7 conformance cmd binaries, and pull `github.com/google/uuid` into `protocols/go.mod`. The `@concept: conformance` annotation rides along with the file move (no edit).
**Scope:** Tasks 4–6
**End state:** working
**Verification:** `cd protocols && go mod tidy && cd .. && make build-all && make test-all`

### Task 4: Relocate the conformance tree

**Files:** `sdk/go/conformance/**` → `protocols/conformance/**`

**Steps:**
1. `git mv sdk/go/conformance protocols/conformance`.
2. Rewrite intra-conformance import paths: `grep -rl 'rimsky/sdk/go/conformance' protocols/conformance/` then in each, rewrite `…/sdk/go/conformance/<x>` → `…/protocols/conformance/<x>`.
3. Run `grep -rn 'rimsky/sdk/go' protocols/conformance/`. The conformance tree is self-contained — it imports only its own sub-packages (`conformance/executor`, `conformance/executor/scenarios`), which step 2 already rewrote; it does not import `server`, `publisher`, `action`, `ops`, or `testpg`. After step 2 this grep should return nothing. If it surfaces any unexpected non-conformance `sdk/go` import, rewrite it to that package's new home and re-run the build.

**Verification:** `git status` shows the conformance tree under `protocols/conformance/`.

### Task 5: Repoint the 7 conformance cmd binaries

**Files:** `cmd/rimsky-{blob-backend,claim-producer,conformance-probe,data-processing,executor,publisher,validation}-conformance/main.go` (and any `main_test.go` in those dirs)

**Steps:**
1. Re-derive: `grep -rl 'rimsky/sdk/go/conformance' --include='*.go' cmd/`.
2. In each, rewrite `…/sdk/go/conformance/<x>` → `…/protocols/conformance/<x>`. Preserve existing import aliases (e.g. `conformance "…"`, `_ "…/scenarios"`).
3. Run `cd protocols && go mod tidy` (adds `github.com/google/uuid`). Confirm `grep 'google/uuid' protocols/go.mod`.

**Verification:** `grep -rn 'rimsky/sdk/go/conformance' --include='*.go' .` returns nothing.

### Task 6: Tidy and build

**Steps:**
1. `cd sdk/go && go mod tidy`.
2. `make build-all && make test-all`.

**Verification:** `make build-all && make test-all` exits 0.

---

## Pass 3: Move `server`→`serverkit` and `publisher`→`publisherkit`

**Goal:** Relocate the two convenience-scaffolding packages into `protocols` with their `kit`-suffixed names, renaming the package identifiers, and repoint the 2 `server` importers (`publisher` has none).
**Scope:** Tasks 7–9
**End state:** working
**Verification:** `make build-all && make test-all`

### Task 7: Move and rename `server` → `serverkit`

**Files:** `sdk/go/server/{bridge.go,lifecycle.go,observability.go,bridge_test.go}` → `protocols/serverkit/`

**Steps:**
1. `git mv sdk/go/server protocols/serverkit`.
2. In every moved file, change the package declaration `package server` → `package serverkit`.
3. `grep -rn 'rimsky/sdk/go' protocols/serverkit/` — rewrite any intra-`sdk/go` imports to their new homes (`action`, `conformance` already moved).
4. Repoint importers: `executors/stub/cmd/main.go` imports the package unaliased (`server.X`) — rewrite the import path to `…/protocols/serverkit` and every `server.` reference to `serverkit.`. `stores/stub/server/server.go` imports it aliased (`bridge "…/sdk/go/server"`) — rewrite only the import path to `…/protocols/serverkit`; the `bridge.` references are unaffected.

**Verification:** `grep -rn 'rimsky/sdk/go/server' --include='*.go' .` returns nothing.

### Task 8: Move and rename `publisher` → `publisherkit`

**Files:** `sdk/go/publisher/{publisher.go,publisher_test.go}` → `protocols/publisherkit/`

**Steps:**
1. `git mv sdk/go/publisher protocols/publisherkit`.
2. Change `package publisher` → `package publisherkit` in both files.
3. Re-derive importers: `grep -rl 'rimsky/sdk/go/publisher' --include='*.go' .` (expected: none in-repo). Repoint any that appear.

**Verification:** `grep -rn 'rimsky/sdk/go/publisher' --include='*.go' .` returns nothing.

### Task 9: Tidy and build

**Steps:**
1. `cd sdk/go && go mod tidy`; `cd protocols && go mod tidy`.
2. `make build-all && make test-all`.

**Verification:** `make build-all && make test-all` exits 0.

---

## Pass 4: Carve the `testpg` module, relocate `ops`, delete `sdk/go`, rewire build + lint + licensing

**Goal:** Everything that must change atomically when `sdk/go` ceases to exist: create the standalone `testpg` module, move `ops` to `internal/ops` (flipping its headers to AGPL), repoint the `foundation/internal/pgtest` `@source` annotations, delete the `sdk/go` module, and update `go.work`, the root `go.mod`, the `Makefile` multi-module targets, `licensing.yml`, and the `.golangci.yml` depguard rules — so the whole workspace builds, tests, lints, and passes the license boundary against the new four-module topology. The lint-rule retarget lands in this pass (not a later one) because deleting `sdk/go` and introducing `testpg` + `internal/ops` breaks `make lint`/`make license-lint` until the rules and headers are updated; leaving them red across a pass boundary is the ordering inversion this plan must avoid.
**Scope:** Tasks 10–18
**End state:** working
**Verification:** `make build-all && make test-all && make lint && make license-lint && (make license-stamp && git diff --quiet)`

### Task 10: Create the `testpg` module

**Files:** `testpg/{testpg.go,testpg_test.go}` (moved), `testpg/go.mod` (new)

**Steps:**
1. `git mv sdk/go/testpg testpg` (moves `testpg.go` + `testpg_test.go` to the repo-root `testpg/` directory).
2. Confirm the package declaration stays `package testpg`.
3. Create `testpg/go.mod` with module path `github.com/fallguyconsulting/rimsky/testpg`, `go 1.25.0`, and the three required deps the helper imports: `github.com/testcontainers/testcontainers-go`, `github.com/testcontainers/testcontainers-go/modules/postgres`, `github.com/jackc/pgx/v5`. No `replace` directive (testpg imports no rimsky package).
4. Run `cd testpg && go mod tidy` to populate indirect requires + `go.sum`.

**Verification:** `cd testpg && go build ./... && go test ./...` exits 0 (Docker must be running for the test).

### Task 11: Update `go.work` and the root `go.mod`

**Files:** `go.work`, `go.mod`

**Steps:**
1. In `go.work`, change the `use` block from `. ./foundation ./protocols ./sdk/go` to `. ./foundation ./protocols ./testpg`.
2. In root `go.mod`: remove the `require` line `github.com/fallguyconsulting/rimsky/sdk/go v0.0.0` and the `replace` line `github.com/fallguyconsulting/rimsky/sdk/go => ./sdk/go`. Add a `require` `github.com/fallguyconsulting/rimsky/testpg v0.0.0` and a `replace` `github.com/fallguyconsulting/rimsky/testpg => ./testpg` (root's `internal/pgmigrate` will import it in Task 12).

**Verification:** `grep -c 'sdk/go' go.work go.mod` returns 0 for both.

### Task 12: Repoint all `testpg` references and tidy the root module

**Files:** `internal/pgmigrate/migrate.go`, `foundation/internal/pgtest/pgtest.go`, `go.mod`

**Steps:**
1. In `internal/pgmigrate/migrate.go`, rewrite the import `github.com/fallguyconsulting/rimsky/sdk/go/testpg` → `github.com/fallguyconsulting/rimsky/testpg`. The package identifier `testpg` is unchanged. Update the prose doc-comment references that say `pkg:sdk/go/testpg` / `sdk/go/testpg` to `testpg` (near the package doc and the `OpenDriver` comment).
2. In `foundation/internal/pgtest/pgtest.go`, repoint **both** `@source:` annotations (the spec mandates this): `@source: sdk/go/testpg/testpg.go::StartFreshPostgresDSN` (around line 65) → `@source: testpg/testpg.go::StartFreshPostgresDSN`, and `@source: sdk/go/testpg/testpg.go::resolveConnectionString` (around line 128) → `@source: testpg/testpg.go::resolveConnectionString`. This keeps the tracked-duplication pointer valid (the duplication is intentionally retained — `foundation/` does not import the `testpg` module). Find them with `grep -n 'sdk/go/testpg' foundation/internal/pgtest/pgtest.go`.
3. Run `go mod tidy` at the repo root (now that `internal/pgmigrate` imports the `testpg` module and no longer reaches `sdk/go`, this keeps the new `testpg` require and drops the orphaned `sdk/go`-era indirect requires, e.g. the testcontainers/moby tree formerly pulled transitively via `sdk/go`).

**Verification:** `grep -rn 'sdk/go/testpg' internal/pgmigrate/ foundation/internal/pgtest/` returns nothing.

### Task 13: Relocate `ops` to `internal/ops`, flip its headers to AGPL, repoint its consumer

**Files:** `sdk/go/ops/{dsn.go,health.go,slog.go,ops_test.go}` → `internal/ops/`; `stores/stub/cmd/main.go`

**Steps:**
1. `git mv sdk/go/ops internal/ops`.
2. Confirm package stays `package ops`.
3. **Manually rewrite the license header** of each moved file (`internal/ops/{dsn.go,health.go,slog.go,ops_test.go}`). They currently carry the Apache SPDX header (`// Copyright © 2026 Fall Guy Consulting.` then `// SPDX-License-Identifier: Apache-2.0`). The `internal/` prefix is AGPL in `licensing.yml`, so replace that two-line header with the AGPL dual-license block (the form the license-check tool recognizes via the marker `Dual-licensed under AGPL`):
   ```
   // Copyright © 2026 Fall Guy Consulting.
   // Dual-licensed under AGPL-3.0-or-later or a Fall Guy Consulting commercial
   // license. See LICENSE.agpl and COPYRIGHT at the repo root.
   ```
   This must be a manual edit: `make license-stamp` will **not** do it — `stampHeaders` skips any file that already has a header (it only stamps headerless files), so the moved files would otherwise keep their Apache headers and fail `make license-lint` with "expected AGPL dual-license header but found Apache or unknown".
4. Repoint the single consumer `stores/stub/cmd/main.go`: rewrite `…/sdk/go/ops` → `…/internal/ops` (it calls `ops.Setup(slog.LevelInfo)`). `stores/stub/cmd` is matched by the `consumption-side-isolation` depguard rule's `stores/**` target, which denies `internal/` — so this import will trip lint until Task 17 exempts the in-repo stub from that rule. The build (`go build`) compiles regardless; the lint resolution lands in Task 17 within this same pass.

**Verification:** `grep -rn 'sdk/go/ops' --include='*.go' .` returns nothing; `grep -l 'Dual-licensed under AGPL' internal/ops/*.go | wc -l` returns 4.

### Task 14: Delete the `sdk/go` module

**Files:** `sdk/go/` (whatever remains: `doc.go`, `go.mod`, `go.sum`, `README.md`, empty dirs)

**Steps:**
1. Confirm nothing remains under `sdk/go` except `doc.go`, `go.mod`, `go.sum`, `README.md`: `find sdk/go -type f`.
2. `git rm -r sdk/go`. This removes `sdk/go/doc.go`, which carried the `// @concept: sdk` annotation — its disappearance is intentional (the concept is retired in Pass 6).
3. `grep -rn 'rimsky/sdk/go' --include='*.go' .` must return nothing (all code repointed in Passes 1–4).

**Verification:** `test ! -d sdk/go` (the directory is gone) and the grep above returns nothing.

### Task 15: Retarget the `Makefile` multi-module targets and `licensing.yml`

**Files:** `Makefile`, `licensing.yml`

**Steps:**
1. In `Makefile`, find every line containing `cd sdk/go` (`grep -n 'cd sdk/go' Makefile` — includes the `lint`, `test-all`, `build-all` targets and any `lint-docker`/variant target) and replace each with the equivalent `cd testpg && …` line. Update the comment on `test-all`/`build-all` that reads "root + foundation + protocols + sdk/go" to "root + foundation + protocols + testpg".
2. In `licensing.yml` `apache:` block: remove the `- sdk/go/` entry; add `- testpg/` (with a comment noting it is the opt-in adopter-facing Postgres test helper, deps-isolated). Also remove the stale bare `- conformance/` entry (no top-level `conformance/` directory exists — the conformance code now lives under `protocols/conformance/`, covered by the existing `protocols/` Apache prefix).

**Verification:** `grep -n 'sdk/go' Makefile licensing.yml` returns nothing; `grep -n 'testpg' Makefile licensing.yml` shows the new entries.

### Task 16: Verify headers and run `license-stamp` idempotency check

**Files:** `internal/ops/*`, `testpg/*` (headers verified, not re-stamped)

**Steps:**
1. The `internal/ops/*` headers were flipped to AGPL manually in Task 13; the `testpg/*` files already carry Apache headers (correct, since `testpg/` is Apache in `licensing.yml`). Run `make license-stamp` — because every moved file now already has a correct header, the tool is a no-op (it only stamps headerless files). Confirm it changed nothing: `make license-stamp && git diff --quiet && echo CLEAN`.
2. This task's full-boundary verification is the pass-level Verification command below.

**Verification:** `make license-stamp && git diff --quiet` exits 0 (no header drift).

### Task 17: Rewrite the depguard rules in `.golangci.yml`

**Files:** `.golangci.yml`

**Steps:**
1. **`sdk-purity` → `protocols-purity`:** rename the rule key; change its `files:` from `- "**/sdk/go/**"` to `- "**/protocols/**"`; keep the deny list (foundation, internal, graph, runtime, control, cmd) and update each `desc` to say "protocols module" instead of "sdk/go". Add one deny entry for `github.com/testcontainers/testcontainers-go` with desc "the protocols module is the public contract surface; no test infrastructure" (compiler-checks "no test infra in the contract module"). Update the rule's leading comment to describe the protocols module's budget: stdlib + grpc + protobuf + uuid + yaml.v3.
2. **`pgx-isolation`:** in the `files:` exclusion list, replace `- "!**/sdk/go/**"` with `- "!**/testpg/**"`; update the `desc` strings that enumerate allowed locations to name `testpg/` instead of `sdk/go/`. (`protocols/` must NOT be excluded — the contract module stays pgx-free.)
3. **`foundation-purity`:** delete the deny entry whose `pkg:` is `github.com/fallguyconsulting/rimsky/sdk/go`.
4. **`graph-purity`:** delete the deny entry whose `pkg:` is `github.com/fallguyconsulting/rimsky/sdk/go`.
5. **`consumption-side-isolation`:** update the rule's leading comment to drop the "(post-P2) sdk/go" reference (consumption-side binaries implement against `protocols/` only). Then add a `files:` exclusion so the rule no longer matches the in-repo test-infra stub: under the rule's `files:` list (currently `stores/**`, `sensors/**`, `subscribers/**`, `executors/**`), add `- "!stores/stub/**"` and `- "!executors/stub/**"`. Rationale: the rule guards *production* consumption-side binaries (the ones the 2026-05-24 reorganization moved to the sibling repo, so they must implement against `protocols/` only). The `stub` store and stub executor deliberately stayed in rimsky as test infrastructure (Apache-classified rimsky-internal carve-outs per `licensing.yml`), so the `stores/stub/cmd` → `internal/ops` import introduced in Task 13 is legitimate; the bare `stores/**` glob over-matches the stub. This is planning-discovered fallout beyond the spec's enumerated lint edits — record it in the CHANGELOG (Task 26) and the `module-layout` invariant (Task 23). Update the rule's leading comment to note the test-infra-stub exemption.

**Verification:** `grep -n 'sdk/go\|sdk-purity' .golangci.yml` returns nothing; `grep -n 'protocols-purity' .golangci.yml` shows the renamed rule; `grep -n '!stores/stub' .golangci.yml` shows the exemption.

### Task 18: Run the full Pass-4 boundary verification

**Steps:**
1. Run `make build-all && make test-all` — confirms the four-module workspace (root, foundation, protocols, testpg) builds and tests green after the surgery.
2. Run `make lint`. With Task 17 done, `protocols-purity` guards the contract module (it should flag nothing — the moved code imports only stdlib/grpc/protobuf/uuid/yaml; if it flags something, that import is a genuine boundary violation to fix, not a rule problem), `pgx-isolation` excludes `testpg`, and `consumption-side-isolation` exempts the in-repo stub. Lint must be green.
3. Run `make license-lint` — confirms the `internal/ops` AGPL header flip (Task 13) and the `licensing.yml` edits (Task 15) leave the import-direction + header boundary clean.

**Verification:** `make build-all && make test-all && make lint && make license-lint` exits 0.

---

## Pass 5: Close the `foundation/locks` contract-type leak

**Goal:** Remove the 17 `claimproducer` re-exports from `foundation/locks/types.go` and repoint every consumer (58 files outside `foundation/locks/` plus the intra-`locks` references) to import `protocols/claimproducer` directly, so `protocols/claimproducer` is the only Go home for the contract types.
**Scope:** Tasks 19–21
**End state:** working
**Verification:** `make build-all && make test-all && make lint`

### Task 19: Repoint external consumers (`locks.<Symbol>` → `claimproducer.<Symbol>`)

**Files:** the 58 files outside `foundation/locks/` that reference the aliased symbols

**The 17 re-exported symbols** (all in `foundation/locks/types.go`): types `ClaimID`, `Intent`, `ClaimSpec`, `ClaimResult`, `OpenOutcome`, `WriteSemantics`, `Capabilities`, `SplitClaimScopeRequest`, `SplitClaimScopeResponse`, `SubClaimScopeDescriptor`; consts `IntentRead`, `IntentReadWrite`, `WriteSemanticsUnknown`, `WriteSemanticsSync`, `WriteSemanticsStagedAsync`, `WriteSemanticsBlockingAsync`, `WriteSemanticsReadOnly`. Also check for a `ParseWriteSemantics` delegate function — if `foundation/locks` wraps `claimproducer.ParseWriteSemantics`, repoint its callers to `claimproducer.ParseWriteSemantics` and remove the wrapper.

**Steps:**
1. Re-derive the consumer list: `grep -rlE 'locks\.(ClaimID|Intent|ClaimSpec|ClaimResult|OpenOutcome|WriteSemantics|Capabilities|SplitClaimScopeRequest|SplitClaimScopeResponse|SubClaimScopeDescriptor|IntentRead|IntentReadWrite|WriteSemanticsUnknown|WriteSemanticsSync|WriteSemanticsStagedAsync|WriteSemanticsBlockingAsync|WriteSemanticsReadOnly|ParseWriteSemantics)' --include='*.go' . | grep -v '/foundation/locks/'`.
2. In each file, for every reference of the form `locks.<Symbol>` where `<Symbol>` is one of the 17 (or `ParseWriteSemantics`), rewrite it to `claimproducer.<Symbol>`. Add the import `claimproducer "github.com/fallguyconsulting/rimsky/protocols/claimproducer"` if not already present, using whatever alias the file already uses for that package if it imports it. If a file used `locks` *only* for these symbols, remove the now-unused `foundation/locks` import; if it uses `locks` for other things too (e.g. `locks.Registry`), keep the `locks` import.
3. Work in batches by directory (`runtime/`, `control/`, `graph/attribute/`, `cmd/`, `test/scenarios/`) and run `go build ./...` after each batch to catch mistakes early.

**Verification:** `grep -rnE 'locks\.(ClaimID|Intent|ClaimSpec|ClaimResult|OpenOutcome|WriteSemantics|Capabilities|SplitClaimScopeRequest|SplitClaimScopeResponse|SubClaimScopeDescriptor|ParseWriteSemantics)' --include='*.go' . | grep -v '/foundation/locks/'` returns nothing (the `Intent`/`WriteSemantics` alternatives also match the const symbols `IntentRead`/`WriteSemanticsSync`/etc. by substring).

### Task 20: Repoint intra-`locks` references and remove the aliases

**Files:** `foundation/locks/types.go` and the ~5–7 other files in `foundation/locks/` that use the bare aliased names

**Steps:**
1. In the non-`types.go` files under `foundation/locks/` that use the bare names (`ClaimResult`, `WriteSemantics`, `OpenOutcome`, `Capabilities`, etc.), add/confirm an import of `claimproducer "github.com/fallguyconsulting/rimsky/protocols/claimproducer"` and rewrite the bare references to `claimproducer.<Symbol>`. Find them: `grep -rlnE '\b(ClaimID|Intent|ClaimSpec|ClaimResult|OpenOutcome|WriteSemantics|Capabilities|SplitClaimScopeRequest|SplitClaimScopeResponse|SubClaimScopeDescriptor)\b' foundation/locks/ | grep -v types.go`.
2. In `foundation/locks/types.go`, delete every `= claimproducer.<X>` alias and const line plus the surrounding explanatory comments. The 17 re-exports are interleaved with local declarations and doc comments across the file (not one contiguous block) — use `grep -n '= claimproducer\.' foundation/locks/types.go` to locate all of them, and remove the doc comment immediately above each removed declaration. Keep the genuinely-local declarations (e.g. `NamedLockSpec`, `Registry`-related types). Keep the `claimproducer` import only if other code in `types.go` still uses it; otherwise remove it.
3. Run `cd foundation && go build ./... && go vet ./...`.

**Verification:** `grep -nE '= claimproducer\.' foundation/locks/types.go` returns nothing.

### Task 21: Full build, test, lint

**Steps:**
1. `make build-all && make test-all && make lint`.

**Verification:** all three exit 0.

---

## Pass 6: Design-doc mutations + CHANGELOG

**Goal:** Apply the spec's `## Design changes` to the concept catalog (retire `concept:sdk`, mutate `concept:module-layout` and `concept:conformance`), refresh the auto-generated TOC, and record the change in `CHANGELOG.md`. These design-doc edits ship with the code per the project's design-docs discipline.
**Scope:** Tasks 22–26
**End state:** working
**Verification:** `make build-all && make lint` (no code impact expected) plus the grep checks below.

> For each concept mutation, the exact current text, new text, and Notes entries are specified in the spec's `## Design changes` section (`.ok-planner/specs/2026-05-26-collapse-sdk-into-protocols-design.md`). Apply them verbatim. All new concept-body text in that section is already written path-free per the concept self-containment rule — do not introduce file paths into concept bodies.

### Task 22: Retire `concept:sdk`

**Files:** `.ok-planner/design/concepts/sdk.md` → `.ok-planner/design/concepts/_retired/sdk.md`

**Steps:**
1. `git mv .ok-planner/design/concepts/sdk.md .ok-planner/design/concepts/_retired/sdk.md`.
2. In the moved file's frontmatter, change `status: as-is` to `status: retired`.
3. Add a `> **Retired** by …` blockquote at the top of the body (matching the format of existing files in `_retired/`, e.g. `schedule.md`), summarizing: the Go SDK module dissolved into the protocols module; scaffolding → `serverkit`/`publisherkit`, conformance library and action vocab into protocols, ops demoted to a rimsky-internal package, the testcontainer helper carved into its own opt-in module; for Go there is no separate SDK — the protocols module is the single public surface; a future development kit is a different-purpose, Python-first authoring layer above the contract, not a Go SDK successor and not a satellite Go module. Use the exact retirement-note text from the spec's "retire `concept:sdk`" Design-changes bullet (path-free).

**Verification:** `test -f .ok-planner/design/concepts/_retired/sdk.md && test ! -f .ok-planner/design/concepts/sdk.md`.

### Task 23: Mutate `concept:module-layout`

**Files:** `.ok-planner/design/concepts/module-layout.md`

**Steps:**
1. Apply each sub-bullet from the spec's "mutate `concept:module-layout`" Design-changes entry: replace the SDK-module bullet with the Postgres-test-helper-module bullet; expand the Protocols-module bullet (dependency budget: stdlib + gRPC + protobuf + UUID + YAML); update the Root-module plain-Postgres-fixture sentence; rewrite the Purpose sentence; drop `sdk` from Boundaries Adjacent and remove the locks-aliasing clause; replace the `sdk-purity` invariant with the `protocols-purity` invariant; update the `pgx-isolation` invariant to name the test-helper module; delete the "or the SDK" clauses in foundation-purity/graph-purity invariants; fix the control-layer four-module parenthetical; rewrite the layer-ordering SDK sentence.
2. In the `consumption-side-isolation` invariant (the sentence describing that rule), append a clause noting the in-repo test-infra stub exemption — keep it path-free, e.g.: "the in-repo stub claim-producer and stub executor (test-infrastructure carve-outs that stayed in rimsky) are exempt, so they may consume rimsky-internal helpers." This keeps the design doc truthful to the `.golangci.yml` change made in Task 17 (the exemption was planning-discovered fallout, not in the spec's enumerated lint edits).
3. Append the dated Notes entry from the spec, extending it to mention the `consumption-side-isolation` stub exemption.

**Verification:** `grep -in 'sdk-purity\|SDK module\|sdk/go' .ok-planner/design/concepts/module-layout.md` returns nothing (all SDK references replaced).

### Task 24: Mutate `concept:conformance`

**Files:** `.ok-planner/design/concepts/conformance.md`

**Steps:**
1. Apply the spec's "mutate `concept:conformance`" sub-bullets: "shared SDK conformance library" → "shared conformance library in the protocols module"; "The SDK conformance library lives in the peer Go module" → "lives in the protocols module"; blob-backend "SDK's reduced backend interface … SDK-purity-clean" → "conformance library's reduced backend interface … protocols-purity-clean"; the invariant compile-time-dependency change; drop `sdk` from Adjacent; "Owns: the SDK conformance library" → "Owns: the conformance library" and "in the SDK library" → "in the conformance library".
2. Append the dated Notes entry from the spec.

**Verification:** `grep -in 'SDK' .ok-planner/design/concepts/conformance.md` returns nothing.

### Task 25: Refresh the concept TOC

**Files:** `.ok-planner/design/concepts.md`

**Steps:**
1. Remove the `- \`sdk\` (aliases: …) — …` line from the `## Concepts` section.
2. Add an `- \`sdk\` — …` line to the `## Retired concepts` section with a one-sentence retirement summary (mirroring the existing retired-concept lines).
3. If the headline one-line definitions for `module-layout` or `conformance` changed materially (e.g. conformance's "shared SDK conformance library" wording), update those lines to match the new concept bodies.

**Verification:** `grep -n '`sdk`' .ok-planner/design/concepts.md` shows it only under "## Retired concepts".

### Task 26: CHANGELOG

**Files:** `CHANGELOG.md`

**Steps:**
1. Append a bullet under `## Unreleased` describing the change and rationale: `sdk/go` module collapsed into `protocols` (single public Go module for service implementers); `action`/`conformance`/`serverkit`/`publisherkit` moved into `protocols` (protocols gains direct deps `google/uuid` and `gopkg.in/yaml.v3`); `ops` demoted to `internal/ops` (headers flipped Apache→AGPL); `testpg` carved into its own opt-in module; `foundation/locks` contract-type re-exports removed (canonical home `protocols/claimproducer`); `sdk-purity` lint rule renamed to `protocols-purity`; `consumption-side-isolation` rule exempted for the in-repo test-infra stub (so it may import `internal/ops`); licensing map updated (drop `sdk/go`, add `testpg`, remove stale `conformance/` entry).

**Verification:** `grep -n 'protocols' CHANGELOG.md` shows the new entry under Unreleased.

---

## Final verification (run after all passes)

Run the complete suite the project's "After Code Changes" rule requires:

```
make build-all && make test-all && make lint && make license-lint
```

Then confirm `make license-stamp` produces no diff (`make license-stamp && git diff --quiet`), the conformance binaries build and run (`go build ./cmd/rimsky-executor-conformance ./cmd/rimsky-claim-producer-conformance && …`), and the scenario + storage suites pass (`go test ./test/scenarios/... ./foundation/persistence/... -count=1`, Docker required).

## Manual checks after completion

None. All verification in this plan is expressible as commands. (The downstream external-consumer migration and the deferred `internal/`-fencing of `foundation` are out-of-scope follow-ups per the spec, not part of this run.)
