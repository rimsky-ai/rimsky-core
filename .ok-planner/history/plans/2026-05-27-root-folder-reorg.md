# Root-Folder Reorganization Implementation Plan

**Spec:** none (this plan was produced from an interactive design conversation, not `/brainstorm`. All design rationale needed to execute is captured inline below.)
**Goal:** Collapse the cluttered repo root into four idiomatic top-level code directories — `cmd/` (binaries), `lib/` (shippable library code), `test/` (out-of-tree tests + their machinery), `tools/` (dev tooling) — with no `src/`, no top-level `internal/`, no standalone `testpg` module, and no dead code.
**Architecture:** Rimsky is a Go multi-module workspace tied together by `go.work`. Today the workspace has four modules: root (`github.com/rimsky-ai/rimsky-core`, holding `graph/ runtime/ control/ cmd/ internal/ stores/ executors/ test/`), `foundation/`, `protocols/`, and `testpg/`. This reorg moves the two library sub-modules (`protocols`, `foundation`) and the root-module library packages (`graph`, `runtime`, `control`) under `lib/`; consolidates all test-only scaffolding under `test/support/`; demotes `testpg` from a standalone module to a plain package under `test/support/`; deletes the dead `internal/ops` package; folds the seven standalone conformance binaries into `rimsky conformance <protocol>` subcommands; relocates `control/cli` out of the control service into the CLI binary; and moves the `license-check` linter to `tools/`. After the reorg the workspace has three modules: root, `lib/foundation`, `lib/protocols`.
**Tech Stack:** Go 1.25, `go.work` multi-module workspace, `golangci-lint` (depguard import-boundary rules), a custom `license-check` linter driven by `licensing.yml`, Docker multi-stage image builds.

---

## Design decisions captured from the conversation

These were settled with the user; do not re-litigate them.

- **Pre-v1, break freely.** Per `.claude/rules/rules.md`, there are no backwards-compat guarantees. This reorg intentionally changes the public Go import paths of `protocols` and `foundation` (they gain a `lib/` segment: `github.com/rimsky-ai/rimsky-core/lib/protocols`) and drops `testpg` as an importable module. That is accepted.
- **`cmd/` stays at the repo root** (idiomatic Go: one top-level dir for all `main` packages). It is NOT moving under `lib/`. The daemon binaries (`rimsky-supervisor`, `rimsky-scheduler`, `rimsky-control-api`, `rimsky-host-agent`, `rimsky-host-agent-proxy`, `rimsky-entrypoint`, `rimsky-migrate`) keep their flat `cmd/<name>` locations; only their import statements change.
- **`internal/ops` is dead** — its consumers (bundled service binaries) left the repo in a prior split. It has zero importers and is to be deleted, not relocated. Pass 1 verifies this before deleting.
- **`internal/pgmigrate` is a test harness, not a migration runner.** The real migration runners live in `foundation/persistence/{postgres,sqlite}/migrate.go` and are unaffected. `pgmigrate` is test-only scaffolding and moves to `test/support/pgmigrate`.
- **`testpg` is demoted.** It is a standalone Postgres-testcontainer helper whose only in-repo consumer is `pgmigrate`. It is NOT a product; it ceases to be a public module and becomes a plain package at `test/support/testpg` inside the root module. Its source files get reclassified Apache→AGPL (Pass 7 re-stamps headers).
- **Conformance runners become CLI subcommands** (`rimsky conformance executor …` etc.), replacing the seven standalone `cmd/rimsky-*-conformance` binaries. This changes the `rimsky-conformance` image's command surface and the `CLAUDE.md` verify-rule invocation; both are updated.
- **`graph/scenario` is test-only infra** (≈100 `_test.go` importers, zero production importers) and moves to `test/support/scenario`.

## Target tree (end state)

```
cmd/                         # binaries (unchanged location)
  rimsky/                    #   CLI: absorbs control/cli + gains `conformance` subcommands
    cli/                     #   moved from control/cli (package cli, + roles/, internal/clitest/)
  rimsky-supervisor/  rimsky-scheduler/  rimsky-control-api/
  rimsky-host-agent/  rimsky-host-agent-proxy/  rimsky-entrypoint/  rimsky-migrate/
lib/
  protocols/                 # was ./protocols (module path -> .../lib/protocols)
  foundation/                # was ./foundation (module path -> .../lib/foundation)
  graph/  runtime/  control/ # were root-module packages (control/cli removed)
test/
  scenarios/  smoke/         # existing out-of-tree tests (stay)
  support/
    testpg/                  # was ./testpg (demoted from module to package)
    pgmigrate/               # was ./internal/pgmigrate
    stores/                  # was ./stores
    executors/               # was ./executors
    scenario/                # was ./graph/scenario
tools/
  license-check/             # was ./cmd/rimsky-license-check
(machinery at root: Makefile, dockerfiles/, .golangci.yml, cold-read/, README.md, licensing.yml, legal files, go.* )
```

## Mechanics & conventions used throughout this plan

- **Directory moves use plain `mv`, then `git add -A`.** Plain `mv` relocates *all* files including generated/git-ignored ones (e.g. `protocols/proto/v1/gen/`), which `git mv` would leave behind. `git add -A` then records the rename and checkpoints the work into the index (this is staging, NOT committing — do not commit; the user owns git).
- **Import-path rewrites use `perl -pi -e` over `*.go` files.** `perl` is used (not `sed -i`) to avoid BSD/GNU `sed` divergence on macOS. Always scope to Go files: `find . -name '*.go' -not -path './.git/*' -print0 | xargs -0 perl -pi -e '...'`. The rewrite strings are full module paths (`github.com/rimsky-ai/rimsky-core/...`), which appear only in import blocks and path-naming comments, so the replacement is safe. `go.mod`, `go.work`, and `.golangci.yml` are edited explicitly (not by the `*.go` rewrite).
- **`golangci-lint` analyzes `_test.go` files** (staticcheck/govet/unused do), so `make lint` is a reliable proxy for "test files still compile" without running the slow testcontainer suites. Intermediate passes gate on `make build-all && make lint`; the expensive `make test-all` runs at the behaviorally-significant boundaries (Passes 4, 6, 8).
- **`make build-all`, `make test-all`, and `make lint` `cd` into each module directory** (`foundation`, `protocols`, `testpg`). These Makefile module paths are updated in the same pass that moves the corresponding module, so `make` stays usable as the per-pass gate.
- **`licensing.yml`, the `license-check` linter, the Dockerfiles, `.gitignore`, and the human docs do not affect `make build-all`/`make lint`.** Their edits are consolidated into Passes 7 (config/build) and 8 (docs). `make license-lint` is only gated from Pass 7 onward, because it would otherwise fail on moved-but-not-yet-reclassified paths.
- After any pass that changes a module's dependency graph, run `go mod tidy` in each affected module directory and `go work sync` to reconcile `go.sum`/`go.work.sum`.

