# Repo reorganization

**Slug:** `2026-05-24-repo-reorganization`
**Status:** Brainstormed; pending plan
**Affects:** repo layout, Go-module structure, depguard rules, `concept:module-layout`, `concept:claim-producer`, `concept:sensor`, `concept:executor`, `concept:conformance`, `concept:replica`, plus a new `concept:sdk`.

## Context

Rimsky's repo has grown past the point where one tree comfortably holds the platform plus its bundled services, its operator dashboard, its compilable reference impls, the prose documentation surface, and the orchestrator demo (`apps/crimefinder/`). This spec carves the project into five repos, with a new `pkg:sdk/go` peer module inside rimsky providing the implementer-facing surface that downstream service authors build against. Pre-v1 license per `file:.claude/rules/rules.md` applies; break-freely, no compat shims, no migration.

Sibling repos have been pre-created by the user at `../rimsky-services`, `../rimsky-docs`, `../crimefinder`, `../rimsky-dashboard`.

Audit performed during brainstorm established that bundled services are nearly standalone today, with three classes of leak:

- **Cosmetic prod leak** — 11 files in `stores/` import `pkg:foundation/locks` instead of `pkg:protocols/claimproducer`; four of the used symbols (`Capabilities`, `ClaimResult`, `OpenOutcome`, `WriteSemantics`) are `type X = claimproducer.X` aliases, two (`WriteSemanticsSync`, `WriteSemanticsStagedAsync`) are constants that equal their `claimproducer` counterparts by value (Go can't alias constants — they're re-declared). Semantic-preserving in both cases.
- **One white-box test** — `code:subscribers/openlineage/subscriber_test.go::seedInstanceWithMainScope` seeds `table:rimsky_instances` and `table:rimsky_run_scopes` with raw SQL.
- **Three pgtest-dependent sensor tests** — `code:sensors/sensor-{http,webhook,object-store}/state_db_test.go` pull `pkg:internal/pgtest` for a Postgres container they don't need rimsky migrations for. (Note: `code:stores/postgres/store/action_vocab_test.go` already uses `testcontainers-go` directly via an inlined `startTestPostgres` helper — see the file's own comment at line 80. It does not need P1.3 treatment.)

`pkg:examples/atomic-staging-fs-producer`, `pkg:stores/common`, `pkg:stores/shared/sql-checks`, `pkg:stores/internal/bridge`, and `pkg:sensors/internal/post` are clean of rimsky-internal imports.

## Target architecture

Five repos post-reorg:

```
github.com/fallguyconsulting/
├── rimsky/                              # core platform
│   ├── protocols/                       # go.mod — wire types (unchanged)
│   ├── foundation/                      # go.mod — primitives (unchanged)
│   ├── sdk/go/                          # go.mod — NEW: implementer-facing surface
│   ├── (root)                           # go.mod — graph/ runtime/ control/ cmd/
│   │   └── control/controlapi/mcp/      # operator MCP shim (in-tree, part of root module)
│   ├── stores/stub/                     # test-double + quickstart no-op (stays — test infra)
│   ├── stores/filesystem/testfixture/   # test-fixture package (stays — see P3 carve-out)
│   ├── stores/postgres/testfixture/     # test-fixture package (stays — see P3 carve-out)
│   ├── executors/stub/                  # test-double (stays — test infra)
│   ├── test/scenarios/                  # gains canaries replacing crimefinder + services in-tree tests
│   └── deploy/                          # unchanged
├── rimsky-services/                     # NEW
│   ├── stores/filesystem/{cmd,server,store,...}  # production-side; testfixture/ stays in rimsky
│   ├── stores/postgres/{cmd,server,store,...}    # production-side; testfixture/ stays in rimsky
│   ├── sensors/{cron,http,object-store,webhook}
│   ├── subscribers/openlineage/
│   └── executors/{claude-agent,http-node,verifier-http,verifier-shape-checks}/
├── rimsky-docs/                         # NEW
│   ├── docs/                            # 49 markdown files
│   ├── cmd/                             # docs-lint binaries; reads RIMSKY_REPO env var
│   ├── examples/atomic-staging-fs-producer/   # compilable Go reference impl
│   └── .vocabulary-lint.yml
├── crimefinder/                         # NEW (TS code-review orchestrator)
└── rimsky-dashboard/                    # NEW (TS web app, Vite + Tailwind)
```

### Module dependency graph inside rimsky

Three Go modules total inside rimsky pre-reorg (`pkg:protocols`, `pkg:foundation`, root) per `file:go.work`; this spec adds a fourth (`pkg:sdk/go`).

- `pkg:protocols` (peer module, no rimsky deps)
- `pkg:foundation` (peer module) → `pkg:protocols`
- `pkg:sdk/go` (peer module, NEW) → `pkg:protocols` only (stdlib + minimal third-party, enforced by new `sdk-purity` depguard)
- Root module → `pkg:protocols` + `pkg:foundation` + `pkg:sdk/go`. Internal layer ordering `graph/` → `runtime/` → `control/` retained. SDK is consumed by `pkg:cmd/rimsky-*-conformance` thin wrappers (calling `pkg:sdk/go/conformance`) and by rimsky's tests (using `pkg:sdk/go/testpg`). Rimsky's calling-side wire code stays rimsky-internal at `pkg:runtime/peer` (renamed from `pkg:runtime/remote`).
- The operator MCP shim at `pkg:control/controlapi/mcp` is part of the root module, not a separate Go module. (The `concept:module-layout` doc currently describes a four-module workspace including an "MCP-server module"; this is a pre-existing concept-doc error corrected by this spec — see Design changes.)

### Cross-repo Go requires

- `rimsky-services` → `pkg:github.com/fallguyconsulting/rimsky/sdk/go` only. Nothing else from rimsky.
- `rimsky-docs` Go modules — docs-lint binaries have no rimsky imports at compile time (read rimsky source files via `env:RIMSKY_REPO` at runtime); examples module imports `pkg:protocols/claimproducer` and `pkg:sdk/go` (server scaffolding).
- `crimefinder`, `rimsky-dashboard` — TS only, no Go deps; consume rimsky via Docker images + HTTP.

