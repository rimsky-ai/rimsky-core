# Changelog

## Unreleased

- **`rimsky-cli` and `rimsky-compose.yml`.** Add an operator-facing CLI
  (`core/cmd/rimsky-cli/`) plus a `rimsky-compose.yml` declarative
  manifest format (`core/cli/compose/`). The CLI is a thin client over
  the existing control-api: ergonomic top-level verbs (`run`, `register`,
  `deploy`, `instantiate`, `ls`, `logs`), literal API subgroups
  (`template`, `tag`, `instance`, `node`, `admin`), kubectl-style
  contexts (`ctx list/use/add/rm/current`), and compose-style
  reconciliation (`compose up/down/plan/status`, `dev up/down/status`).
  Compose owns project-prefixed names (`compose:<project>:<tag>`,
  `compose:<project>:<name>`); manual API calls outside that prefix are
  invisible to compose. Apply-once-and-exit, fail-fast with resumable
  retry, exit code 3 on `compose plan` drift (mirrors `terraform plan
  -detailed-exitcode`). Distribution: GitHub Releases, install script,
  Homebrew tap, `go install`, distroless `rimsky/cli` Docker image. Per
  `docs/specs/2026-05-02-rimsky-cli-and-compose-design.md`.

  Post-implementation review fixes:
  - Embedded `deploy/docker-compose.yml` v1 scaffold trimmed to the
    minimal init-supported services (no `store-postgres` / `init-items`)
    and remounts `./.rimsky/rimsky.yml` to match the materialization
    target so `dev up` against a fresh `init` directory does not
    block on missing files. `cli-sync-embedded` Makefile target rewrites
    the same transforms when re-syncing from `deploy/`.
  - Endpoint resolution split into `ResolveEndpoint` (non-compose:
    flag > env > config) and `ResolveEndpointForCompose` (compose:
    manifest-pin > flag > env > config), matching spec §4.1's
    compose-verb override clause and unblocking the manifest's role
    as a deployment pin.
  - `instance events --follow` now tracks a watermark across poll
    cycles instead of relying on `next_cursor` (which the live
    control-api only sets on full pages); the clitest fake mirrors
    that contract.
  - `tag mv` and `tag rm` reject the `compose:` prefix, matching the
    existing `tag create` / `template register --tag` guard.
  - Stateful clitest fixture's `GetTemplate`, `ListTemplates`,
    `GetNode`, `ListNodes` now return value copies, eliminating a
    latent race when concurrent tests mutate state.
  - Plan steps carry an explicit `Destructive bool` set at plan time;
    `destructive()` is a one-line check on the bool plus the live
    undeploy-active-bindings precheck (computed once per apply, not
    once per step).
  - `dev up` / `dev down` forward `--no-color` and `-o` onto the
    delegated compose verb.
  - `--no-color` is consumed by `EmitTable` (bold ANSI headers when
    color is on) and by `formatStep` (green/red `+`/`-` markers).
  - `dev down` loads the manifest once and threads it through the
    optional `infra.down` hook.
  - Compose-up `Source` field sent to the control-api is now
    `manifest:<project>:<tag>` rather than the operator's absolute
    filesystem path.
  - Embedded scaffold's `example.yml` is validated against the same
    executors / stores declared in the embedded `rimsky-compose.yml.tmpl`
    so a misspelled executor in the example would fail the embedded
    test rather than at first `dev up`.
  - Cycle-3 review: `compose plan` now exits 3 when params drift on a
    non-terminal compose-owned instance is detected (mirrors `terraform
    plan -detailed-exitcode`), driven by a new `Plan.HasDriftWarnings`
    field set by `ComputePlan` when it emits the stderr warning;
    embedded `docker-compose.yml` no longer ships the unused
    `claude-agent` executor (it isn't declared by the init scaffold's
    inline `rimsky_config:` and would block the supervisor's
    `depends_on`); `cli-sync-embedded` Makefile target gained a
    matching trim for `claude-agent` plus a buffered-comment pass so
    orphan comments above stripped service blocks no longer leak;
    `RunHealth` and `RunCtxList` now propagate `--no-color` via
    `SetActiveCommonFlags`; smoke test cleanup falls back to a direct
    `docker compose down -v` when the CLI invocation can't reach the
    control-api; dead `ApplyOpts.Yes` field, dead helper functions
    (`hasReservedPrefix` / `hasComposePrefix` / `truncShort` /
    `truncHash`) consolidated to `strings.HasPrefix` and a single
    exported `cli.TruncHash`.