---

## Pass 1: Delete the dead `internal/ops` package

**Goal:** Remove `internal/ops`, confirmed to have zero importers, so the reorg doesn't carefully relocate a corpse.
**Scope:** Tasks 1–2
**End state:** working
**Verification:** `make build-all && make lint`

### Task 1: Confirm `internal/ops` is unreferenced

**Files:** none (read-only check)

**Steps:**
1. Run `grep -rn 'rimsky-core/internal/ops' --include='*.go' . | grep -v '/.ok-planner/'`. Expect **no output**. If there is any output, STOP — the premise is wrong; the importer must be understood before proceeding (the package is not dead).
2. Run `ls internal/ops/` and confirm it contains only operational helpers (`dsn.go`, `health.go`, `slog.go`, `ops_test.go`). Confirm `internal/pgmigrate/` also exists (it stays for now; it moves in Pass 4).

**Verification:** `grep -rn 'rimsky-core/internal/ops' --include='*.go' . | grep -v '/.ok-planner/'` prints nothing.

### Task 2: Delete `internal/ops`

**Files:** `internal/ops/dsn.go`, `internal/ops/health.go`, `internal/ops/slog.go`, `internal/ops/ops_test.go` (and any other files in that directory)

**Steps:**
1. `rm -rf internal/ops` then `git add -A`.
2. Build and lint to confirm nothing depended on it.

**Verification:** `make build-all && make lint` both exit 0.

---

## Pass 2: Move `protocols` and `foundation` modules into `lib/`

**Goal:** Relocate the two library sub-modules under `lib/`, updating their module paths, the workspace file, the replace directives, every import site (≈165 + ≈451 files), the depguard entries that name them, and the Makefile module paths.
**Scope:** Tasks 3–9
**End state:** working
**Verification:** `make build-all && make lint`

### Task 3: Physically move the two module directories

**Files:** `protocols/` → `lib/protocols/`, `foundation/` → `lib/foundation/`

**Steps:**
1. `mkdir -p lib`
2. `mv protocols lib/protocols`
3. `mv foundation lib/foundation`
4. `git add -A`

**Verification:** `test -d lib/protocols/proto/v1 && test -d lib/foundation/persistence && echo OK` prints `OK`; `test ! -e protocols && test ! -e foundation && echo gone` prints `gone`.

### Task 4: Update the three module manifests and the workspace file

**Files:** `lib/protocols/go.mod`, `lib/foundation/go.mod`, `go.mod`, `go.work`

**Steps:**
1. In `lib/protocols/go.mod`, change the module line to:
   ```
   module github.com/rimsky-ai/rimsky-core/lib/protocols
   ```
2. In `lib/foundation/go.mod`: change the module line to `module github.com/rimsky-ai/rimsky-core/lib/foundation`; change the `require` entry `github.com/rimsky-ai/rimsky-core/protocols v0.0.0` to `github.com/rimsky-ai/rimsky-core/lib/protocols v0.0.0`; change the replace line `replace github.com/rimsky-ai/rimsky-core/protocols => ../protocols` to `replace github.com/rimsky-ai/rimsky-core/lib/protocols => ../protocols` (the relative `../protocols` is correct: from `lib/foundation/`, `../protocols` resolves to `lib/protocols/`).
3. In the root `go.mod` `require` block, change `github.com/rimsky-ai/rimsky-core/foundation v0.0.0` → `github.com/rimsky-ai/rimsky-core/lib/foundation v0.0.0` and `github.com/rimsky-ai/rimsky-core/protocols v0.0.0` → `github.com/rimsky-ai/rimsky-core/lib/protocols v0.0.0`. Leave the `testpg` require untouched (it goes in Pass 4).
4. In the root `go.mod` `replace` block, change the two lines to:
   ```
   github.com/rimsky-ai/rimsky-core/lib/foundation => ./lib/foundation
   github.com/rimsky-ai/rimsky-core/lib/protocols => ./lib/protocols
   ```
   Leave the `testpg` replace untouched.
5. In `go.work`, change `./foundation` → `./lib/foundation` and `./protocols` → `./lib/protocols`. Leave `./testpg`. Result:
   ```
   use (
       .
       ./lib/foundation
       ./lib/protocols
       ./testpg
   )
   ```

**Verification:** `grep -n 'lib/foundation\|lib/protocols' go.work go.mod lib/foundation/go.mod lib/protocols/go.mod` shows the updated paths.

### Task 5: Rewrite every Go import of `protocols` and `foundation` (and the `.proto` `go_package` options)

**Files:** all `*.go` files repo-wide; the ten `lib/protocols/proto/v1/*.proto` files

**Steps:**
1. Run:
   ```
   find . -name '*.go' -not -path './.git/*' -print0 | xargs -0 perl -pi -e \
     's{github\.com/rimsky-ai/rimsky-core/foundation}{github.com/rimsky-ai/rimsky-core/lib/foundation}g; s{github\.com/rimsky-ai/rimsky-core/protocols}{github.com/rimsky-ai/rimsky-core/lib/protocols}g;'
   ```
   This fixes the generated bindings' Go import statements (the `.pb.go` files) as well as hand-written source.
2. Rewrite the `option go_package = "…"` line in the proto sources so the next `make proto-gen` regenerates bindings under the correct import path (the `*.go` rewrite above does NOT touch `.proto` files):
   ```
   find lib/protocols/proto/v1 -name '*.proto' -print0 | xargs -0 perl -pi -e \
     's{github\.com/rimsky-ai/rimsky-core/protocols/proto/v1/gen}{github.com/rimsky-ai/rimsky-core/lib/protocols/proto/v1/gen}g;'
   ```
3. `git add -A`

**Verification:** `grep -rn 'rimsky-core/foundation"\|rimsky-core/protocols"' --include='*.go' . | grep -v '/.ok-planner/' | grep -v '/lib/'` prints nothing (no un-prefixed imports remain — note the trailing `"` and the `lib/` filter to avoid matching the already-correct `lib/foundation`/`lib/protocols`); and `grep -rn 'rimsky-core/protocols/proto/v1/gen' lib/protocols/proto/v1/*.proto` prints nothing (all ten `go_package` options now carry the `lib/` segment).

