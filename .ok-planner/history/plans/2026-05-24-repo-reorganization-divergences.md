# 2026-05-24 repo-reorganization — divergences

Record of where the working tree across the five repos differs from what
`plan:2026-05-24-repo-reorganization` literally said. Produced by the
post-execute divergence auditor; this is a list of creative choices, not
a code review.

Counts: 48 tasks in the plan, ~17 meaningful divergences below.

---

### 1. Pass 1, Task 2 — openlineage test rewrite used testcontainers-go with minimal subscriber-contract DDL instead of docker-compose w/ rimsky image

**What the plan said:** Task 2 (plan lines 52–72) called for a `subscribers/openlineage/docker-compose.test.yml` (or testcontainers wrapping the locally-built rimsky image) that stands up the full rimsky stack from `./deploy/build-images.sh`, registers a template via `POST /templates`, creates an instance via `POST /instances`, registers the subscriber as a peer service, and asserts on OpenLineage events driven from the public API.

**What was implemented:** `../rimsky-services/subscribers/openlineage/subscriber_test.go` (814 lines) uses `testcontainers-go` to stand up a vanilla Postgres, applies a minimal `rimsky_lineage` read-contract DDL (not full rimsky migrations, no rimsky binaries running), and exercises the subscriber against that. No template registration, no operator-API drive, no rimsky stack bring-up. The file's own header doc-comment justifies: "The harness uses testcontainers-go directly to stand up a vanilla Postgres, applies only the subscriber's read-contract schema (a minimal `rimsky_lineage` table with the columns the subscriber queries — no FKs to rimsky-internal tables like `rimsky_instances` or `rimsky_frames`)." No `docker-compose.test.yml` exists.

**Inferred reason:** Forced choice / cleaner shape. The plan's "stand up rimsky from a locally-built image then drive it via public API" requires a published rimsky image and operator-API plumbing that don't exist yet. The implementer interpreted the spec's *acceptance* criterion ("Test does not import `pkg:foundation/persistence`, `pkg:foundation/shared`, or `pkg:internal/pgtest`") literally — the new shape satisfies it by owning a minimal contract schema instead of riding rimsky's. The semantic coverage shrank (no longer exercises the rimsky → subscriber wire end-to-end) but is now self-contained.

---

### 2. Pass 2, Task 8 — `internal/pgtest` split went to `foundation/internal/pgtest` (not the plan's `internal/pgmigrate`)

**What the plan said:** Task 8 (plan lines 224–249) specified splitting `internal/pgtest/pgtest.go` into two pieces: plain-container surface moves to `sdk/go/testpg/`; migrations-applying surface stays rimsky-internal at *"`internal/pgmigrate/` (or another name)"* with the directory renamed for clarity.