### Versioning

Lockstep tags for rimsky-core and `pkg:sdk/go`. Standard Go sub-module convention: root tagged `v0.5.0`, sub-module tagged `sdk/go/v0.5.0`, both cut by the same release script. Each downstream repo tags independently and notes the rimsky tag it validated against in its CHANGELOG. Break-freely license applies symmetrically to root and SDK.

## Phases

Six phases. P1 → P2 → P3 strictly sequential (constraints below). P4, P5, P6 are independent of each other and of P3; they can land in any order, in parallel with P3 and with each other, after P1+P2.

### Load-bearing sequencing constraints

1. **P1 audit fixes must precede P2 SDK creation.** Building the SDK on top of the cosmetic Go swap, the white-box openlineage rewrite, and the pgtest swap means the SDK can credibly claim to be the canonical implementer-facing surface. Reverse order builds the SDK around lint-passing-but-coupled code.
2. **P2 SDK creation must precede P3 services move.** Services in `../rimsky-services` import `pkg:github.com/fallguyconsulting/rimsky/sdk/go`; the module must exist with content first.
3. **P5 (crimefinder) depends on P2.** P5's drift-canary handoff requires the scenario tests added in §P2 to be in place before crimefinder moves out (otherwise there is a silent breakage window for control-api shape + YAML grammar).
4. **P4 (docs) depends on P2.** P4's pre-release reconciliation gate depends on rimsky's release script existing in its post-P2 form.

P6 (dashboard) has no dependencies; can land any time.

### P1: In-repo prep (rimsky-only)

Five units of work, all in rimsky, all landing before `pkg:sdk/go` is created.

#### P1.1 Cosmetic import swap — 11 files in `stores/`

Files (all currently import `corestore "github.com/fallguyconsulting/rimsky/foundation/locks"`):

```
stores/filesystem/store/store.go
stores/filesystem/store/pick_policy.go
stores/postgres/cmd/main.go
stores/postgres/server/server.go
stores/postgres/server/executor_test.go
stores/postgres/store/store.go
stores/postgres/store/action_vocab_test.go
stores/postgres/testfixture/testfixture.go
stores/stub/cmd/main.go
stores/stub/store/store.go
stores/stub/store/store_test.go
```

Swap import alias `corestore "github.com/fallguyconsulting/rimsky/foundation/locks"` → `claimproducer "github.com/fallguyconsulting/rimsky/protocols/claimproducer"`. Of the six used symbols, four (`Capabilities`, `ClaimResult`, `OpenOutcome`, `WriteSemantics`) are `type X = claimproducer.X` aliases at `code:foundation/locks/types.go:102,109,116,161`; two (`WriteSemanticsSync`, `WriteSemanticsStagedAsync`) are constants at `code:foundation/locks/types.go:129,134` that equal their `claimproducer` counterparts by value (Go disallows const aliasing). Semantic-preserving rename either way; no behavior change.

The `pkg:foundation/locks` package retains `NamedLockSpec` and other rimsky-internal types it owns; only the import-side aliasing changes.

**Acceptance:** No `foundation/locks` imports remain outside `foundation/*` and rimsky-internal callers (graph, runtime, control, cmd).

#### P1.2 Openlineage subscriber test — white-box → integration

`code:subscribers/openlineage/subscriber_test.go::seedInstanceWithMainScope` currently seeds `table:rimsky_instances` and `table:rimsky_run_scopes` with raw SQL, then runs the subscriber against that fixture. Rewrite as integration test:

- Stand up rimsky from a locally-built image (`cmd:deploy/build-images.sh`) plus its persistence dependency. Mechanism is implementer's choice (dedicated docker-compose file, testcontainers wrapping the image, etc.).
- Drive rimsky via public API: register a template, create an instance via the operator API, drive it to emit the events the subscriber should pick up.
- Subscriber registers as a peer service and receives callbacks from rimsky over its public callback API.
- Assertions land on the OpenLineage event payloads the subscriber forwards downstream — same correctness check as today, driven from outside rimsky.

**Acceptance:** Subscriber test has zero opinion about rimsky's internal table layout. Test does not import `pkg:foundation/persistence`, `pkg:foundation/shared`, or `pkg:internal/pgtest`.

#### P1.3 Plain testcontainer swap — 3 sensor test files

`code:sensors/sensor-http/state_db_test.go`, `code:sensors/sensor-webhook/state_db_test.go`, `code:sensors/sensor-object-store/state_db_test.go` currently import `pkg:internal/pgtest` for a Postgres container that incidentally has rimsky migrations applied. None need rimsky's schema — they test the sensor's own state-persistence against tables the sensor owns and migrates itself.