### Task 6: Update depguard entries that name `foundation`/`protocols`

**Files:** `.golangci.yml`

**Steps:**
1. In the `foundation-internal-isolation` rule, change the deny `pkg:` `github.com/rimsky-ai/rimsky-core/foundation/internal` → `github.com/rimsky-ai/rimsky-core/lib/foundation/internal`.
2. In the `protocols-purity` rule, change the deny `pkg:` `github.com/rimsky-ai/rimsky-core/foundation` → `github.com/rimsky-ai/rimsky-core/lib/foundation`.
3. In the `foundation-purity` rule, leave the `**/foundation/**` `files:` glob as-is (the `**/` prefix still matches `lib/foundation/**`). No deny entries in this rule name foundation/protocols.
4. In the `consumption-side-isolation` rule, change the deny `pkg:` `github.com/rimsky-ai/rimsky-core/foundation` → `github.com/rimsky-ai/rimsky-core/lib/foundation`.
5. Do NOT touch the `**/foundation/**`, `**/protocols/**`, `**/foundation/persistence/postgres/**`, or `**/foundation/internal/pgtest/**` `files:` globs — they are `**/`-anchored and continue to match under `lib/`. (The final `make lint` gate confirms this.)

**Verification:** none individually; covered by the pass gate.

### Task 7: Update Makefile module paths (foundation/protocols only) and the protocols npm metadata

**Files:** `Makefile`, `lib/protocols/package.json`

**Steps:**
0. In `lib/protocols/package.json`, update the package metadata that names the old subdirectory: `"directory": "protocols"` → `"directory": "lib/protocols"`, and the `"homepage"` URL's trailing `/tree/main/protocols` → `/tree/main/lib/protocols`. (These feed `npm publish` provenance; the relocated package would otherwise advertise a path that no longer exists.)
1. In `proto-gen`, change `cd protocols/proto/v1` → `cd lib/protocols/proto/v1`.
2. In `lint`, change `cd foundation` → `cd lib/foundation` and `cd protocols` → `cd lib/protocols`. Leave the `cd testpg` line.
3. In `test-all` and `build-all`, change `cd foundation` → `cd lib/foundation` and `cd protocols` → `cd lib/protocols`. Leave the `cd testpg` lines.
4. In `lint-docker`, change `cd foundation` → `cd lib/foundation` and `cd ../protocols` → `cd ../lib/protocols` (mind the relative `cd` chaining: after `cd lib/foundation` the next `cd` is relative to it — rewrite the chain so each module is reached correctly, e.g. `cd /src/lib/foundation && … && cd /src/lib/protocols && … && cd /src/testpg && …` using absolute `/src` paths, since these run in the Docker `/src` workdir). Leave the `testpg` segment.
5. In `publish-protocols`, change `cd protocols && npm publish` → `cd lib/protocols && npm publish`.

**Verification:** `make build-all` and `make lint` both run without "no such file or directory" on the module `cd`s (covered by the pass gate).

### Task 8: Tidy and sync modules

**Files:** `go.sum`, `go.work.sum`, `lib/foundation/go.sum`, `lib/protocols/go.sum`

**Steps:**
1. `(cd lib/protocols && go mod tidy)`
2. `(cd lib/foundation && go mod tidy)`
3. `go mod tidy`
4. `go work sync`
5. `git add -A`

**Verification:** `go build ./...` exits 0.

### Task 9: Pass-2 gate

**Files:** none

**Steps:**
1. Run `make build-all && make lint`.

**Verification:** `make build-all && make lint` exits 0.

---

## Pass 3: Move `graph`, `runtime`, `control` into `lib/`

**Goal:** Relocate the three root-module library packages under `lib/`, rewriting every import and the depguard deny entries that name them. `cmd/` stays at root. `graph/scenario` moves to `lib/graph/scenario` for now (it relocates to `test/support/` in Pass 4).
**Scope:** Tasks 10–14
**End state:** working
**Verification:** `make build-all && make lint`

### Task 10: Physically move the three package directories

**Files:** `graph/` → `lib/graph/`, `runtime/` → `lib/runtime/`, `control/` → `lib/control/`

**Steps:**
1. `mv graph lib/graph`
2. `mv runtime lib/runtime`
3. `mv control lib/control`
4. `git add -A`

**Verification:** `test -d lib/graph && test -d lib/runtime && test -d lib/control && test ! -e graph && test ! -e runtime && test ! -e control && echo OK` prints `OK`.

### Task 11: Rewrite every Go import of `graph`/`runtime`/`control`

**Files:** all `*.go` files repo-wide

**Steps:**
1. Run:
   ```
   find . -name '*.go' -not -path './.git/*' -print0 | xargs -0 perl -pi -e \
     's{github\.com/rimsky-ai/rimsky-core/graph}{github.com/rimsky-ai/rimsky-core/lib/graph}g; s{github\.com/rimsky-ai/rimsky-core/runtime}{github.com/rimsky-ai/rimsky-core/lib/runtime}g; s{github\.com/rimsky-ai/rimsky-core/control}{github.com/rimsky-ai/rimsky-core/lib/control}g;'
   ```
   (No prefix collisions: `control/cli` and `control/controlapi` correctly become `lib/control/cli` and `lib/control/controlapi`; `graph/scenario` correctly becomes `lib/graph/scenario`.)
2. `git add -A`

**Verification:** `grep -rn 'rimsky-core/\(graph\|runtime\|control\)' --include='*.go' . | grep -v '/.ok-planner/' | grep -v 'rimsky-core/lib/'` prints nothing.

### Task 12: Update depguard entries that name `graph`/`runtime`/`control`

**Files:** `.golangci.yml`

**Steps:**
1. In `protocols-purity`, change deny `pkg:` entries `.../graph` → `.../lib/graph`, `.../runtime` → `.../lib/runtime`, `.../control` → `.../lib/control`. Leave `.../cmd` and `.../internal` unchanged (cmd stays; `internal` is handled in Pass 4).
2. In `foundation-purity`, change deny `pkg:` `.../graph` → `.../lib/graph`, `.../runtime` → `.../lib/runtime`, `.../control` → `.../lib/control`. Leave `.../cmd`, `.../stores`, `.../executors` (the last two move in Pass 4).
3. In `graph-purity`: change deny `pkg:` `.../runtime` → `.../lib/runtime`, `.../control` → `.../lib/control`. Leave the `**/graph/**`, `!**/graph/scenario/**`, `!**/graph/scheduler/scheduler.go`, `!**/graph/scheduler/pure_cascade.go` `files:` globs (all `**/`-anchored, still match under `lib/`; `graph/scenario` relocates in Pass 4).
4. In `runtime-purity`: change deny `pkg:` `.../control` → `.../lib/control`. Leave `.../cmd`, `.../stores`, `.../executors`.
5. In `consumption-side-isolation`: change deny `pkg:` `.../graph` → `.../lib/graph`, `.../runtime` → `.../lib/runtime`, `.../control` → `.../lib/control`.