**What was implemented:** The split landed at **two** rimsky-internal locations: `foundation/internal/pgtest/` (untouched-shape package `pgtest` with the migrations-applying `StartPostgres`) AND `internal/pgmigrate/` (new package `pgmigrate` exposing `OpenDriver` / `StartPostgres` over the SDK's testpg). Both import `foundation/persistence` + `foundation/persistence/postgres` + `foundation/shared`. The `pgx-isolation` depguard explicitly carves out `**/foundation/internal/pgtest/**` AND `**/internal/pgmigrate/**` as pgx-allowed locations.

**Inferred reason:** Cleaner shape under a depguard constraint. `foundation-internal-isolation` (existing rule) makes `foundation/internal/pgtest` reachable only to foundation callers, while rimsky-root callers (graph/scenario, runtime tests) use `internal/pgmigrate`. Plan envisioned one rename; reality wanted two homes with different visibility scopes.

---

### 3. Pass 2, Task 10 — Server scaffolding extracted as single `lifecycle.go`, not per-protocol `sensor.go` / `subscriber.go` / `executor.go`

**What the plan said:** Task 10 (plan lines 270–290) named new files `sdk/go/server/sensor.go`, `sdk/go/server/subscriber.go`, `sdk/go/server/executor.go` extracted from each impl's inlined server code, "implementer's call on package boundaries".

**What was implemented:** `sdk/go/server/` contains four files: `bridge.go` (from `stores/internal/bridge`), `bridge_test.go`, `observability.go`, plus **one** new file `lifecycle.go` (78 lines, `Listen` / `Serve` / `GracefulStop` / `RunGRPC` — a single generic gRPC-server bring-up + graceful-shutdown wrapper). No `sensor.go` / `subscriber.go` / `executor.go`. The header comment confirms: the helper "handles the listen/serve/graceful-stop pattern that was previously copy-pasted across every sensor / subscriber / executor main.go."

**Inferred reason:** Cleaner shape. The plan assumed each protocol's bring-up was different enough to warrant its own file; the implementer found one generic gRPC lifecycle wrapper sufficed.

---

### 4. Pass 3, Task 13 — Conformance `executor` package kept name `conformance` (preserving `conformance.Register` API) and added `sdk/go/conformance/executor/client.go` as tracked-duplicate of `runtime/executor`

**What the plan said:** Task 13 (plan lines 333–354) called for extracting six conformance runners into `sdk/go/conformance/<protocol>/runner.go`. No mention of a separate client.go, no mention of preserving the `conformance.Register` name across packages.

**What was implemented:** The executor sub-package at `sdk/go/conformance/executor/` declares `package conformance` (not `package executor`) — preserving the historical `conformance.Register` / `conformance.AwaitTerminal` API used by scenario `init()` registrations. An additional file `sdk/go/conformance/executor/client.go` (untracked at audit time) duplicates three types from `runtime/executor` (`Endpoint`, `Client`, `EventStream`, plus `NewGRPCClient`, `ClientPool`), each tagged `@source: runtime/executor/...` + `@diverged: true`. The header comment justifies: "the SDK cannot import that package (`sdk-purity` denies it). The two surfaces are kept in semantic lockstep."

**Inferred reason:** Depguard constraint. `sdk-purity` forbids `sdk/go` from importing `runtime/`; the conformance runner needs the Endpoint/Client/EventStream types; tracked duplication via `@source` + `@diverged` is the cold-read-blessed shape for that case. The package-name preservation is to keep call-sites in scenario `init()`s working without rename.

---

### 5. Pass 3, Task 13 (residue) — Stale `sdk/go/conformance/claimproducer-tmp/runner.go` left behind through Pass 5, deleted only in Pass 7

**What the plan said:** Task 13 produces `sdk/go/conformance/claimproducer/runner.go` (one of the six runners). Plan never mentions a `-tmp` sibling.

**What was implemented:** A 246-line `sdk/go/conformance/claimproducer-tmp/runner.go` was created during Pass 3, remained committed-staged through Passes 4–6, and is recorded in the CHANGELOG (Pass 7 entry) as "leftover from Pass 5; the canonical impl at `pkg:sdk/go/conformance/claimproducer/` superseded it, but the `-tmp` copy was never deleted — and it carried a stale `pkg:foundation/locks` import that violated `sdk-purity` once `make lint` re-checked." The file is now staged-as-added-and-then-deleted (`AD` in git status).

**Inferred reason:** Implementer error (rename collision) discovered late by lint and patched in Pass 7. Recorded here because the audit framing flagged "pre-existing bugs the implementers fixed alongside plan work."

---

### 6. Pass 4, Task 22 — REWRITE-TO-STUB had zero rewrites required; audit superseded the plan's expected workload

**What the plan said:** Task 22 (plan lines 568–582) directed the implementer to walk every REWRITE-TO-STUB test, swap `stores/{filesystem,postgres}/testfixture` → `stores/stub/testfixture` (or `stores/stub/store`), and rerun.

**What was implemented:** Per `.ok-planner/plans/2026-05-24-repo-reorganization-test-audit.md` (working notes): "**Outcome of Task 22: no rewrites required.** Every test in this category was already authored against `pkg:stores/stub` from the start — no in-tree generic test imports `pkg:stores/filesystem` or `pkg:stores/postgres`." The 30+ in-tree scenario tests under `test/scenarios/{acquire_*,locks/,fanout_*,held_claim_*,lifecycle/,...}` had no production-store imports.

**Inferred reason:** Plan error / superseded by audit. The spec's "Default rule: rewrite to stub-store" pre-supposed a body of tests using production stores generically; the audit found that body didn't exist. Plan compliance was vacuous.

---

### 7. Pass 4, Task 24 — Testfixture refactor chose stub-wrapping (the secondary option), preserved expanded API surface for source compat

**What the plan said:** Task 24 (plan lines 595–612) called out two options — preferred docker image bring-up, fallback stub-store wrapping. Step 2 forced the fallback: "**For this task, use the stub-store approach**." The plan expected the public-API signatures to remain identical.

**What was implemented:** Both `stores/filesystem/testfixture/testfixture.go` and `stores/postgres/testfixture/testfixture.go` wrap `stores/stub`. The implementer expanded the testfixture surface: each defines a local `PickPolicy` type mirroring the production store's `PickPolicy` shape (filesystem's preserves `Root`, `OnCommit`, `OnGiveUp`, `VisibilityTimeout`, `SyncStrategy`; postgres's preserves `ItemsTable`, `OnCommit`, `OnGiveUp`, `VisibilityTimeout`). Many fields are accepted-for-source-compat and silently ignored (e.g., postgres `EnableExecutor`, `Connection`, `WriteSemantics`, `SweepInterval`, `WithAdmin`; filesystem `SyncStrategy`, `VisibilityTimeout`). Both files document this with prose pointing tests that need the dropped semantics to rimsky-services.

**Inferred reason:** Cleaner shape. Plan said "preserve public function signatures so call-site tests don't need changes"; implementer judged that retaining the field-level config struct (silently ignoring fields the stub-store can't honor) was the lowest-disruption form of "API unchanged." Stub-purity vs source-compat trade resolved toward source-compat.

---

### 8. Pass 4 / Pass 5 — Build-tag `//go:build rimskyservices` parked nine MOVE-set tests during Pass 4; stripped at move time in Pass 5

**What the plan said:** The plan never introduced a build tag. Pass 4 (Task 23) said "no file changes in this task," and Pass 5 (Task 29) said move + import-path update.

**What was implemented:** During Pass 4, nine files (three `test/scenarios/stores/fs_*_test.go`, two `test/scenarios/atomic_staging/pg_verifier_*` files using the postgres testfixture, the five `test/smoke/*.go` files) were stamped with `//go:build rimskyservices` so they were excluded from default `go build` / `go test` while their production-side dependencies still lived in rimsky. Pass 5 stripped the tag on move. Two MOVE-set files (`pg_verifier_test.go`, `bundled_executor_vocab_test.go`) were left untagged because they didn't import the testfixture or rimsky-internal harness. Documented in the CHANGELOG Pass-4 entry.

**Inferred reason:** Cleaner shape / forced choice. The plan's strict sequencing implicitly required tests to be parked during the in-between state; the implementer made the parking mechanism explicit and reversible via a single tag.

---

### 9. Pass 5, Task 27 — `stores/common/action` promoted into the SDK at `sdk/go/stores/action/`, not just moved to rimsky-services

**What the plan said:** Task 27 step 4 (plan lines 686) directed moving `stores/common/` and `stores/shared/` to `../rimsky-services/stores/common/` and `../rimsky-services/stores/shared/`.

**What was implemented:** `stores/common/action` was **promoted into the SDK** at `sdk/go/stores/action/` (four files: `action.go`, `parity_test.go`, `yaml.go`, `yaml_test.go`). The empty `stores/common/` directory was removed. `stores/shared/sql-checks` did move to rimsky-services as planned. The CHANGELOG Pass-5 entry justifies: "The action-vocabulary types (`Action`, `Kind`, `Pop` / `PopAndMove` / `PopAndDelete` / `Recycle`) are the canonical cross-implementer surface every claim-producer store implements against; with the filesystem + postgres stores moving to rimsky-services and `pkg:stores/stub` staying in rimsky, the only consistent home for the shared vocabulary is the SDK."

**Inferred reason:** Cleaner shape. The plan assumed all of `stores/common` moved; the implementer split it — the action vocabulary belongs in the implementer-facing surface (SDK), the SQL helpers belong with the SQL-using stores (rimsky-services). The in-rimsky `stores/stub` consumes it from the SDK, so both repos depend on it via the SDK path.

---

### 10. Pass 5, Task 29 — Moved tests landed as `t.Skip` TODO stubs in rimsky-services, not running tests

**What the plan said:** Task 29 step 4 (plan line 728) said "If a test depends on `graph/scenario.Start` (an in-process rimsky harness that won't be reachable from rimsky-services), rewrite the test to bring up rimsky from the published image and drive it via public API (similar shape to Task 2)." Step 5 said verify by running.

**What was implemented:** Nine moved files in `../rimsky-services/test/` are `t.Skip` stubs carrying the comment `TODO: 2026-05-24-repo-reorganization — needs rimsky image bring-up harness; depends on rimsky-services CI publishing rimsky-core images.` Affected: `test/smoke/{auth,data_platform,observability,stores_redesign}_smoke_test.go`, `test/smoke/setup.go`, `test/scenarios/atomic_staging/pg_verifier_{commit_abandon,conformance}_test.go`, `test/scenarios/bundled_executor_vocab_test.go`, `test/scenarios/stores/fs_{cross_queue_concurrency,pick_policy_basic,pick_vs_scope_concurrency}_test.go`. The non-skipped survivor: `test/scenarios/atomic_staging/pg_verifier_test.go` (pure wire-shape, no harness dependency).

**Inferred reason:** Forced choice. The plan's "rewrite to bring up rimsky from the published image" requires the rimsky-services CI to actually publish rimsky-core images, which doesn't happen until that CI is bootstrapped — a chicken-and-egg the implementer punted on with explicit TODOs.

---

### 11. Pass 5 / Pass 7, Cross-repo go.mod — Committed `replace` directives in sibling repos, not per-developer `go.work`

**What the plan said:** Plan Task 25 step 2 said `go.mod` should declare `require github.com/fallguyconsulting/rimsky/sdk/go v0.0.0` as a placeholder. Spec §P3.3 explicitly said: "For local dev across both repos: a per-developer `go.work` (not committed; lives outside either repo or is `.gitignore`d) lists both module paths so changes in `pkg:sdk/go` reflect immediately without retagging. CI in rimsky-services resolves the tagged dependency from the module proxy; no local-path resolution."

**What was implemented:** Both `../rimsky-services/go.mod` and (per CHANGELOG Pass-7 entry) `../rimsky-docs/examples/go.mod` carry committed `replace` directives pointing at sibling paths:

```
replace (
    github.com/fallguyconsulting/rimsky/protocols => ../rimsky/protocols
    github.com/fallguyconsulting/rimsky/sdk/go => ../rimsky/sdk/go
)
```

A trailing comment notes: "Local-dev replace directives so sibling rimsky checkouts resolve without a tagged release. CI may strip these (or set the replaced version) when pinning to a published rimsky tag."

**Inferred reason:** Spec intent override / cleaner shape. The implementer judged that committed `replace` directives are the pre-v1 norm and that "CI may strip" is acceptable, avoiding the per-developer `.gitignore`d-go.work setup-friction. The break with the spec is explicit and acknowledged in the file comment.

---

### 12. Pass 5, scope expansion — `sdk/go/conformance/executor/{lifecycle_check,observability_check}.go` were also extracted

**What the plan said:** Task 13 enumerated extracting `cmd/rimsky-executor-conformance/{main.go, lifecycle_check.go, observability_check.go}` into `sdk/go/conformance/executor/runner.go`. The Files block listed one runner.go destination.

**What was implemented:** `sdk/go/conformance/executor/` ended up with separate files: `runner.go`, `lifecycle_check.go`, `observability_check.go`, `await_terminal.go`, `callback_receiver.go`, `scenario.go`, `client.go`, plus `scenarios/` sub-package (10 files). The `lifecycle_check.go` + `observability_check.go` aren't tracked yet (untracked at audit time alongside `client.go`). Similar split: `sdk/go/conformance/claimproducer/` has `runner.go` + `observability_check.go`.

**Inferred reason:** Cleaner shape. Preserved file-level structure from the source `cmd/` directories rather than merging into one runner.go; the plan's "one runner.go per protocol" was a directional sketch.

---

### 13. Pass 6, Task 32 — `consumption-side-isolation` depguard `files:` glob narrowed from `**/stores/**` to root-anchored `stores/**` (and siblings)

**What the plan said:** Task 5 (Pass 1) and the spec's P1.5 block (spec lines 144–161) defined the rule with `files:` patterns `**/stores/**`, `**/sensors/**`, `**/subscribers/**`, `**/executors/**`.

**What was implemented:** `.golangci.yml`'s `consumption-side-isolation.files` uses root-anchored `stores/**`, `sensors/**`, `subscribers/**`, `executors/**` (no leading `**/`). An inline comment justifies: "Root-anchored patterns intentionally — the spec's `**/stores/**` would also match `test/scenarios/stores/`, which is rimsky-internal scenario test code, not bundled consumption-side binaries scheduled to move out."

**Inferred reason:** Plan error / cleaner shape. The wildcard `**/stores/**` would have flagged the test scenario tests as consumption-side violators. The implementer caught it and narrowed; the inline comment makes the intent permanent.

---

### 14. Pass 7, Task 38 — docs-llms-full subsumed the dropped Makefile `docs-roots` cp-step instead of leaving root llms files hand-stitched

**What the plan said:** Task 40 (plan lines 932–945) said determine whether `llms.txt` / `llms-full.txt` are generated, and either move with docs or leave in rimsky.

**What was implemented:** Per the CHANGELOG Pass-7 entry: "`cmd:rimsky-docs-llms-full` gained a `-root-output` flag and now writes both `docs/agents/llms-full.txt` and the repo-root copy in a single invocation, subsuming the dropped `Makefile docs-roots` cp-step." Root-level `llms.txt` / `llms-full.txt` moved to `../rimsky-docs/`. The `Makefile`'s `docs-glossary`, `docs-llms-full`, `docs-lint`, `docs-roots`, `docs-build` targets and `.PHONY` entries were removed wholesale.

**Inferred reason:** Cleaner shape. Plan envisioned a moved-or-left binary decision; implementer rebuilt the upstream tool to emit both locations in one pass, removing a Makefile target that no longer had a home. Scope expansion beyond what the task enumerated.

---

### 15. Pass 7, Task 39 — atomic-staging example's 4 scenario tests moved without `t.Skip` (the example's local store package satisfied them)

**What the plan said:** Task 39 step 5 (plan lines 922–926) said the four atomic-staging scenario tests "previously used `graph/scenario.Start` — rewrite them to drive rimsky from a published image (same pattern as Task 2 and Task 29) if they need to be runnable in the new repo; OR if they can be unit tests of the example's logic without needing rimsky's harness, rewrite as unit tests."

**What was implemented:** Per the CHANGELOG Pass-7 entry: "All four scenario tests (`abandon_on_any_failure_test.go`, `commit_on_all_success_test.go`, `concurrent_staging_test.go`, `sub_stage_verifier_failure_test.go`) moved alongside under `scenarios/` and pass without modification beyond import-path retargeting — they exercise the example's own `store` package, not `pkg:graph/scenario`, so no `t.Skip` was needed." Contrast with Pass 5's pervasive `t.Skip` stubs (divergence #10).

**Inferred reason:** Plan-supplied alternative chosen and verified — the tests turned out to be self-contained against the example's own package.

---

### 16. Pass 7 / cross-pass — `licensing.yml` and helm-chart updates landed beyond what plan tasks enumerated

**What the plan said:** No plan task touches `licensing.yml`. No plan task adds a `bundledServicesImage` block to `deploy/kubernetes/rimsky-chart/values.yaml`. Tasks 32 and 47 only mention image-reference updates in `deploy/*.yml`.

**What was implemented:**

- `licensing.yml`: Renamed `runtime/remote/` → `runtime/peer/` (two locations); removed orphan apache-list entries for `examples/`, `cmd/rimsky-docs-{glossary,llms-full,lint}/`, `dashboards/`, `docs/` (with breadcrumb comments pointing at the new homes).
- `deploy/kubernetes/rimsky-chart/values.yaml`: New `bundledServicesImage:` block (10 lines) added with `repository: ghcr.io/fallguyconsulting/rimsky-services`. Six chart `templates/deployment-*.yaml` files updated for the new image references.
- `feature-index.md`: Major rewrite of the "Bundled service reference impls" section (sibling-repo pointer comment) and the `cmd/` table; `runtime/remote` row renamed `runtime/peer` with expanded purpose description.
- `Makefile`: Five docs-related targets removed wholesale (`docs-glossary`, `docs-llms-full`, `docs-lint`, `docs-roots`, `docs-build`), and `lint` / `test-all` / `build-all` / `lint-docker` targets extended to include `sdk/go`.

**Inferred reason:** "After Code Changes — Required Final Step" rules. The repo's `.claude/rules/rules.md` requires `feature-index.md` + `licensing.yml` updates anytime annotated files move; the implementer followed the rule even where plan tasks didn't enumerate. Helm chart updates count as the implicit dual of the deploy/ compose updates.

---

### 17. Cross-cutting — `@source` / `@diverged` tracked-duplication annotations seeded in `sdk/go/conformance/`

**What the plan said:** Plan never directs annotation work; "tracked duplication" is a cold-read convention.

**What was implemented:** Multiple `@source` + `@diverged` annotations in `sdk/go/conformance/`:

- `sdk/go/conformance/blobbackend/runner.go:31–32` — `@source: foundation/persistence/blob.go::BlobBackend`, `@diverged: true`.
- `sdk/go/conformance/executor/callback_receiver.go:168–169` — `@source: runtime/callback.go::parseAsyncCallback`, `@diverged: true`.
- `sdk/go/conformance/executor/client.go:35,44,53,67,105` — five separate `@source: runtime/executor/...` annotations across `Endpoint`, `Client`, `EventStream`, `NewGRPCClient`, `ClientPool`.

**Inferred reason:** Cold-read convention + depguard constraint. `sdk-purity` blocks the SDK from importing `foundation/` or `runtime/`, but conformance runners need types those packages define. Tracked duplication with explicit `@diverged` is the convention-blessed way to do that.

---

## What matched the plan (sampled)

The vast majority of mechanical moves matched the plan literally: P1's 11 cosmetic `foundation/locks` swaps, the 3 sensor pgtest swaps, the two empty `cmd/rimsky-verifier-*` deletions, the new `consumption-side-isolation` depguard rule (modulo divergence #13), the new `sdk-purity` rule, the `pgx-isolation` allowlist update, the `runtime/remote` → `runtime/peer` rename across all callers, all 7 concept-doc mutations (sdk creation + module-layout + claim-producer + publisher + sensor + executor + replica + conformance), the 6 conformance CLI binaries becoming thin wrappers, the publisher extraction to `sdk/go/publisher`, the ops glue extraction to `sdk/go/ops`, the docs move to rimsky-docs, the crimefinder move to crimefinder, the dashboard move to rimsky-dashboard, and all five CHANGELOG entries (in both rimsky and each sibling repo). The bridge file from `stores/internal/bridge` did land in `sdk/go/server/bridge.go` as planned.