(`code:stores/postgres/store/action_vocab_test.go` was originally part of this set but already uses `testcontainers-go` directly via an inlined `startTestPostgres` helper per the file's own comment — it stays as-is.)

Drop the rimsky-migrations dependency. Note: `code:internal/pgtest/pgtest.go::StartFreshPostgresDSN` (declaration at line 81; preceding doc-comment opens at line 61) already provides a no-migrations entry point; the three sensor tests can simply switch to it, no new variant needed. Alternative: inline a small `testcontainers/postgres` setup. (`pkg:internal/pgtest` moves into `pkg:sdk/go` as a public helper in P2; any inline duplication can collapse then.)

**Acceptance:** The three sensor files run against a vanilla Postgres container; rimsky migrations are not applied for these tests.

#### P1.4 Delete stale empty directories

`file:cmd/rimsky-verifier-http/` and `file:cmd/rimsky-verifier-shape-checks/` are empty (verified by `ls`). Delete.

#### P1.5 New depguard rule: `consumption-side-isolation`

Add to `file:.golangci.yml`:

```yaml
consumption-side-isolation:
  list-mode: lax
  files:
    - "**/stores/**"
    - "**/sensors/**"
    - "**/subscribers/**"
    - "**/executors/**"
  deny:
    - pkg: "github.com/fallguyconsulting/rimsky/foundation"
      desc: "Consumption-side binaries implement against protocols/ only. foundation/ is rimsky-internal."
    - pkg: "github.com/fallguyconsulting/rimsky/internal"
      desc: "internal/ is private to rimsky."
    - pkg: "github.com/fallguyconsulting/rimsky/graph"
    - pkg: "github.com/fallguyconsulting/rimsky/runtime"
    - pkg: "github.com/fallguyconsulting/rimsky/control"
    - pkg: "github.com/fallguyconsulting/rimsky/cmd"
```

After P1.1–P1.3 land, this rule passes. Post-P3 the `files` targets no longer have local matches; the rule stays as a defensive guard against re-bundling.

#### P1 acceptance

- `cmd:make lint && cmd:make test-all` clean, including the new depguard rule.
- `file:CHANGELOG.md` `## Unreleased` notes each unit.

### P2: SDK birth (rimsky-only)

#### P2.1 Module scaffolding

- Create `file:sdk/go/go.mod` declaring module `pkg:github.com/fallguyconsulting/rimsky/sdk/go`.
- Add `pkg:sdk/go` to `file:go.work`.
- Third-party dependency budget: stdlib `log/slog`, `go-chi/chi`, `pgx/v5`, `testcontainers-go`, plus `pkg:protocols/`. Nothing heavier.
- Add `sdk-purity` rule to `file:.golangci.yml`: `pkg:sdk/go` imports only `pkg:protocols/` + stdlib + minimal third-party. No imports from `foundation/`, `graph/`, `runtime/`, `control/`, or `cmd/`.

#### P2.2 What lives in `pkg:sdk/go`

Five functional surfaces (per revised Q2 — SDK is implementer-facing only; calling-side does NOT move into the SDK). Exact package names are implementer's call; the content is:

| Surface | Sources | Purpose |
|---|---|---|
| Server | `pkg:stores/internal/bridge` plus common scaffolding currently inlined in each sensor/subscriber/executor `cmd/main.go` and server package | gRPC server scaffolding for implementing claim-producer / executor / lifecycle-subscriber / blob-backend / publisher protocols. Only `stores/` has a dedicated `internal/bridge/` today; the equivalent code in sensors/subscribers/executors is inlined in each impl and gets extracted during P2. |
| Publisher | `pkg:sensors/internal/post` | Publisher-side message-emit helpers: retry+backoff, idempotency-key header convention, callback POST handling |
| Conformance | `pkg:conformance/*` + the library-extracted core of `pkg:cmd/rimsky-*-conformance/main.go` | Conformance runner as a library, invocable from a service author's own Go tests in addition to CLI. Reaches the wire through the generated stubs in `pkg:protocols/proto/v1/gen` directly (no rimsky-internal client wrapper). |
| Testpg | `pkg:internal/pgtest` | Public testcontainer helper: plain Postgres + optional `WithRimskyMigrations` variant |
| Ops | Copy-pasted setup in bundled services' `cmd/main.go` | `slog` setup, healthcheck HTTP endpoint, DSN env-var parser |

#### P2.3 Rimsky-internal calling-side rename

`pkg:runtime/remote` stays in rimsky's root module and is renamed to `pkg:runtime/peer`. The name "remote" implies external-facing surface, but the package is rimsky-internal infrastructure tightly coupled to `concept:supervisor`, `concept:terminal-resolution`, and `concept:discovery-cache` — "peer" matches the `concept:service` vocabulary better.

Calling-side code does NOT move into `pkg:sdk/go`. Per revised Q2.

**Acceptance:** All in-tree imports of `pkg:runtime/remote` updated to `pkg:runtime/peer`; the old path is removed.

#### P2.4 Conformance reorganization

- Conformance-runner logic moves from `pkg:cmd/rimsky-{claim-producer,executor,blob-backend,data-processing,publisher,validation}-conformance/main.go` into `pkg:sdk/go/conformance` (one sub-package per protocol).
- The existing `cmd:` binaries become thin `main.go` wrappers: parse flags, call the library, exit. Build targets in `file:Makefile` unchanged from the operator's perspective.
- External Go service authors gain the ability to invoke conformance from a Go test without shelling out.
- `pkg:cmd/rimsky-conformance-probe` (stub-mode probe) and `pkg:cmd/rimsky-license-check` are not conformance runners; they stay as standalone binaries in `cmd/`.

#### P2.5 In-tree canary scenarios

Add scenario tests under `pkg:test/scenarios/` driven through the `pkg:graph/scenario` harness:

- **Template-registration + run-a-pass scenario** — exercises control-api YAML grammar end-to-end (replaces crimefinder's catch). Registers a template via the operator API, instantiates it, observes the run completes. Implementer should audit crimefinder's e2e test scope to size the canary so it catches what crimefinder catches today.
- **Lifecycle-subscriber callback contract scenario** — exercises the `concept:lifecycle-subscriber` callback API (replaces openlineage white-box's catch beyond what its peer-driven rewrite in P1.2 covers).

These tests live in rimsky proper, run on every PR; breakage lands in the PR that caused it.

#### P2.6 Depguard rule updates

- New `sdk-purity` rule (per P2.1).
- `foundation-purity`: add `pkg:sdk/go` to the deny list (foundation doesn't import the SDK).
- `graph-purity`: add `pkg:sdk/go` to the deny list (graph doesn't import the SDK).
- `runtime-purity`: no change — `pkg:sdk/go` is implicitly allowed.
- `pgx-isolation`: ensure `pkg:sdk/go` is in the allow list (testpg helper uses pgx).

#### P2 acceptance

- `cmd:make lint && cmd:make build-all && cmd:make test-all` clean.
- `cd sdk/go && go build ./... && go test ./...` clean independently (SDK module is self-contained except for `pkg:protocols/`).
- Conformance CLI binaries still build and run the same external contracts.
- All P2-applicable concept-doc mutations from `## Design changes` applied (`concept:module-layout`, `concept:sdk` creation, `concept:conformance`, plus the P2 portions of `concept:claim-producer` and `concept:publisher` covering the `runtime/remote` → `runtime/peer` rename).
- `file:CHANGELOG.md` `## Unreleased` notes the SDK birth + `runtime/remote` → `runtime/peer` rename.

### P3: Services migration (rimsky → `../rimsky-services`)

#### P3.1 What moves

The codebase distinguishes two roles for "bundled" code: **user-facing reference impls** (move to rimsky-services) and **test infrastructure** (stays in rimsky as rimsky-internal fixtures). This carve-out is load-bearing — `pkg:executors/stub` is explicitly documented as test infrastructure at `code:executors/stub/stub.go:11`, `pkg:stores/stub` is used by `file:quickstart/store-stub.yml` as a no-op deployment, and `pkg:stores/{filesystem,postgres}/testfixture/` packages are imported by 30+ in-tree scenario tests.

**Moves to rimsky-services:**

```
stores/filesystem/{cmd,server,store,lifecycle,...}   # everything EXCEPT testfixture/
stores/postgres/{cmd,server,store,lifecycle,...}     # everything EXCEPT testfixture/
sensors/{cron,http,object-store,webhook}/
subscribers/openlineage/
executors/claude-agent/                               # TS, separate npm toolchain
executors/http-node/                                  # Go reference impl
executors/verifier-http/                              # Go reference impl
executors/verifier-shape-checks/                      # Go reference impl
```

Plus the per-service `Dockerfile.*` files inside each subdirectory.

**Stays in rimsky (test infrastructure):**

```
executors/stub/                                       # whole tree, including cmd/ and stubtest/
stores/stub/                                          # whole tree, including cmd/
stores/filesystem/testfixture/                        # test-fixture package
stores/postgres/testfixture/                          # test-fixture package
```

The four "stays" items remain at their current paths for this reorg. An optional cleanup-rename (e.g., to `pkg:test/fixtures/...`) is deferrable to a separate cycle.

**Testfixture-package coupling:** the `stores/{filesystem,postgres}/testfixture/` packages currently import their sibling `store/`, `server/`, `cmd/` packages. After P3, those sibling packages live in rimsky-services. Implementer chooses how to break the import:

- *(preferred)* refactor the testfixture to spin up the rimsky-services-published image of the store (no Go import of the production code — shell out to docker), or
- swap the testfixture's backing store from postgres/filesystem to stub-store while preserving the same test-facing API.

Either keeps the testfixture surface stable for the in-tree scenario tests that import it.

#### P3.2 Layout in `../rimsky-services`

Single Go module at the repo root:

```
rimsky-services/
├── go.mod                                 # depends on rimsky/sdk/go
├── stores/{filesystem,postgres,stub}/
├── sensors/{cron,http,object-store,webhook}/
├── subscribers/openlineage/
├── executors/claude-agent/                # separate npm toolchain inside
├── deploy/                                # service-side compose fragments + Dockerfiles
├── .github/workflows/                     # CI
└── README.md / CHANGELOG.md / CLAUDE.md
```

#### P3.3 Cross-repo Go dependency

- `file:rimsky-services/go.mod` declares `require github.com/fallguyconsulting/rimsky/sdk/go vX.Y.Z` against a tagged rimsky release.
- For local dev across both repos: a per-developer `file:go.work` (not committed; lives outside either repo or is `.gitignore`d) lists both module paths so changes in `pkg:sdk/go` reflect immediately without retagging.
- CI in rimsky-services resolves the tagged dependency from the module proxy; no local-path resolution.

#### P3.4 Scenario-test migration audit

Approximately 30 in-tree test files under `pkg:test/scenarios/` and `pkg:test/smoke/`, plus two conformance binary tests (`code:cmd/rimsky-claim-producer-conformance/main_test.go`, `code:cmd/rimsky-data-processing-conformance/main_test.go`), import paths under `pkg:stores/{filesystem,postgres}/` (production-side, moving out). Each needs a categorization decision during P3 execute-plan:

**Default rule: rewrite to use stub-store.** Tests that exercise rimsky's behavior generically with any backing store get rewritten to use `pkg:stores/stub` (which remains in rimsky as test infrastructure). This covers most tests under `test/scenarios/locks/`, `test/scenarios/fanout_*`, `test/scenarios/held_claim_*`, `test/scenarios/lifecycle/`, etc. — the test is about cascade/scheduler/supervisor behavior; the choice of store is incidental.

**Exception 1: store-specific behavior tests move to rimsky-services.** File-name convention indicates store-specific intent:
- `pkg:test/scenarios/stores/fs_*` — filesystem-specific (pick policies, queue concurrency)
- `pkg:test/scenarios/atomic_staging/pg_verifier_*` — postgres-specific

These tests move to rimsky-services where the postgres/filesystem stores they exercise live.

**Exception 2: redundant tests delete.** If a test is covered by other in-tree scenarios using stub-store, delete rather than rewrite.

**Conformance binary tests** (`cmd/rimsky-{claim-producer,data-processing}-conformance/main_test.go`) — these tests verify the conformance runner against a known-good target. They can keep using `pkg:stores/stub` (which stays in rimsky); the import path remains rimsky-internal.

**Acceptance:** No imports of `pkg:stores/filesystem/{cmd,server,store,...}` or `pkg:stores/postgres/{cmd,server,store,...}` remain outside of `pkg:stores/{filesystem,postgres}/testfixture/`. The testfixture packages either avoid those imports (via the refactor in P3.1) or are themselves rewritten.

#### P3.5 Conformance posture

After P2, conformance is a library in `pkg:sdk/go/conformance`. rimsky-services exercises its own bundled implementations against that library directly from Go tests — no shelling out to `cmd:rimsky-claim-producer-conformance` from CI. A bundled store's "is conformance still happy with me" check is part of the store's own test suite.

#### P3.6 Deployment story

- rimsky-services publishes per-service Docker images via its CI (presumably to `ghcr.io/fallguyconsulting/rimsky-services/<service>`). Tagging convention is operator's choice.
- Rimsky's `file:deploy/` continues to host the reference docker-compose stack. References to bundled services (`store-postgres.yml`, `store-filesystem.yml`, sensor fragments) update to point at published rimsky-services images, pinned to a rimsky-services tag.
- `file:deploy/build-images.sh` in rimsky no longer builds bundled services; it builds only rimsky-core images. rimsky-services has its own equivalent build script.
- `file:CHANGELOG.md` callout: operators upgrading past this point pull bundled services as separate images.

#### P3.7 In-tree drift canary

P2.5 already added scenario tests catching rimsky-side breakage in PRs that introduce them. After P3 lands, rimsky-services' own CI catches the converse: a `pkg:sdk/go` change that breaks a bundled service surfaces in rimsky-services CI on the next bump. Two-side check.

#### P3 acceptance

- rimsky: `cmd:make lint && cmd:make build-all && cmd:make test-all` clean after the move. The moved directories (`stores/{filesystem,postgres}/{cmd,server,store,lifecycle,...}`, `sensors/*`, `subscribers/openlineage`, `executors/{claude-agent,http-node,verifier-http,verifier-shape-checks}`) are no longer referenced anywhere left in rimsky. The carve-out items (`stores/stub`, `executors/stub`, `stores/{filesystem,postgres}/testfixture`) remain in rimsky and continue to satisfy their test/quickstart/conformance roles.
- rimsky-services: `cd rimsky-services && go build ./... && go test ./...` clean against the released `pkg:sdk/go` version. The TS workspace under `executors/claude-agent/` follows its own `cmd:npm test && cmd:npm run build`.
- Reference deployment (rimsky's `file:deploy/docker-compose.yml`) reaches `/health` against published rimsky-services images. The reference deployment may still ship the in-tree `stores/stub` binary as a no-op fallback (per `file:quickstart/store-stub.yml`).
- Concept-doc mutations in `## Design changes` applied.

### P4: Docs migration (rimsky → `../rimsky-docs`)

#### P4.1 What moves

- `file:docs/` — all 49 markdown files
- `file:docs/.vocabulary-lint.yml`
- `pkg:cmd/rimsky-docs-lint`, `pkg:cmd/rimsky-docs-llms-full`, `pkg:cmd/rimsky-docs-glossary`
- `pkg:examples/atomic-staging-fs-producer`
- Four in-tree scenario tests that exercise the atomic-staging example move with it:
  - `code:test/scenarios/atomic_staging/abandon_on_any_failure_test.go`
  - `code:test/scenarios/atomic_staging/commit_on_all_success_test.go`
  - `code:test/scenarios/atomic_staging/concurrent_staging_test.go`
  - `code:test/scenarios/atomic_staging/sub_stage_verifier_failure_test.go`
  
  These tests demonstrate the example's behavior under various conditions; they belong with the example as tests-as-documentation. The remaining tests in the same directory (`pg_verifier_commit_abandon_test.go`, `pg_verifier_conformance_test.go`, `pg_verifier_test.go`) follow P3.4's rule — `pg_verifier` is postgres-specific, so they move to rimsky-services.
- Root-level `file:llms.txt` and `file:llms-full.txt` *if* generated from docs (verify during execute-plan; `pkg:cmd/rimsky-docs-llms-full` writes to `file:docs/agents/llms-full.txt`, but the root-level files may be hand-written or written by other tooling)

#### P4.2 Layout in `../rimsky-docs`

```
rimsky-docs/
├── docs/                                  # the 49 markdown files
├── cmd/                                   # docs-lint, docs-llms-full, docs-glossary
│   └── go.mod                             # docs-tools module — stdlib + yaml only
├── examples/
│   └── atomic-staging-fs-producer/        # compilable Go reference impl
│       └── go.mod                         # examples module — imports rimsky/sdk/go
├── .vocabulary-lint.yml
└── .github/workflows/
```

Two Go modules: `cmd/` (docs tools, lean dependency profile) and `examples/` (rimsky-importing). Keeps the examples' rimsky dependency separate from docs-tools.

#### P4.3 Tool-side `env:RIMSKY_REPO` convention

- `env:RIMSKY_REPO` — path to a local rimsky checkout. Required for lint commands that cross-check citations against source annotations (`@blessed-invariant`, `@concept:`), the concept catalog at `file:.ok-planner/design/concepts/`, and the protocol surface at `pkg:protocols/`.
- Tools log a clear error if `env:RIMSKY_REPO` is unset.

#### P4.4 Pre-release reconciliation gate

- A rimsky release script invokes the docs-lint binaries from a pinned `../rimsky-docs` checkout, with `env:RIMSKY_REPO` pointing at the about-to-release rimsky tip. If lint reports drift, release blocks until docs are reconciled (PR to rimsky-docs).
- Mid-release-cycle drift is expected; reconciliation rides in front of every rimsky release tag.
- Bypass: `--skip-docs-reconciliation` flag for emergency releases. Documented in the release script's help.

#### P4 acceptance

- `cd ../rimsky-docs/cmd && go build ./... && go test ./...` clean.
- `cd ../rimsky-docs/examples && go build ./... && go test ./...` clean against the released `pkg:sdk/go` version.
- Docs-lint binaries run against a rimsky checkout via `env:RIMSKY_REPO` and report cleanly on a freshly-reconciled state.
- Rimsky's release script blocks if docs-lint reports drift.
- `file:CHANGELOG.md` in rimsky notes the docs split.

### P5: Crimefinder migration (rimsky → `../crimefinder`)

#### P5.1 What moves

Everything under `file:apps/crimefinder/` (257 TS files plus its own CHANGELOG, CLAUDE.md, feature-index.md, README, `cold-read/`, `deploy/`). Layout in `../crimefinder` is identical to current; `git mv` is the move.

#### P5.2 Deploy story

- `file:apps/crimefinder/deploy/docker-compose.fragment.yml` references `image: crimefinder/producer:latest`. Confirm this image gets published from the new repo's CI; pin to a tag.
- `file:apps/crimefinder/deploy/rimsky.yml.fragment` references rimsky-as-an-image; verify against the cross-repo image reference convention.

#### P5.3 Drift canary handoff

P2.5's scenario tests in rimsky's `pkg:test/scenarios/` (template-registration + run-a-pass) replace crimefinder's previous in-tree role as the drift signal for control-api shape + YAML grammar.

#### P5 acceptance

- `cd ../crimefinder && npm install && npm test && npm run build` clean.
- Crimefinder against a pinned rimsky image runs a code-review pass end-to-end.
- `file:apps/` directory in rimsky is empty; remove it.
- `file:CHANGELOG.md` in rimsky notes the crimefinder split.

### P6: Dashboard migration (rimsky → `../rimsky-dashboard`)

#### P6.1 What moves

`file:dashboards/rimsky-dashboard/` — TS web app (Vite + Tailwind), index.html, src/, package.json, Dockerfile, postcss.config.js, tailwind.config.js, tests/. Layout in `../rimsky-dashboard` identical to current; `git mv` is the move.

#### P6.2 Deploy story

- rimsky-dashboard's Dockerfile publishes an image via the new repo's CI.
- Rimsky's reference deployment compose snippet referencing the dashboard updates to point at the published image.

#### P6 acceptance

- `cd ../rimsky-dashboard && npm install && npm test && npm run build` clean.
- Dashboard against rimsky's reference deployment renders and functions.
- `file:dashboards/` directory in rimsky is empty; remove it.
- `file:CHANGELOG.md` in rimsky notes the dashboard split.

## Risks

- **In-tree canary thinness.** P2.5 adds scenario tests to replace crimefinder's drift signal. If thin, a control-api or YAML grammar break could land in rimsky and only surface when crimefinder next bumps. Mitigation: audit crimefinder's e2e test scope when implementing P2.5 and ensure the canary exercises an equivalent surface.
- **Conformance library extraction may surface coupling.** When extracting runner logic from `pkg:cmd/rimsky-*-conformance/main.go` into `pkg:sdk/go/conformance`, the implementer may discover the runners coupled to rimsky-internal types (logger conventions, error envelopes). Those couplings must break cleanly — `sdk-purity` prevents importing foundation as a workaround. Pre-v1 license means the fix can be invasive.
- **Pre-release reconciliation gate friction.** P4.4 makes docs reconciliation a blocking gate for rimsky releases. If it fires frequently mid-release, it could become annoying. Mitigation: `--skip-docs-reconciliation` flag for emergency releases.

## Verification items (during execute-plan)

These are concrete facts the design assumes but I haven't verified at brainstorm time. None are load-bearing — they affect detail-level execution but not the structural plan.

- **Root-level `file:llms.txt` and `file:llms-full.txt`** — whether generated from docs or hand-written. Determines whether they move with docs or stay in rimsky.
- **Image-tag conventions** — each downstream repo publishes Docker images; rimsky's reference deployment pins to specific tags. Exact tag scheme (semver, git SHA, calver) is operator choice; spec captures only that images get published.

## Out-of-scope

- **Python RDK** (`file:.ok-planner/sketches/2026-05-14-rimsky-development-kit.md`) — a Python authoring layer over the Go SDK. Separate downstream work; this spec lays the foundation but doesn't deliver the RDK.
- **Package manager** (`file:.ok-planner/sketches/2026-04-26-package-manager.md`) — OCI-registry distribution for graph specs, executors, stores. The reorg makes this easier (each artifact type lives in its own repo) but the package manager itself is future work.
- **`pkg:sdk/ts/` extraction** — deferred per brainstorm Q5; claude-agent moves to rimsky-services as a self-contained TS module.
- **Helm chart updates** — `file:deploy/kubernetes/rimsky-chart/` already lags per `file:CLAUDE.md`'s drift note. The reorg may necessitate further updates (separate images for bundled services); the chart's reconciliation is tracked in its own CHANGELOG cadence, not by this spec.
- **Versioning scheme specifics beyond lockstep** — spec says rimsky and `pkg:sdk/go` are lockstep-tagged; downstream repos tag independently. Specific tag schemes are operator choice.
- **CI plumbing details** — each new repo gets a CI pipeline; specific GitHub Actions / build steps are implementation choices for write-plan.

## Design changes

These mutations are applied by execute-plan as part of the phase tasks that introduce the change. Each is precise enough to apply mechanically.

### Concept: create `file:.ok-planner/design/concepts/sdk.md`

New concept file. Applied in P2.

Template:

```markdown
---
concept: sdk
status: as-is
aliases:
  - rimsky-sdk
  - sdk/go
---

# SDK

## What it is

`pkg:github.com/fallguyconsulting/rimsky/sdk/go` is the canonical Go-side implementer-facing surface for building services that rimsky talks to. A peer Go module within the rimsky repo, alongside `pkg:protocols/` and `pkg:foundation/`. Houses:

- Server scaffolding for claim-producer / executor / lifecycle-subscriber / blob-backend / publisher protocols
- Publisher-side helpers (message-emit retry+backoff, idempotency-key header, callback POST handling)
- Conformance library (`pkg:sdk/go/conformance`) — invocable from service authors' Go tests in addition to the thin CLI wrappers in `pkg:cmd/rimsky-*-conformance`
- Testcontainer helpers (`pkg:sdk/go/testpg`) — plain Postgres + optional `WithRimskyMigrations` variant
- Ops glue — `slog` setup, healthcheck HTTP endpoint, DSN env-var parser

## Purpose

Remove footguns from third-party and bundled service authors (canonical example: the TS `kind`-vs-`type` body-key bug documented in `file:CLAUDE.md`'s gotcha list). Provide one paved path to "implement a service rimsky calls."

## Boundaries

Owns: the implementer-facing surface listed above. Does NOT own: the calling-side wire code (rimsky-internal infrastructure tightly coupled to `concept:supervisor`, `concept:terminal-resolution`, `concept:discovery-cache` — stays in rimsky's `pkg:runtime/peer`). Does NOT own: non-Go languages (a future `pkg:sdk/ts` would be a separate concept if/when it lands).

## Invariants

- `sdk-purity` depguard rule: `pkg:sdk/go` imports only `pkg:protocols/` + stdlib + minimal third-party. No imports from `foundation/`, `graph/`, `runtime/`, `control/`, or `cmd/`.
- Lockstep tagging with rimsky-core: root module tagged `v0.X.0`, sub-module tagged `sdk/go/v0.X.0`, both cut by the same release script.
- Break-freely pre-v1 license per `file:.claude/rules/rules.md`. No deprecation-alias discipline; CHANGELOG entries are the visibility surface for breaks.

## Aliases and historical names

`rimsky-sdk` informally; `sdk/go` in path-form. Created in this reorganization (spec `2026-05-24-repo-reorganization-design`).

## Notes

- 2026-05-24: created as part of the repo reorganization. SDK birth covered in spec `2026-05-24-repo-reorganization-design` phase P2.
```

### Concept: mutate `file:.ok-planner/design/concepts/module-layout.md` in place

Applied in P2.

- **"What it is" section:** Replace the current paragraph with text describing a four-Go-module workspace: `pkg:protocols`, `pkg:foundation`, `pkg:sdk/go` (NEW), and the root module (containing `graph/`, `runtime/`, `control/`, `cmd/`). The operator MCP shim at `pkg:control/controlapi/mcp` is in-tree as part of the root module, NOT a separate Go module (corrects pre-existing concept-doc error: the doc currently claims a separate "MCP-server module" at `mcp-servers/control-api/` which does not exist — verify against `file:go.work`, which lists only three modules pre-reorg). Describe `pkg:sdk/go`'s dependency budget (protocols + stdlib + minimal third-party) and its purpose (canonical implementer-facing surface — links to `concept:sdk`). Remove references to bundled-deliverables directories (`stores/`, `executors/`, `sensors/`, `subscribers/`, `dashboards/`, `examples/`, `apps/`) — they live in `pkg:github.com/fallguyconsulting/rimsky-services` and sibling repos post-reorg.
- **"Boundaries" section:** Add a sentence: `pkg:sdk/go` owns the implementer-facing surface; does NOT own the calling-side wire code (rimsky-internal, stays at `pkg:runtime/peer`).
- **"Invariants" section:**
  - Add `sdk-purity` depguard rule entry.
  - Add `consumption-side-isolation` depguard rule entry (transitional + defensive guard against re-bundling).
  - Update `foundation-purity` entry: deny list adds `pkg:sdk/go`.
  - Update `graph-purity` entry: deny list adds `pkg:sdk/go`.
  - Remove `stores/`, `executors/`, `dashboards/` from `foundation-purity` and `graph-purity` and `runtime-purity` deny lists (those directories no longer exist in rimsky post-P3 + P6; the `consumption-side-isolation` rule covers any future re-introduction).
- **"Notes" section:** Append two entries:
  - `2026-05-24: in-repo audit prep. P1 of spec 2026-05-24-repo-reorganization-design: 11 cosmetic foundation/locks → protocols/claimproducer swaps in stores/*, white-box openlineage subscriber test rewritten as peer-driven integration, 4 sensor/store tests dropped their internal/pgtest dependency, new consumption-side-isolation depguard rule, two empty cmd/rimsky-verifier-* directories deleted.`
  - `2026-05-24: SDK birth + bundled-deliverables migration. P2–P6 of spec 2026-05-24-repo-reorganization-design: new pkg:sdk/go peer Go module (server scaffolding, publisher helpers, conformance library, testcontainer helpers, ops glue); rimsky's calling-side renamed pkg:runtime/remote → pkg:runtime/peer; conformance CLI binaries become thin wrappers over pkg:sdk/go/conformance; production-side bundled stores (filesystem, postgres), sensors, subscribers/openlineage, and production-side executors (claude-agent, http-node, verifier-http, verifier-shape-checks) moved to ../rimsky-services. Test-infrastructure carve-outs (stores/stub, executors/stub, stores/{filesystem,postgres}/testfixture) stayed in rimsky. Docs + docs-tooling + atomic-staging-fs-producer example + four of its scenario tests moved to ../rimsky-docs; apps/crimefinder moved to ../crimefinder; dashboards/rimsky-dashboard moved to ../rimsky-dashboard; in-tree pkg:test/scenarios/ canaries added to replace crimefinder + openlineage in-tree drift signals.`

### Concept: mutate `file:.ok-planner/design/concepts/claim-producer.md` in place

Applied in P2 (for the `runtime/remote` → `runtime/peer` path update at line 19) and P3 (for the rest).

- **P2 — Update calling-side path reference at line 19:** Replace `runtime/remote/` with `runtime/peer/`.
- **P3 — "Boundaries" section:** Replace `The bundled SQL-based store stores/postgres/ additionally registers proto:executor.proto::Executor to support verification of its own staged content` with `The bundled SQL-based store pkg:github.com/fallguyconsulting/rimsky-services/stores/postgres additionally registers proto:executor.proto::Executor to support verification of its own staged content`.
- **P3 — "Aliases and historical names" section:** Replace `the directory name (stores/)` with `the directory name (stores/ in pkg:github.com/fallguyconsulting/rimsky-services for production-side reference impls; pkg:stores/stub stays in rimsky as test infrastructure)`.
- **P3 — Sweep for other in-tree store path references:** Update `stores/filesystem/`, `stores/postgres/` references throughout the doc to `pkg:github.com/fallguyconsulting/rimsky-services/stores/filesystem/` and `pkg:github.com/fallguyconsulting/rimsky-services/stores/postgres/` respectively (notably the reference-implementation enumeration at line 50). Leave `stores/stub/` references rimsky-local.
- **"Notes" section:** Append entry: `2026-05-24: production-side bundled claim-producer reference impls (stores/{filesystem,postgres}) moved out of rimsky to pkg:github.com/fallguyconsulting/rimsky-services. Test-infrastructure carve-outs (stores/stub for test-double + quickstart, stores/{filesystem,postgres}/testfixture as test-fixture packages) stay in rimsky. Boundary statement updated to reflect new home. Also: calling-side gRPC client path updated runtime/remote/ → runtime/peer/ per P2 rename. See spec 2026-05-24-repo-reorganization-design phases P2 and P3.`

### Concept: mutate `file:.ok-planner/design/concepts/publisher.md` in place

Applied in P2 (calling-side rename) and P3 (sensor-path retargeting).

- **P2 — Update calling-side path references** (two occurrences, currently at lines 23 and 42): Replace `code:runtime/remote/publisher_client.go` with `code:runtime/peer/publisher_client.go`. Sweep the file for any other `runtime/remote/` references and update accordingly.
- **P3 — Update bundled-sensor path reference at line 51:** Replace `pkg:sensors/sensor-*/` with `pkg:github.com/fallguyconsulting/rimsky-services/sensors/sensor-*/`. Sweep for any other in-tree sensor-bundled-impl references.
- **"Notes" section:** Append entry: `2026-05-24: calling-side gRPC client path updated runtime/remote/ → runtime/peer/ per P2 rename; bundled-sensor path references retargeted to pkg:github.com/fallguyconsulting/rimsky-services/sensors/* per P3 move. See spec 2026-05-24-repo-reorganization-design.`

### Concept: mutate `file:.ok-planner/design/concepts/sensor.md` in place

Applied in P3.

- **Update path references:** Replace `pkg:sensors/sensor-*/` (line 17 of current sensor.md) with `pkg:github.com/fallguyconsulting/rimsky-services/sensors/sensor-*/`. Sweep the file for any other `sensors/` path references and update to the new location.
- **"Notes" section:** Append entry: `2026-05-24: bundled sensor reference impls moved to pkg:github.com/fallguyconsulting/rimsky-services. Path references updated. See spec 2026-05-24-repo-reorganization-design phase P3.`

### Concept: mutate `file:.ok-planner/design/concepts/executor.md` in place

Applied in P3. The concept doc references `executors/claude-agent` (line 17) and `stores/postgres/` (line 27) by path; both must update. Sweep also for any other in-tree bundled-impl path references.

- **Update production-side path references:** Replace `executors/claude-agent` with `pkg:github.com/fallguyconsulting/rimsky-services/executors/claude-agent`. Replace `stores/postgres/` references with `pkg:github.com/fallguyconsulting/rimsky-services/stores/postgres`. If the doc references `pkg:executors/http-node`, `pkg:executors/verifier-http`, or `pkg:executors/verifier-shape-checks`, update those to their `pkg:github.com/fallguyconsulting/rimsky-services/executors/...` locations.
- **Preserve test-infrastructure references unchanged:** `pkg:executors/stub` stays in rimsky and any references in the doc to its role (test double, conformance target, stubtest in-process wrapper) keep their current paths.
- **"Notes" section:** Append entry: `2026-05-24: production-side bundled executor reference impls (claude-agent, http-node, verifier-http, verifier-shape-checks) moved to pkg:github.com/fallguyconsulting/rimsky-services/executors/. executors/stub stays in rimsky as test infrastructure. Cross-reference to stores/postgres also retargeted. See spec 2026-05-24-repo-reorganization-design phase P3.`

### Concept: mutate `file:.ok-planner/design/concepts/conformance.md` in place

Applied in P2.

- **"What it is" section:** Update the stale binary count. Current doc says "Four standalone binaries" — the actual count is six (`rimsky-{claim-producer,executor,blob-backend,data-processing,publisher,validation}-conformance`). Replace the intro paragraph with text reflecting the post-P2 shape: "Six thin CLI wrappers in `pkg:cmd/rimsky-*-conformance` over a shared library at `pkg:sdk/go/conformance` (one sub-package per protocol)." Also update the per-binary bullet list that follows to enumerate all six binaries (current list covers only four — add entries for `rimsky-data-processing-conformance`, `rimsky-publisher-conformance`, `rimsky-validation-conformance`).
- **"Boundaries" section:** Replace `Owns: the standalone binaries, the two shared fixture packages, the stub-mode probe.` with `Owns: the conformance library (pkg:sdk/go/conformance), the thin CLI wrappers (pkg:cmd/rimsky-*-conformance), the two shared fixture packages, and the stub-mode probe (pkg:cmd/rimsky-conformance-probe).`
- **"Notes" section:** Append entry: `2026-05-24: conformance runner logic extracted from pkg:cmd/rimsky-*-conformance/main.go into pkg:sdk/go/conformance as a library. CLI binaries kept as thin wrappers calling the library. External Go authors can now invoke conformance from a Go test. Also corrected pre-existing stale binary count (four → six) in the "What it is" section. See spec 2026-05-24-repo-reorganization-design phase P2.`

### Concept: mutate `file:.ok-planner/design/concepts/replica.md` in place

Applied in P3. The concept doc references `pkg:sensors/sensor-*/` (line 31), `pkg:executors/*` (line 32), and `pkg:stores/*` (line 33) by path; all must update.

- **Update path references** (production-side only; `pkg:stores/stub` and `pkg:executors/stub` references, if present, stay rimsky-local): Replace `pkg:sensors/sensor-*/` with `pkg:github.com/fallguyconsulting/rimsky-services/sensors/sensor-*/`. Replace `pkg:executors/*` (production-side: `claude-agent`, `http-node`, `verifier-http`, `verifier-shape-checks`) with `pkg:github.com/fallguyconsulting/rimsky-services/executors/*`. Replace `pkg:stores/*` (production-side: `filesystem`, `postgres`) with `pkg:github.com/fallguyconsulting/rimsky-services/stores/*`.
- **"Notes" section:** Append entry: `2026-05-24: path references retargeted from in-tree bundled-impl locations to pkg:github.com/fallguyconsulting/rimsky-services. See spec 2026-05-24-repo-reorganization-design phase P3.`