**Verification:** covered by the pass gate.

### Task 13: Tidy and sync

**Files:** `go.sum`, `go.work.sum`

**Steps:**
1. `go mod tidy`
2. `go work sync`
3. `git add -A`

**Verification:** `go build ./...` exits 0.

### Task 14: Pass-3 gate

**Steps:**
1. Run `make build-all && make lint`.

**Verification:** exits 0.

---

## Pass 4: Consolidate test-support under `test/support/` and demote `testpg`

**Goal:** Move `internal/pgmigrate`, `stores`, `executors`, and `lib/graph/scenario` to `test/support/`; demote the `testpg` module to a plain package at `test/support/testpg` (drop its `go.mod`, remove it from the workspace and root manifests); update all imports and the depguard path globs/denies. After this pass every import rewrite is complete, so this pass runs the full test suite.
**Scope:** Tasks 15–22
**End state:** working
**Verification:** `make build-all && make test-all && make lint`

### Task 15: Physically move the test-support directories

**Files:** `internal/pgmigrate/` → `test/support/pgmigrate/`; `stores/` → `test/support/stores/`; `executors/` → `test/support/executors/`; `lib/graph/scenario/` → `test/support/scenario/`; `testpg/` → `test/support/testpg/`

**Steps:**
1. `mkdir -p test/support`
2. `mv internal/pgmigrate test/support/pgmigrate`
3. `mv stores test/support/stores`
4. `mv executors test/support/executors`
5. `mv lib/graph/scenario test/support/scenario`
6. `mv testpg test/support/testpg`
7. `rmdir internal 2>/dev/null || true` (the `internal/` dir is now empty — `ops` was deleted in Pass 1, `pgmigrate` just moved). If `rmdir` fails because files remain, STOP and inspect — nothing else should be there.
8. `git add -A`

**Verification:** `test -d test/support/pgmigrate && test -d test/support/stores && test -d test/support/executors && test -d test/support/scenario && test -d test/support/testpg && test ! -e internal && test ! -e testpg && echo OK` prints `OK`.

### Task 16: Drop the `testpg` module manifest

**Files:** `test/support/testpg/go.mod` (delete), `test/support/testpg/go.sum` (delete)

**Steps:**
1. `rm test/support/testpg/go.mod test/support/testpg/go.sum`
2. `git add -A`

**Verification:** `test ! -e test/support/testpg/go.mod && echo gone` prints `gone`. (The `testpg` package now belongs to the root module; its import path becomes `github.com/rimsky-ai/rimsky-core/test/support/testpg`.)

### Task 17: Remove `testpg` from the workspace and root manifest

**Files:** `go.work`, `go.mod`

**Steps:**
1. In `go.work`, delete the `./testpg` line. Final:
   ```
   use (
       .
       ./lib/foundation
       ./lib/protocols
   )
   ```
2. In root `go.mod`, delete the `require` line `github.com/rimsky-ai/rimsky-core/testpg v0.0.0` and the `replace` line `github.com/rimsky-ai/rimsky-core/testpg => ./testpg`.

**Verification:** `grep -n testpg go.work go.mod` prints nothing.

### Task 18: Rewrite imports for the moved test-support packages

**Files:** all `*.go` files repo-wide

**Steps:**
1. Run:
   ```
   find . -name '*.go' -not -path './.git/*' -print0 | xargs -0 perl -pi -e \
     's{github\.com/rimsky-ai/rimsky-core/internal/pgmigrate}{github.com/rimsky-ai/rimsky-core/test/support/pgmigrate}g; s{github\.com/rimsky-ai/rimsky-core/lib/graph/scenario}{github.com/rimsky-ai/rimsky-core/test/support/scenario}g; s{github\.com/rimsky-ai/rimsky-core/testpg}{github.com/rimsky-ai/rimsky-core/test/support/testpg}g; s{github\.com/rimsky-ai/rimsky-core/stores}{github.com/rimsky-ai/rimsky-core/test/support/stores}g; s{github\.com/rimsky-ai/rimsky-core/executors}{github.com/rimsky-ai/rimsky-core/test/support/executors}g;'
   ```
   (Order note: `lib/graph/scenario` is rewritten as its own full path, so it is unaffected by any generic `graph` rule — and there is no generic `graph` rule in this pass anyway.)
2. `git add -A`

**Verification:** `grep -rn 'rimsky-core/\(internal/pgmigrate\|lib/graph/scenario\|testpg\|stores\|executors\)' --include='*.go' . | grep -v '/.ok-planner/' | grep -v 'test/support/'` prints nothing.

### Task 19: Update depguard path globs and denies for the moved packages

**Files:** `.golangci.yml`

**Steps:**
1. In `pgx-isolation` `files:`: change `!**/internal/pgmigrate/**` → `!**/test/support/pgmigrate/**`, and `!**/graph/scenario/**` → `!**/test/support/scenario/**`. Leave `!**/testpg/**` and `!**/stores/**` (both still match the `/testpg/` and `/stores/` substrings under `test/support/`). Update the `desc:` text on the pgx deny entries to reflect the new paths (cosmetic but keeps the message accurate).
2. In `graph-purity` `files:`: remove the `!**/graph/scenario/**` exclusion line — `scenario` is no longer under `graph/`, so the exclusion matches nothing. (The harness is now at `test/support/scenario`, outside the `**/graph/**` glob, so it is not subject to graph-purity at all.)
3. In `foundation-purity`, `runtime-purity`: change deny `pkg:` `.../stores` → `.../test/support/stores` and `.../executors` → `.../test/support/executors`.
4. In `graph-purity`: change deny `pkg:` `.../stores` → `.../test/support/stores` and `.../executors` → `.../test/support/executors`. Update the executor-exception `desc:` note (it referenced `graph/scenario`; the harness has moved out).
5. In `consumption-side-isolation`: change deny `pkg:` `.../stores` → `.../test/support/stores`, `.../executors` → `.../test/support/executors`. Leave the root-anchored `files:` globs (`stores/**`, `executors/**`, etc.) as the documented defensive guard against re-bundling at the repo root; they now match nothing local (the stubs live under `test/support/`), which is the intended inert state.
6. Remove the now-orphaned `pkg: github.com/rimsky-ai/rimsky-core/internal` deny entries from both the `protocols-purity` rule and the `consumption-side-isolation` rule. The top-level `internal/` package is gone (Task 15 deleted the directory: `ops` removed in Pass 1, `pgmigrate` just moved to `test/support/pgmigrate`), so these denies reference a path that no longer resolves to anything. (Do NOT touch the `foundation-internal-isolation` rule, which guards the still-present `lib/foundation/internal`.)

