# Root-Folder Reorg — Divergence Record

Audit of the staged (uncommitted) working tree against
`.ok-planner/plans/2026-05-27-root-folder-reorg.md`. This is a record of where
the implementation differs from what the plan literally said — **not** a code
review. Build (`go build ./...`) and `make lint` (root + lib/foundation +
lib/protocols) are green at audit time; the Task-43 stale-reference greps both
return empty.

The plan's directory moves all landed at their stated destinations (verified via
`git diff --staged --name-status -M`: `control/cli/*` → `cmd/rimsky/cli/*`,
`cmd/rimsky-license-check/*` → `tools/license-check/*`, the two library modules
and the three root packages under `lib/`, the five test-support trees under
`test/support/`). No rename landed anywhere other than where the plan named it.

---

## 1. Proto bindings were regenerated, not just textually rewritten (Pass 4)

- **What the plan said:** Task 5 only anticipated rewriting (a) the `.pb.go`
  import statements via the repo-wide `perl` `*.go` rewrite and (b) the
  `option go_package` line in the `.proto` sources "so the next `make
  proto-gen` regenerates bindings under the correct import path." No plan task
  runs `make proto-gen` as part of the reorg.
- **What was implemented:** `make proto-gen` was run. The 20 generated files
  under `lib/protocols/proto/v1/gen/*.pb.go` show up under rename detection as
  `R099` (old `protocols/proto/v1/gen/X.pb.go` → new `lib/protocols/...`), i.e.
  ~99% similar — consistent with a clean regen where only the embedded
  `go_package` path bytes changed. The new files carry the correct
  `lib/protocols/proto/v1/gen` path inside the descriptor; no `rimsky-core/protocols/proto`
  remnants survive in `gen/`. The `.proto` sources carry the updated
  `option go_package = ".../lib/protocols/proto/v1/gen;genv1"`.
- **Inferred reason:** Forced correction. Pass 2's textual `perl` rewrite over
  `*.go` files edited the length-prefixed `go_package` string embedded in the
  generated `rawDesc` literal without updating its length prefix, leaving the
  descriptor bytes internally inconsistent. Regenerating from the (correctly
  rewritten) `.proto` sources is the only coherent fix. Reported by the
  implementer; confirmed landed.

## 2. String-literal fixture path repointed in a scenario test (Pass 4)

- **What the plan said:** Nothing. The Pass-3 import rewrite (Task 11) and the
  Pass-4 rewrites operate on Go *import paths* only.
- **What was implemented:** In `test/scenarios/host_agent_harness_test.go`, the
  build-helper argument `buildBinary(t, "runtime/hostagent/testdata/stubchild")`
  was changed to `"lib/runtime/hostagent/testdata/stubchild"`. This is a string
  literal, not an import, so the `perl` import rewrites would have missed it.
- **Inferred reason:** Plan gap / forced correction. The `runtime/` → `lib/runtime/`
  move (Pass 3) invalidated the hard-coded relative path; without this edit the
  scenario test would fail to locate the stub-child binary. Reported by the
  implementer; confirmed landed.

## 3. Conformance verify-rule lives in rules.md, not CLAUDE.md (Pass 6 / Task 31)

- **What the plan said:** Task 31 step 1 said to edit `CLAUDE.md`'s
  "Conformance-relevant changes" bullet, changing `go run ./cmd/rimsky-executor-conformance
  --endpoint <executor> --transport grpc` to the new `rimsky conformance executor`
  invocation.
- **What was implemented:** That bullet does not live in `CLAUDE.md` — it is in
  `.claude/rules/rules.md` (the "After Code Changes" section). The implementer
  edited it there: `go run ./cmd/rimsky conformance executor --endpoint <executor>
  --transport grpc`. `CLAUDE.md` was instead updated where it *does* discuss
  conformance — the "Image builds" paragraph (a Task-38 item), which now says
  the runners are `rimsky conformance <protocol>` subcommands and the
  `rimsky-conformance` image runs `rimsky conformance …`.
- **Inferred reason:** Plan error (wrong file named). The implementer applied
  the intended change to the file that actually contains the rule. Reported by
  the implementer; confirmed landed.

## 4. Extra proto-glob correction in rules.md (Pass 8 / Task 41)

- **What the plan said:** Task 41 step 2 enumerated specific `.claude/rules/rules.md`
  path updates: the migrations path, the three race-test paths, the storage-test
  path, and the Search-Scoping `proto/v1/gen/` exclusion. It did **not** mention
  the "Proto changes" verify-rule glob.
- **What was implemented:** Beyond the listed edits, the implementer also
  corrected the "Proto changes (`proto/v1/*.proto`)" verify-rule glob to
  "Proto changes (`lib/protocols/proto/v1/*.proto`)". (The Search-Scoping
  `proto/v1/gen/` → `lib/protocols/proto/v1/gen/` edit the plan *did* list also
  landed.)
- **Inferred reason:** Cleaner/complete shape — the same `lib/` relocation
  applies to this glob, and leaving it stale would point a future session at a
  path that no longer exists. Reported by the implementer; confirmed landed.

## 5. All seven conformance handlers in one file, not one-per-handler

- **What the plan said:** Task 27 offered a choice: "`cmd/rimsky/conformance.go`
  (or one file per handler, e.g. `cmd/rimsky/conformance_executor.go`)".
- **What was implemented:** A single new file `cmd/rimsky/conformance.go` (582
  lines) holds `dispatchConformance`, `printConformanceUsage`, all seven
  `runConformance*` handlers, the two CSV/adapter helpers, the
  `blobBackendAdapter` type, and `openBlobBackend`. No per-handler files.
- **Inferred reason:** Allowed by the plan (one of the two offered shapes). Worth
  recording because the file is ~582 lines — above the ~500-line cold-read
  guideline — which a reviewer may want to weigh against the cohesion benefit of
  keeping all conformance dispatch in one place.

## 6. Helper renames to avoid `package main` collisions (Pass 6)

- **What the plan said:** Task 27 said to port each `main()` body into a
  `runConformance<Name>` function and register flags on a fresh `FlagSet`, but
  did not enumerate the package-level helper/type names that would collide once
  seven former `main` packages share one `package main`.
- **What was implemented (confirmed):**
  - `adapter` (blob-backend conformance) → `blobBackendAdapter`
    (`cmd/rimsky/conformance.go:432`).
  - `openBackend` → `openBlobBackend` (`cmd/rimsky/conformance.go:465`).
  - test helper `startFixtureServer` → `startFixtureValidationServer`
    (`cmd/rimsky/conformance_validation_test.go:76`).
  - The CSV splitter is namespaced as `splitConformanceCSV`
    (`cmd/rimsky/conformance.go:149`).
- **Inferred reason:** Forced choice — collapsing seven `package main` binaries
  into one binary requires unique top-level identifiers. Reported by the
  implementer; confirmed landed.

## 7. Migrated conformance tests target the library `Run`, not the new handlers

- **What the plan said:** Task 28: "adapting any references to the old
  `main`-level flag variables to exercise the new `runConformance<Name>` entry
  points instead. If a test only validated flag parsing or argument handling,
  retarget it at the new handler."
- **What was implemented:** The four migrated test files
  (`conformance_claimproducer_test.go`, `conformance_dataprocessing_test.go`,
  `conformance_publisher_test.go`, `conformance_validation_test.go`) call the
  importable conformance library directly (e.g. `valconformance.Run(...)`,
  `conformance_validation_test.go:108`) — the same surface the original
  `main_test.go` files exercised. None of them invoke `runConformance*`. (One
  file's comment notes the handler is "a thin wrapper around it.")
- **Inferred reason:** The originals were library-level tests, not flag-parsing
  tests, so there was nothing handler-specific to retarget — the plan's
  "if a test only validated flag parsing" branch didn't apply. The new CLI
  handlers themselves have no dedicated unit tests; they are covered only by
  build/lint and the Pass-31 smoke checks.

## 8. Compressed test-file names (no hyphens)

- **What the plan said:** Task 28 named the targets `cmd/rimsky/conformance_<name>_test.go`,
  with the binary→subcommand table using hyphenated names (`claim-producer`,
  `data-processing`).
- **What was implemented:** Files are `conformance_claimproducer_test.go` and
  `conformance_dataprocessing_test.go` — the hyphens dropped, matching the
  importable library package names (`claimproducer`, `dataprocessing`) rather
  than the hyphenated subcommand spellings.
- **Inferred reason:** Cleaner shape / Go convention (package and file names
  avoid hyphens). Trivial but noted because it deviates from the literal
  filenames the plan implied.

---

## Items checked and found to match the plan (no divergence)

These are recorded so the reviewer knows they were verified, not skipped:

- **Module manifests / workspace** (Tasks 4, 16, 17): `go.work` lists exactly
  `.`, `./lib/foundation`, `./lib/protocols`; root `go.mod` `require`/`replace`
  use the `lib/`-prefixed paths; no `testpg` entries remain. `testpg/go.mod` and
  `testpg/go.sum` are deleted.
- **`testcontainers-go` promotion** (Task 21): `testcontainers-go` and
  `testcontainers-go/modules/postgres` are now in the **direct** `require` block
  of the root `go.mod`, exactly as the plan predicted (folded in from the
  demoted `testpg`).
- **depguard edits** (Tasks 6, 12, 19): all `pkg:` denies repointed to `lib/...`
  and `test/support/...`; the orphaned `github.com/rimsky-ai/rimsky-core/internal`
  denies are gone from both `protocols-purity` and `consumption-side-isolation`;
  the `**/`-anchored `files:` globs (`**/foundation/**`, `**/protocols/**`,
  `**/testpg/**`, `**/stores/**`, etc.) were left unchanged as the plan
  directed; the `!**/graph/scenario/**` exclusion was removed from `graph-purity`.
- **`internal/ops` deletion** (Pass 1): all four files
  (`dsn.go`, `health.go`, `slog.go`, `ops_test.go`) deleted; the `internal/`
  directory no longer exists.
- **`licensing.yml`** (Task 34): `apache:` = `lib/protocols/` + `cold-read/`
  (no `testpg`); `agpl:` = the five `lib/...`+`cmd/`+`test/`+`tools/` list;
  `exempt:` carries `lib/protocols/proto/v1/gen/`.
- **`test/support/testpg/*.go` headers** flipped Apache→AGPL (Task 35).
- **Dockerfiles** (Tasks 30, 36): all three carry
  `COPY lib/foundation/...` + `COPY lib/protocols/...` with no `testpg` COPY;
  `Dockerfile.conformance` builds a single `./cmd/rimsky` binary and its
  header/LABEL document `rimsky conformance <protocol>`.
- **`cmd/rimsky/main.go`** (Tasks 25, 26): `case "conformance":` dispatches to
  `dispatchConformance`; the package-doc comment reads "handlers in
  cmd/rimsky/cli/."
- **Docs** (Tasks 38–42): `CLAUDE.md` describes the three-module / four-top-level-dir
  layout with no stale "four-module"/`testpg` claims; `feature-index.md`,
  `README.md`, `COPYING.md`, `COPYRIGHT`, the stub `README.md`, and the
  `module-layout` concept doc (including the dated 2026-05-27 Notes entry) all
  reflect the new tree.