- **Control-plane v1 + store lifecycle protocol.** Templates are now
  content-addressed (`rimsky_templates.id` is `sha256-<64-hex>` over RFC 8785
  JCS-canonicalized spec); tags are movable aliases in `rimsky_template_tags`.
  Four-state template lifecycle (registered/deployed/undeployed/deregistered).
  Six new RPCs on `StoreService` (`OnTemplateRegistered`/`Deployed`/
  `Undeployed`/`Deregistered` + `OnInstanceCreated`/`Terminated`); all stores
  implement all six (the rimsky-side `Store` interface ships an embeddable
  `LifecycleNoop` for stores that don't react). `OpenRequest` gains
  `template_id` and `instance_id` fields. Per-(store, scope) bookkeeping in
  `rimsky_store_lifecycle` drives idempotent fan-out. Unified `rimsky.yml`
  (`RIMSKY_CONFIG`) replaces `RIMSKY_STORES_CONFIG` and the supervisor's
  `executors:` block — declares stores, named_locks, and executors in one
  place. Control-api gains `ExecutorDeclared` validation hook. Per
  `docs/specs/2026-05-01-control-plane-and-store-lifecycle-design.md`.
  Pre-v1: drop+recreate of `rimsky_templates`/`rimsky_instances`; existing
  dev DBs nuked.

- **Stores Protocol Cleanup — store-internal-vocabulary excision.**
  Drops `policy_override` from `CommitRequest` / `AbandonRequest`,
  deletes the `Delete` wire verb (4+1 verbs, was 5+1), replaces
  `OpenResponse`'s implicit all-empty-bytes pool-empty signal with
  an explicit `oneof Acquired | Unavailable` discriminator, and
  removes the `claim_resolutions` template grammar
  (`node.ClaimResolution` Go type deleted; `selectResolutionAction`
  and `fireResolutionVerb` deleted from
  `core/supervisor/auto_terminal.go`). Store disposition
  (commit-vs-release-vs-delete on the store's own state) is
  governed entirely by per-store config (e.g. the postgres
  reference store-service's per-pick-policy `on_commit_default` /
  `on_give_up_default`). Bridge handler switches from
  `encoding/json` to `protojson` for response marshaling so the
  new oneof round-trips correctly. Spec:
  `docs/specs/2026-04-30-stores-protocol-cleanup-design.md`.
  Supersedes v3 §4.1 / §4.5 / §4.7 third-paragraph / §4.10
  invariant 13.1 / §5.1 / §5.2 / §7.8 obligation #3.

- **http-node: fix stub-mode userdata validation ordering bug.**
  `executeCore` validated `userdata.url` before the stub-mode
  short-circuit, so the conformance suite's executor-agnostic
  scenarios (which send `{stub_probe: true}` with no URL) errored
  out before reaching `executeStub`. Move the stub-probe escape
  hatch ahead of URL validation so the suite passes; the
  `malformed_userdata` scenario (which omits `stub_probe`) still
  exercises the URL check. Discovered while running the v3 T57
  conformance verification against the reference http-node.
  (`executors/http-node/server.go`)

- **Stores Redesign v3 — out-of-process store-services.** Standard
  store implementations (`filesystem`, `postgres`, `stub`) move from
  in-process Go subpackages of `core/store/` to standalone binaries
  under `stores/<kind>/`. Rimsky processes (`rimsky-supervisor`,
  `rimsky-scheduler`, `rimsky-control-api`) talk to them exclusively
  via the new 5+1-verb gRPC protocol defined in
  `proto/v1/store_service.proto` (Open / Commit / Abandon / Delete /
  Release plus a startup Capabilities() handshake). Spec:
  `docs/specs/2026-04-27-stores-redesign-v3-design.md`.

  Headline changes:
  - **`Factory` / `Registry.BuildAll` / `StoresConfig` removed**
    from `core/store/`. Registry collapses to a name → Store map
    populated externally by each rimsky cmd binary at startup.
  - **`stores.yml` schema rewritten**: thin name → endpoint +
    declared capabilities form (no `kind`, no `connection`, no
    `pick_policies` — store-service-specific keys live in each
    store-service's own config).
  - **Atomicity decoupled** (invariant 10 clarified): rimsky's
    bookkeeping tx is independent of the store-service's tx. The v2
    tx-sharing mechanism (`store.WithTx` / `TxFromContext`) is gone
    along with `core/store/tx.go`. Store atomicity is the store's
    concern (per the new §7.8 obligations).
  - **Region conflict is byte-equal** (invariant 14 retired):
    `Store.RegionsConflict` and `Store.UnmarshalRegion` are removed;
    rimsky compares `rimsky_lock_holders.region_data` byte-for-byte.
    Stores canonicalize region bytes such that byte-equal
    indicates conflict.
  - **Filesystem store-service: glob support dropped**
    (concrete-paths only). Operators needing globs write a custom
    store-service.
  - **4 inertness violations gone (structurally impossible)**: the
    rimsky-side admin items endpoint
    (`/admin/stores/.../pick-policies/.../items`), the pick-policy
    validator hook, the scheduler visibility-timeout sweep, and the
    `*pgstore.Store` store-internal methods (`InsertItems`,
    `PickPolicyConfig`, `PickPolicies`). The postgres store-service
    ships with its own admin endpoint for items insertion (separate
    listener port).
  - **`rimsky_lock_holders.id` generated client-side** (so it can be
    passed to `Store.Open` as `claim_id` per spec §4.2). Column
    default `gen_random_uuid()` retained as safety net.
  - **Invariant 15 revised**: `Open` still fires inside the
    rimsky-side acquisition tx, but the store's state mutation runs
    in its own tx.
  - **Held-claim resolution mechanically updated**: store verb
    calls go through the remote-client gRPC path; the store-side
    action runs in its own tx (no longer shares a tx with the
    lock-holder DELETE).
  - **Deployment**: three new Dockerfiles (`stores/{filesystem,
    postgres,stub}/Dockerfile.<kind>`); `deploy/build-images.sh`
    builds all 9 images; `deploy/docker-compose.yml` adds two new
    services (`store-filesystem`, `store-postgres`) and removes the
    `init-items` one-shot's coupling to rimsky's admin route.

- **Stores Redesign v2 — code-review correctness pass.** Closed the
  `findInheritedAliasesForNode` cartesian-product bug (per-row
  resolution joining the lock-holder back to the acquirer NodeType +
  alias; aliases disambiguated via the substituted selector when an
  acquirer declares multiple stores against the same store_name). The
  per-node claim_holders flip is now a single targeted UPDATE on
  `(lock_holder_id, holder_node_id)` (`CompleteByLockHolderAndNode`)
  rather than the prior list-then-loop. The terminal release path
  reads `region_data` and `address` from the lock-holder row inside
  the release tx (per spec §13.6), removing the dependency on
  `lk.ClaimResult` for async-callback resumed flows. `buildLockSpecs`
  now resolves `{{claim.<alias>...}}` substitutions against the
  inheritor's live claim-holder rows so downstream selectors that
  reference inherited claims resolve at dispatch time. Inheritance
  validator now rejects ambiguous-acquirer inheritance (multiple
  reachable acquirers per inheritor) at deploy time, and
  `HoldingSubgraphsForTemplate` reproduces the same deps-walk so
  deploy-time and runtime subgraph computations agree. `FrameID` is
  now plumbed through `storage.LockHolderInsertInput` and
  `storage.ClaimHolderInsertInput` so the storage adapter populates
  `frame_id` on writes (observability-only per spec §12.10/§12.11).
  Postgres factory honors `connection: postgres://...` per-store
  config — opens its own pool for the store rather than silently
  reusing the platform pool. Strict equality `err == pgx.ErrNoRows`
  in `auto_terminal.go` switched to `errors.Is`. Cleaned dead
  `RebindForResume` / `ListByNodeAndStore` / `ClaimEligibilityInput`
  / `ClaimHolderAction*` symbols and the stale doc comments
  referencing the dissolved `AcquireLock` / `OpenHandle` /
  `ReleaseLock` / `claim_store-postgres` vocabulary. CLAUDE.md now
  references the v2 spec; proto and TS executor stale spec
  references repointed; TS bindings dropped the wire-reserved
  `resumed?` field; `make proto-gen` regenerated `node_executor.pb.go`
  with the reserved fields removed. Reconciled blessed-invariant 20
  doc-blocks: `walkPath` is the sanctioned payload-walk site,
  `stringifyRaw` is the sanctioned address/region shape-flattener,
  and `runner_dispatch.go::makeStoreHandle` is the sanctioned
  wire-encoding site. Added substantive tests: postgres store
  Open/Commit/Abandon/regional/factory-rejects-bad-items-table/
  factory-honors-connection (`core/store/postgres/store_test.go`),
  CompleteByLockHolderAndNode + LockHolders FrameID round-trip
  (`core/storage/postgres/postgres_test.go`), CheckAndFireResolution
  aggregate-completed and aggregate-failed paths (`core/supervisor/
  auto_terminal_test.go`), end-to-end claim release at terminal
  (`test/scenarios/locks/atomic_acquisition_test.go`), regional
  claim run-to-completion (`test/scenarios/stores/
  regional_claim_test.go`), params substitution at dispatch and
  required-source-failure routing (`test/scenarios/attributes/
  substitution_dispatch_test.go`), and auto-terminal aggregate-
  outcome with active-row-blocking guard (`test/scenarios/claim_stores/
  auto_terminal_aggregate_outcome_test.go`).

- Stores Redesign v2 (third major rewrite of core/store/):
  - 5 protocol verbs (Open, Commit, Abandon, Delete, Release) replace the prior AcquireLock/OpenHandle/Commit/ReleaseLock shape.
  - Two-noun primitives split: claim (store-bound) vs named lock (store-independent).
  - Pick policies are store-side via store-recognized selector forms (`@policy-name` convention).
  - Held claims via explicit `inherits:` declarations; auto-terminal at holding-subgraph completion.
  - Capability struct collapsed to one field (write_semantics).
  - Schema: rimsky_lock_holders gains address column, drops claim_id; rimsky_claim_holders gains lock_holder_id FK, drops actual_action/delete_won.
  - Inertness invariant 20 added; pre-sweep type-hardening of claim-content fields to json.RawMessage.
  - Operator config gains named_locks: top-level block.
  - Versions permanently eliminated; versioned mode does not exist.
  - claim-store-postgres renamed to postgres; pick_policies block configures multiple named pick policies per store.

- **Held-claim resolution: per-active-cycle uniqueness + frame-scoped sibling counts.** The smoke test was reproducibly stranding 2–3 items in `topics_items.state='in_progress'` after Phase 2 cascade-steady-state. Root cause: ring-buffer claim stores reuse `claim_id` (= items-table `item_id`) across cycles, but the `rimsky_claim_holders` unique index on `(claim_id, holder_node_id)` was unconditional. The second cycle's `insertHeldClaimHolders` failed the unique constraint against the prior cycle's now-completed row; the supervisor's commit tx rolled back, but the acquisition tx had already flipped the items-table row to `in_progress`, leaving it stranded. Fixes: (1) the unique index is now partial on `state='active'` (`core/migrations/001-initial.sql`), enforcing "one ACTIVE holder per (claim, leaf) at a time" while permitting historical rows to coexist; (2) `claimstorepg.Store.ResolveOnTerminal` filters its FOR UPDATE SELECT on `state='active'` and scopes the §5.6.4 sibling-count predicates by `frame_id IS NOT DISTINCT FROM R.frame_id` so prior cycles' completed delete/delete_won rows don't leak into the "did anyone delete?" check for a fresh cycle on a reused claim_id. Spec §5.6.4 + §9.9.3 updated to match. Smoke test gains an on-failure diagnostic dump in `assertFinalState` so a future regression prints stuck items + their claim_holders + dispatch rows without manual `psql` instrumentation. Smoke now passes 3-of-3 consecutive runs (~47s each).
- **`docs/architecture.md`** gains a new §4.1.1 "Frame engine" section describing `core/frame/` (producer + engine), how `frame.RunTick` runs under the existing scheduler advisory lock, and how `frame_id` propagates through the schema. Cross-references the frame-resolution design doc and the conceptual section in `docs/node-graph-design.md`.
- **`runner_terminal.go` cascade-message comment** updated to describe what the SQL guard `(state = 'fresh' OR (state = 'stale' AND frame_id IS NULL))` actually defends against under the frame model — `Create()` defaults to `'fresh'`, so the `stale + no-frame_id` branch is a defensive backstop for orphan-reap recovery / future paths, not the initial-create case the prior comment named.

- **Frame resolution** (single coherent change; see `docs/specs/2026-04-26-frame-resolution-design.md`). The cascade engine gains a first-class **frame** primitive — a complete pass over the reachable subgraph from one or more invalidation sources, executing serially per instance. Two modes: `frame_resolution: serial_queue` (each invalidate produces a distinct frame, FIFO) and `frame_resolution: coalesce` (mid-render invalidates collapse into one trailing frame). Required at the template level — control-api rejects template uploads without it. Default `frame_timeout_ms` is 600000 (10 min), hard floor 60000. Closes the smoke-test cascade-coalescing gap. **BREAKING:** dev DB must be nuked; templates must declare `frame_resolution`.
  - **New schema:** `rimsky_frames` table (frame_id, instance_id, mode, state, source_node_ids, queued_at, started_at, ended_at, frame_timeout_ms) with `uq_rimsky_frames_running` (at most one running per instance) and `uq_rimsky_frames_coalesce_queued` (at most one pending coalesce row per instance). `frame_id` columns added to `rimsky_dispatch` (NOT NULL), `rimsky_nodes` (nullable, cleared at fresh, preserved at failed), `rimsky_lock_holders` (observability), `rimsky_claim_holders` (observability).
  - **New package:** `core/frame/` — `EnqueueOrCoalesce` producer helper (called from `core/scheduler/invalidate.go`'s `InvalidateNode`, schedule_ticker, and admin force-fire indirectly) and `RunTick` engine (frame-end detection, queued→running advancement, stuck-frame reaper, orphan-dispatch reaper). The scheduler tick invokes `frame.RunTick` under the existing `pg_try_advisory_lock(SCHEDULER_TICK_KEY)`.
  - **Removed:** `rimsky_nodes.kill_requested` column, `core/supervisor/runner_dispatch.go::isKillRequested`, the heartbeat-tick kill-poll path in `core/supervisor/supervisor.go`, the controlapi `POST /nodes/{id}/kill` route + `handleKillNode` handler, and the `KillRequested` field on storage's `NodeRow` + storage interface. Operator-originated invalidates now enqueue or coalesce a frame; in-flight work is never preempted.
  - **New blessed invariants** (§18 — 15-19): (15) frame_resolution mode is mandatory and per-template; (16) at most one running frame per instance; (17) at most one queued coalesce frame per instance; (18) frame-start atomicity (queued→running CAS + source-stale writes in one tx); (19) frame_id flows with cascade — no NULL on dispatch rows or non-fresh node rows.
  - **New scenario suite** at `test/scenarios/frame_resolution/` (14 tests covering all 5 invariants, both modes, async handoff, pruning via changed:false, frame timeout reaper, failed-node frame outcomes, controlapi rejection of bad templates).
  - **Smoke fixture** declares `frame_resolution: serial_queue`, restoring the §19.2 acceptance predicate (≥100 terminal commits per 100 force-fires).
  - **Migration 002** (`core/migrations/002-frame-resolution.sql`): creates `rimsky_frames`, adds `frame_id` columns, drops `kill_requested`. Pre-v1 destructive: in-flight cascades are abandoned (`rimsky_nodes.state` forced to `failed` for stale/running rows; `rimsky_dispatch` truncated).

- **Stores redesign** (single coherent change; see `docs/specs/2026-04-25-stores-redesign-design.md`). The `Resource` abstraction and its surrounding template/dispatch/protocol vocabulary have been replaced end-to-end by a unified **store** abstraction with explicit **lock**, **claim**, **region**, and **attributes** vocabulary. **BREAKING:** dev DB must be nuked before adoption (no migrations from the old schema — `core/migrations/001-initial.sql` is rewritten in place to the §9 end-state schema; `core/migrations/002-data-ref-jsonb.sql` is deleted).
  - **Removed concepts:** the `core/resource/` package and its two impls (`inlinejsonb`, `externalsql`); template fields `owns_resources` / `reads_resources` / `instance_params` / `concurrency_tags` and the matching node columns; `Complete.result` and the `deps_data` / `reads_data` request fields on the wire and in storage; `RestoreVersion` everywhere (template grammar, `InvalidateArgs`, scheduler `invalidateRestorePath`, control-api `nodes.go` decode, `node.ReasonRestoreVersion`, related event payloads); `core/storage/postgres/resources.go` + `resource_data.go`; `core/controlapi/resources.go`; the legacy `concurrency_tags` predicate in the dispatch SQL; the two scenario tests `double_buffering_test.go` and `rollback_via_restore_version_test.go` (sidecar mode + versioned mode are post-v1); `docs/resource-author-guide.md`.
  - **Added concepts:** the `core/store/` package — `Store` / `LockSpec` / `LockHandle` / `Capabilities` / `ReleaseAction` / `ClaimResult` interfaces plus a `Registry` (`core/store/registry.go`), shared `rimsky_lock_holders` postgres helpers (`core/store/lockholders.go`), and three reference implementations: `core/store/filesystem/` (direct-mode, region-glob `RegionsConflict` purity, `SupportsRegionLock` + `SupportsResume`), `core/store/claimstorepg/` (postgres-backed claim store with FIFO acquire / on-commit release-actions / hold + reference-counted resolution per §5.6.4 in `holders.go`), and `core/store/stub/` (in-process test fixture used by the migrated scenarios). New template grammar: `stores: [{name, claim?, hold?, write?, read?}]`, `locks: [{name, mode, limit?}]`, `attributes: {schema}` with `properties[*].source: "{{deps.<n>.<f>}}" | "{{claim.<store>.payload.<f>}}" | "{{params.<k>}}"`, and `claim_resolutions: [{store, on_commit, on_give_up}]` (§11.4 holding-subgraph DAG walk validated at template-deploy). New `core/attributes/` package owns single-pass substitution (`substitution.go`), JSON-Schema validation at both dispatch and commit gates (`validate.go`), and the §12.5 incremental-writeback HTTP handler `POST /v1/attributes/{node_id}` (`callback.go`). New unified locks: `kind in ('named','region','claim')` rows in `rimsky_lock_holders`, atomic dispatch-claim + lock-insert + store `AcquireLock` per §13.3, deterministic sorted acquisition, and the `5 × heartbeat_interval` orphan reap. New error classes `template_resolution_failed` and `attributes_schema_failed` in the policy chain. New admin endpoints on control-api: `GET /claims/{claim_id}/holders`, `POST /admin/claim-stores/{name}/items`, and `POST /admin/scheduled-nodes/{node_id}/force-fire` (used by the smoke fixture; immediately updates `rimsky_schedules.next_fire_at = now()` and returns 204). New stores config plumbing: `RIMSKY_STORES_CONFIG` (loaded by `rimsky-supervisor`, `rimsky-control-api`, and `rimsky-scheduler`); reference `deploy/stores.yml` declaring `content` (filesystem direct) + `topics-ring` (claim-store-postgres). New protocol: `proto/v1/node_executor.proto` rewritten per §12 (`ExecuteRequest{NodeId, InstanceId, NodeType, Userdata, Attributes, Stores[], Locks[], CancelToken, CallbackUrl}`, `Complete.attributes_delta` replacing `result`, `userdata` opaque end-to-end). New scenario buckets `test/scenarios/{stores,locks,attributes,claim_stores}/` and the §19.2 smoke fixture at `test/smoke/`. New doc `docs/store-author-guide.md`.
  - **Blessed invariants** are now 14 (§18): the eight pre-existing invariants plus four new ones — (9) lock state lives only in postgres; (10) lock acquisition is atomic with dispatch claim; (11) userdata is opaque to rimsky; (12) attributes validate twice (dispatch + commit); (13) first-delete-wins, last-released-wins held-claim resolution; (14) `RegionsConflict` and `UnmarshalRegion` are pure. Invariants 3 and 4 are generalised: all locks (named, region, claim) acquired in deterministic sorted order, and every `rimsky_lock_holders` delete plus every `rimsky_dispatch.claimed_by = NULL` is claimant-guarded.
  - **Helm chart drift** at `deploy/kubernetes/rimsky-chart/` was best-effort updated (stores-config ConfigMap mounted on scheduler / supervisor / control-api; env vars realigned to the binaries' actual contracts; supervisor ConfigMap rewritten to the `yamlConfig` shape consumed by `core/cmd/rimsky-supervisor/main.go`). Remaining known drift not repaired: chart not validated via `helm lint` / `helm template`; no provisioning Job for the operator-owned `topics_items` table; no shared PVC for the `content` filesystem store across supervisor + executors; no Service for the supervisor's callback endpoint. Pick this back up under live cluster validation.
  - **Smoke acceptance status:** `go test ./test/smoke/... -count=1 -timeout 10m` reaches steady state on the ring-buffer / dispatch / lock-holders / claim-holders predicates, but the `>= 100 work_completed` counter coalesces to ~2-4 review completions per run because the cascade implementation merges successive upstream invalidates into single downstream runs. All other suites (`./core/supervisor/...`, `./core/attributes/...`, `./core/store/...`, `./core/node/...`, `./test/scenarios/...`, `./conformance/...`) pass.