**Verification:** covered by the pass gate.

### Task 20: Remove the now-empty `testpg` module from Makefile gate targets

**Files:** `Makefile`

**Steps:**
1. In `lint`, delete the `cd testpg && golangci-lint run` line.
2. In `test-all`, delete the `cd testpg && go test ./...` line.
3. In `build-all`, delete the `cd testpg && go build ./...` line.
4. In `lint-docker`, delete the `testpg` segment of the `&&`-chained command.
5. Update the comments in `test-all`/`build-all`/`core-images` that enumerate "root + foundation + protocols + testpg" to drop `testpg` (now: "root + lib/foundation + lib/protocols").

**Verification:** `grep -n 'cd testpg\|cd .*testpg' Makefile` prints nothing.

### Task 21: Tidy and sync (testpg deps fold into root)

**Files:** `go.sum`, `go.work.sum`

**Steps:**
1. `go mod tidy` (this promotes `testcontainers-go`, `testcontainers-go/modules/postgres` from indirect to direct in the root `go.mod`, since `test/support/testpg` now imports them directly within the root module).
2. `go work sync`
3. `git add -A`

**Verification:** `go build ./... && go vet ./test/support/...` exits 0.

### Task 22: Pass-4 gate (full suite)

**Steps:**
1. Run `make build-all && make test-all && make lint`. `make test-all` requires a working Docker socket (testcontainers); ensure Docker is running.

**Verification:** exits 0.

---

## Pass 5: Relocate `control/cli` into the CLI binary

**Goal:** Move the CLI library out of the control service into the CLI binary's directory, so the CLI is no longer filed under (or coupled to) the control service. `control/cli` has zero importers other than `cmd/rimsky` and imports nothing from `control/controlapi`, so this is a pure move + one import rewrite.
**Scope:** Tasks 23–25
**End state:** working
**Verification:** `make build-all && make lint`

### Task 23: Move `lib/control/cli` to `cmd/rimsky/cli`

**Files:** `lib/control/cli/` → `cmd/rimsky/cli/` (carries `roles/` and `internal/clitest/` subpackages along)

**Steps:**
1. `mv lib/control/cli cmd/rimsky/cli`
2. `git add -A`

**Verification:** `test -d cmd/rimsky/cli/roles && test -d cmd/rimsky/cli/internal/clitest && test ! -e lib/control/cli && echo OK` prints `OK`.

### Task 24: Rewrite the `control/cli` import path

**Files:** all `*.go` files (in practice only `cmd/rimsky/main.go` and files within `cmd/rimsky/cli/`)

**Steps:**
1. Run:
   ```
   find . -name '*.go' -not -path './.git/*' -print0 | xargs -0 perl -pi -e \
     's{github\.com/rimsky-ai/rimsky-core/lib/control/cli}{github.com/rimsky-ai/rimsky-core/cmd/rimsky/cli}g;'
   ```
   This fixes `cmd/rimsky/main.go`'s `import "github.com/rimsky-ai/rimsky-core/control/cli"` (now `lib/control/cli` after Pass 3) to `cmd/rimsky/cli`, and any internal references within the moved tree (e.g. `cli/internal/clitest`, `cli/roles`).
2. `git add -A`

**Verification:** `grep -rn 'rimsky-core/lib/control/cli' --include='*.go' . | grep -v '/.ok-planner/'` prints nothing; `grep -rn 'rimsky-core/cmd/rimsky/cli' cmd/rimsky/main.go` shows the updated import.

### Task 25: Verify the CLI builds and check the depguard package-comment

**Files:** `cmd/rimsky/main.go` (comment only)

**Steps:**
1. Update the `main.go` package doc comment line `// main.go — rimsky entry point. Dispatches subcommands to handlers in control/cli/.` to read `… handlers in cmd/rimsky/cli/.`
2. `make build-all && make lint`.

**Verification:** `make build-all && make lint` exits 0; `go build -o /tmp/rimsky-cli-check ./cmd/rimsky && /tmp/rimsky-cli-check --help` prints the root usage and exits 0 (then `rm -f /tmp/rimsky-cli-check`).

---

## Pass 6: Fold the conformance binaries into `rimsky conformance <protocol>` subcommands

**Goal:** Replace the seven standalone `cmd/rimsky-*-conformance` binaries with subcommands of the `rimsky` CLI, calling the same `lib/protocols/conformance/*` library the old mains call. Delete the old binaries, migrate their tests, and update the conformance image + the `CLAUDE.md` verify rule.
**Scope:** Tasks 26–31
**End state:** working
**Verification:** `make build-all && make test-all && make lint`

Binary → subcommand mapping:
| Old binary (`cmd/…`) | New invocation |
| --- | --- |
| `rimsky-executor-conformance` | `rimsky conformance executor` |
| `rimsky-claim-producer-conformance` | `rimsky conformance claim-producer` |
| `rimsky-publisher-conformance` | `rimsky conformance publisher` |
| `rimsky-validation-conformance` | `rimsky conformance validation` |
| `rimsky-data-processing-conformance` | `rimsky conformance data-processing` |
| `rimsky-blob-backend-conformance` | `rimsky conformance blob-backend` |
| `rimsky-conformance-probe` | `rimsky conformance probe` |

### Task 26: Add the `conformance` dispatch to the CLI

**Files:** `cmd/rimsky/main.go`, new `cmd/rimsky/conformance.go`

**Steps:**
1. In `cmd/rimsky/main.go`, add a case to the `switch os.Args[1]` block (alongside the existing cases), before `default`:
   ```go
   case "conformance":
       os.Exit(dispatchConformance(os.Args[2:]))
   ```
2. Create `cmd/rimsky/conformance.go` (package `main`, with the standard license header copied from `cmd/rimsky/main.go`'s first three lines) containing `func dispatchConformance(args []string) int`. It must: print a usage line listing the seven subcommands when `args` is empty or `args[0]` is `help`/`--help`/`-h`; switch on `args[0]` over the seven subcommand names; for each, call a `runConformance<Name>(args[1:]) int` function (defined in the next task); and return exit code 2 for an unknown subcommand. Model the structure on the existing `dispatchTemplate`/`dispatchInstance` functions in `main.go`.

**Verification:** covered by the pass gate plus Task 31's smoke check.

### Task 27: Port each conformance main into a subcommand handler

**Files:** `cmd/rimsky/conformance.go` (or one file per handler, e.g. `cmd/rimsky/conformance_executor.go`), reading the seven source files `cmd/rimsky-executor-conformance/main.go`, `cmd/rimsky-claim-producer-conformance/main.go`, `cmd/rimsky-publisher-conformance/main.go`, `cmd/rimsky-validation-conformance/main.go`, `cmd/rimsky-data-processing-conformance/main.go`, `cmd/rimsky-blob-backend-conformance/main.go`, `cmd/rimsky-conformance-probe/main.go`

**Steps:**
1. For each of the seven binaries, open its `main.go` and port the body of `func main()` into a `func runConformance<Name>(args []string) int`. Concretely: replace the package-level `flag` usage with a fresh `fs := flag.NewFlagSet("rimsky conformance <name>", flag.ContinueOnError)` so multiple handlers can coexist in one binary; register the exact same flags the original `main` registered (read them from the source — e.g. executor registers `--endpoint`, `--transport`, `--require-stub-mode`, `--scenarios`, `--skip`, `--timeout`, `--check-observability`, `--retention-test-seconds`, `--check-lifecycle`, `--callback-bind`, `--callback-host`); call `fs.Parse(args)`; preserve every validation, the library call into `github.com/rimsky-ai/rimsky-core/lib/protocols/conformance/...`, and the exit-code behavior; return the int the original passed to `os.Exit` (replace `os.Exit(n)` with `return n`, and a normal completion with `return 0`).
2. Preserve each original's stderr usage/error messages, adjusting the program name in the messages from e.g. `rimsky-executor-conformance:` to `rimsky conformance executor:`.
3. Wire each `runConformance<Name>` into the `dispatchConformance` switch from Task 26.

**Verification:** covered by the pass gate plus Task 31's smoke check.

### Task 28: Migrate the conformance mains' tests

**Files:** existing `cmd/rimsky-claim-producer-conformance/main_test.go`, `cmd/rimsky-data-processing-conformance/main_test.go`, `cmd/rimsky-publisher-conformance/main_test.go`, `cmd/rimsky-validation-conformance/main_test.go` → new tests under `cmd/rimsky/`

**Steps:**
1. For each `main_test.go` above, read it and move its test logic into a corresponding `cmd/rimsky/conformance_<name>_test.go` (package `main`), adapting any references to the old `main`-level flag variables to exercise the new `runConformance<Name>` entry points instead. If a test only validated flag parsing or argument handling, retarget it at the new handler.

**Verification:** covered by the pass gate (`make test-all` runs these).

### Task 29: Delete the seven standalone conformance binaries

**Files:** `cmd/rimsky-executor-conformance/`, `cmd/rimsky-claim-producer-conformance/`, `cmd/rimsky-publisher-conformance/`, `cmd/rimsky-validation-conformance/`, `cmd/rimsky-data-processing-conformance/`, `cmd/rimsky-blob-backend-conformance/`, `cmd/rimsky-conformance-probe/`

**Steps:**
1. `rm -rf cmd/rimsky-executor-conformance cmd/rimsky-claim-producer-conformance cmd/rimsky-publisher-conformance cmd/rimsky-validation-conformance cmd/rimsky-data-processing-conformance cmd/rimsky-blob-backend-conformance cmd/rimsky-conformance-probe`
2. `git add -A`

**Verification:** `ls cmd/ | grep conformance` prints nothing.

### Task 30: Update the conformance image to run the CLI

**Files:** `dockerfiles/Dockerfile.conformance`

**Steps:**
1. Replace the multi-binary build loop (the `RUN for bin in … rimsky-executor-conformance … ; do … go build … ./cmd/$bin … done` block) with a single build of the CLI:
   ```
   RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/rimsky ./cmd/rimsky
   ```
2. Update the module-manifest COPY lines (they will be fixed wholesale in Pass 7, but if this pass's edits touch them, mirror Pass 7's target: `COPY lib/foundation/go.mod lib/foundation/go.sum ./lib/foundation/`, `COPY lib/protocols/go.mod lib/protocols/go.sum ./lib/protocols/`, and remove the `testpg` COPY line). To avoid duplicate work, leave the COPY lines for Pass 7 and only change the build/run surface here.
3. Update the header comment and the `org.opencontainers.image.description` LABEL so the usage example reads `docker run rimsky-conformance rimsky conformance executor --endpoint <addr> --transport grpc` and the description says runners are selected via `rimsky conformance <protocol>`.

**Verification:** `grep -n 'rimsky-executor-conformance\|for bin in' dockerfiles/Dockerfile.conformance` prints nothing; `grep -n 'rimsky conformance' dockerfiles/Dockerfile.conformance` shows the new usage. (Actual image build is in Manual checks.)

### Task 31: Update the CLAUDE.md verify rule and gate the pass

**Files:** `CLAUDE.md`

**Steps:**
1. In `CLAUDE.md`, find the "Conformance-relevant changes" bullet under "After Code Changes" that reads `go run ./cmd/rimsky-executor-conformance --endpoint <executor> --transport grpc` and change it to `go run ./cmd/rimsky conformance executor --endpoint <executor> --transport grpc`.
2. Run the pass gate.

**Verification:** `make build-all && make test-all && make lint` exits 0; `go run ./cmd/rimsky conformance` prints the seven-subcommand usage and exits non-zero; `go run ./cmd/rimsky conformance probe` (no `--endpoint`) prints the probe's usage/error and exits non-zero (confirms the handler is wired and parses flags).

---

## Pass 7: Relocate `license-check` to `tools/` and finalize config (licensing, Dockerfiles, gitignore)

**Goal:** Move the dev-only `license-check` linter out of `cmd/` into `tools/`, then bring all non-build-gated config into its final state: `licensing.yml` path reclassification (including testpg Apache→AGPL), header re-stamping, the remaining Dockerfile module-COPY edits, and `.gitignore` cleanup. This pass first turns on the `make license-lint` gate.
**Scope:** Tasks 32–37
**End state:** working
**Verification:** `make build-all && make lint && make license-lint`

### Task 32: Move `license-check` to `tools/`

**Files:** `cmd/rimsky-license-check/` → `tools/license-check/`

**Steps:**
1. `mkdir -p tools`
2. `mv cmd/rimsky-license-check tools/license-check`
3. `git add -A`

**Verification:** `test -d tools/license-check && test ! -e cmd/rimsky-license-check && echo OK` prints `OK`.

### Task 33: Repoint the Makefile license targets

**Files:** `Makefile`

**Steps:**
1. In `license-lint`, change `go run ./cmd/rimsky-license-check` → `go run ./tools/license-check`.
2. In `license-stamp`, change `go run ./cmd/rimsky-license-check --stamp` → `go run ./tools/license-check --stamp`.

**Verification:** `grep -n 'license-check' Makefile` shows only `./tools/license-check`.

### Task 34: Rewrite `licensing.yml` to the new tree

**Files:** `licensing.yml`

**Steps:**
1. Under `apache:`, change `protocols/` → `lib/protocols/`. Remove the `testpg/` entry (it is demoted to AGPL test support). Leave `cold-read/`.
2. Under `agpl:`, replace `foundation/`, `graph/`, `control/`, `runtime/` with `lib/foundation/`, `lib/graph/`, `lib/control/`, `lib/runtime/`. Remove the `internal/`, `stores/`, and `executors/` entries (the first no longer exists; the latter two now live under `test/`, which is already an AGPL entry). Add `tools/` (covers the relocated license-check). Keep `cmd/` and `test/`. Result `agpl:` list: `lib/foundation/`, `lib/graph/`, `lib/control/`, `lib/runtime/`, `cmd/`, `test/`, `tools/`.
3. Update the comment at the top that points at `cmd/rimsky-license-check/` to `tools/license-check/`. Update the inline path comments (`protocols/` → `lib/protocols/`) for accuracy.
4. Leave `protocols/proto/v1/gen/` under `exempt:` but change it to `lib/protocols/proto/v1/gen/`.

**Verification:** `grep -n 'protocols/\|foundation/\|testpg' licensing.yml` shows only `lib/`-prefixed paths (no bare `protocols/`, `foundation/`, no `testpg`).

### Task 35: Re-stamp headers and verify the license boundary

**Files:** source files under `test/support/testpg/` (Apache→AGPL header rewrite) and any other moved files needing reconciliation

**Steps:**
1. Run `make license-stamp` (this reads the updated `licensing.yml` and rewrites each file's header to match its directory's classification — notably the `test/support/testpg/*.go` files flip from Apache to AGPL).
2. `git add -A`
3. Run `make license-lint` to confirm: every classified path exists, every source file is classified, and no Apache file imports an AGPL package.

**Verification:** `make license-lint` exits 0.

### Task 36: Finalize the Dockerfile module manifests

**Files:** `dockerfiles/Dockerfile.rimsky`, `dockerfiles/Dockerfile.conformance`, `dockerfiles/Dockerfile.go-base`

**Steps:**
1. In each of the three Dockerfiles, change the module-manifest COPY block from:
   ```
   COPY foundation/go.mod foundation/go.sum ./foundation/
   COPY protocols/go.mod protocols/go.sum ./protocols/
   COPY testpg/go.mod testpg/go.sum ./testpg/
   ```
   to:
   ```
   COPY lib/foundation/go.mod lib/foundation/go.sum ./lib/foundation/
   COPY lib/protocols/go.mod lib/protocols/go.sum ./lib/protocols/
   ```
   (Remove the `testpg` COPY line entirely — `testpg` is no longer a module.) Update the adjacent comments that say "foundation, protocols, and testpg are separate Go modules" to "lib/foundation and lib/protocols are separate Go modules".
2. The `cmd/<binary>` build/COPY paths in `Dockerfile.rimsky` (daemons) and `Dockerfile.go-base` (`./cmd/${BINARY}`) are unchanged — `cmd/` did not move.

**Verification:** `grep -rn 'testpg\|COPY foundation\|COPY protocols' dockerfiles/` prints nothing; `grep -rn 'COPY lib/foundation\|COPY lib/protocols' dockerfiles/` shows the new lines in all three Dockerfiles.

### Task 37: Clean up `.gitignore` and gate the pass

**Files:** `.gitignore`

**Steps:**
1. Remove the now-stale compiled-binary ignore lines for the deleted conformance binaries: `/rimsky-conformance-probe`, `/rimsky-executor-conformance`, `/rimsky-claim-producer-conformance`, `/rimsky-blob-backend-conformance`, `/rimsky-publisher-conformance`, `/rimsky-validation-conformance`, `/rimsky-data-processing-conformance`, and `/rimsky-license-check`.
2. Update the stale root-anchored fixture-binary ignore: `/runtime/hostagent/testdata/stubchild/stubchild` → `/lib/runtime/hostagent/testdata/stubchild/stubchild` (Pass 3 moved `runtime/` to `lib/runtime/`; the anchored `/runtime/...` pattern would otherwise match nothing).
3. Run the pass gate.

**Verification:** `make build-all && make lint && make license-lint` exits 0.

---

## Pass 8: Update human docs and the module-layout concept

**Goal:** Bring the prose documentation and the durable design doc in line with the new tree. These files do not affect the build; this final pass closes them out and runs the full gate to leave the tree green.
**Scope:** Tasks 38–43
**End state:** working
**Verification:** `make build-all && make test-all && make lint && make license-lint`

### Task 38: Update `CLAUDE.md`

**Files:** `CLAUDE.md`

**Steps:**
1. Update the "Where to look first" / module-layout description (the line describing "the four-module / four-layer split (`protocols/` + `foundation/` + … `testpg/` + root with `graph/` → `runtime/` → `control/`)") to describe the new three-module layout: root + `lib/foundation` + `lib/protocols`, with library packages under `lib/` (`lib/protocols`, `lib/foundation`, `lib/graph`, `lib/runtime`, `lib/control`), binaries under `cmd/`, test scaffolding under `test/support/`, and dev tooling under `tools/`.
2. Update the reference to `foundation/persistence/...` (test-infra note) to `lib/foundation/persistence/...`.
3. Update the image-build paragraph if it names `cmd/` conformance binaries — note that conformance is now `rimsky conformance <protocol>` subcommands shipped in the `rimsky` binary, and the `rimsky-conformance` image runs `rimsky conformance …`.

**Verification:** `grep -n 'testpg\|four-module' CLAUDE.md` reflects the new layout (no stale "four-module" claim).

### Task 39: Update `feature-index.md`

**Files:** `feature-index.md`

**Steps:**
1. Rewrite the path prefixes throughout: `foundation/…` → `lib/foundation/…`, `graph/…` → `lib/graph/…` (except the scenario harness, now `test/support/scenario/`), `runtime/…` → `lib/runtime/…`, `control/…` → `lib/control/…` (and `control/cli/` → `cmd/rimsky/cli/`), `protocols/…` → `lib/protocols/…`.
2. Update the "reference binaries" section: remove the seven `cmd/rimsky-*-conformance` entries (now `rimsky conformance <protocol>` subcommands under `cmd/rimsky/`) and change `cmd/rimsky-license-check/` → `tools/license-check/`.
3. Update the test-infra entries: `stores/stub/`, `stores/filesystem/testfixture/`, `stores/postgres/testfixture/`, `executors/stub/` → their `test/support/…` paths; and `internal/pgtest/` references stay under `lib/foundation/internal/pgtest/`.

**Verification:** `grep -n 'rimsky-core/foundation\|^.*\bgraph/\|cmd/rimsky-.*conformance' feature-index.md` shows no stale top-level references (spot-check).

### Task 40: Update the licensing/legal prose

**Files:** `COPYING.md`, `COPYRIGHT`

**Steps:**
1. In `COPYING.md`, update every `protocols/` reference to `lib/protocols/`; update the Apache-carve-out list (it lists `protocols/`, `testpg/`, `cold-read/`) to `lib/protocols/` and `cold-read/` (drop `testpg/`, now AGPL); update `cmd/rimsky-license-check` → `tools/license-check`.
2. In `COPYRIGHT`, update the embedder-layer reference from `protocols/` to `lib/protocols/`.

**Verification:** `grep -n 'protocols/\|testpg\|rimsky-license-check' COPYING.md COPYRIGHT` shows only `lib/protocols/` and `tools/license-check` (no bare `protocols/`, no `testpg`, no `cmd/rimsky-license-check`).

### Task 41: Update `README.md` and `.claude/rules/rules.md`

**Files:** `README.md`, `.claude/rules/rules.md`

**Steps:**
1. In `README.md`, update `protocols/proto/v1/` references → `lib/protocols/proto/v1/` and the `protocols/` module/licensing references → `lib/protocols/`.
2. In `.claude/rules/rules.md`: update `foundation/persistence/{postgres,sqlite}/migrations/` → `lib/foundation/persistence/...`; the race-test paths `runtime/...`, `graph/scheduler/...`, `foundation/persistence/postgres/...` → their `lib/...` equivalents; the storage-test path `foundation/persistence/...` → `lib/foundation/persistence/...`; and the Search-Scoping exclusion `proto/v1/gen/` note → `lib/protocols/proto/v1/gen/`. (`test/scenarios/...` is unchanged; `executors/claude-agent/` is the out-of-repo TypeScript executor and is unchanged.)

**Verification:** `grep -n 'protocols/proto\|foundation/persistence\|graph/scheduler' README.md .claude/rules/rules.md` shows `lib/`-prefixed paths.

### Task 42: Update the stub README and the `module-layout` concept

**Files:** `test/support/executors/stub/README.md`, `.ok-planner/design/concepts/module-layout.md`

**Steps:**
1. In `test/support/executors/stub/README.md`, update the self-referencing path `executors/stub` → `test/support/executors/stub`, `executors/stub/stubtest/` → `test/support/executors/stub/stubtest/`, and `protocols/proto/v1/executor.proto` → `lib/protocols/proto/v1/executor.proto`. The `rimsky-executor-conformance` mention becomes `rimsky conformance executor`.
2. Update `.ok-planner/design/concepts/module-layout.md` to describe the new layout. This is a durable design doc, so edit it in place: the four-module split becomes three modules (root + `lib/foundation` + `lib/protocols`); the layer ordering `foundation < graph < runtime < control` now lives under `lib/`; the boundaries/invariants sections keep the same depguard rule names but reference the new paths; the licensing-boundary section reflects `lib/protocols` (Apache) vs the rest (AGPL) and that `testpg` is no longer a public module. Append a dated Notes entry: `- YYYY-MM-DD (spec: none — root-folder reorg): collapsed the repo into cmd/ lib/ test/ tools/; protocols+foundation moved under lib/ (module paths gained a lib/ segment); testpg demoted from a standalone module to test/support/testpg; internal/ops deleted (dead); internal/pgmigrate + stores + executors stubs + the scenario harness consolidated under test/support/; control/cli relocated into cmd/rimsky/cli; conformance binaries folded into "rimsky conformance <protocol>" subcommands; license-check moved to tools/.` (use today's date).

**Verification:** `grep -n 'testpg\|four-module' .ok-planner/design/concepts/module-layout.md` reflects the new three-module layout; the Notes entry is present.

### Task 43: Final full gate and stale-reference sweep

**Files:** none

**Steps:**
1. Run a stale-reference sweep to catch anything missed: `grep -rn 'rimsky-core/\(foundation\|protocols\|graph\|runtime\|control\)"' --include='*.go' . | grep -v '/.ok-planner/' | grep -v 'rimsky-core/lib/'` and `grep -rn 'rimsky-core/\(internal/pgmigrate\|testpg\|stores\|executors\)' --include='*.go' . | grep -v 'test/support/'` — both must print nothing.
2. Run the full gate: `make build-all && make test-all && make lint && make license-lint` (Docker must be running for `test-all`).

**Verification:** the two greps print nothing and `make build-all && make test-all && make lint && make license-lint` exits 0.

---

## Manual checks after completion

These require a Docker daemon and build full images / bring up the stack — heavier than the automated gate and best run by the user after the implementation and review are done.

1. **Rebuild the four core images:** `make core-images`. Confirm all four (`rimsky`, `rimsky-all-in-one`, `rimsky-host-agent-proxy`, `rimsky-conformance`) build cleanly with the updated `lib/` module COPY lines and the testpg COPY removed.
2. **Smoke-test the conformance image's new command surface:** `docker run --rm rimsky-conformance rimsky conformance` should print the seven-subcommand usage; `docker run --rm rimsky-conformance rimsky conformance executor --help`-style invocation should parse.
3. **Smoke-test the all-in-one stack:** bring up the `rimsky-all-in-one` image and confirm it reaches `/health`, exercising the baked SQLite defaults end-to-end.
