# Acceptance Coverage Recovery Implementation Plan

**Spec:** .ok-planner/specs/2026-06-02-acceptance-coverage-recovery-design.md
**Goal:** Add a real acceptance gate for each of the ten coverage gaps the `/coverage` diagnostic found, fix any bug a gate flushes, and regenerate `VERIFICATION.md` against the honest result.
**Architecture:** Ten new tests, each driving a real entry point through one of three existing harnesses (services integration harness `lib/services/test/harness/`; in-process scenario harness `test/support/scenario/`; control-api app fixture `controlapi.NewApp` behind `httptest.NewServer`). One new harness helper (`StartSensorHTTP`) and one test-only seam in the runner. No production behavior changes except bug-fixes a gate forces.
**Tech Stack:** Go, testcontainers-go (Docker), chi, pgx, modernc SQLite, gRPC.

---

## How to read this plan

These are **coverage gaps**: the platform already implements the behavior; what is missing is a test that drives the *real value path*. So each gate is a **green-on-arrival** test, gated proof-first by the **revert-check / coupling-proof** pattern the write-plan skill sanctions for green-on-arrival code (the skill's "Acceptance" section): a task adds the test (verification: the named test **passes**), then a coupling-proof task **neuters the exact enforcing site**, confirms the test goes **red** (`! <test>` exits 0 only when the test fails), and **restores** it by editing out and back — never `git stash`/`checkout`/`reset`. The red confirmation is a real executed step; it proves the test is coupled to the behavior, not a tautology.

If a gate's test is **red on arrival** (a real bug, not just missing coverage), the green gate forces the fix: `execute-plan` re-dispatches until the named test passes, and the implementer fixes the bug forward under `.claude/rules/rules.md` ("Fix Every Bug You Find" — fix the code, no workarounds, verify). Two gates change non-behavioral code to *enable* a test (Gate 3's runner seam, Gate 4's fixture extension); both are called out as test infra, not behavior changes.

The **final pass (Pass 8) is the acceptance pass**: it boots the real assembled product (the `rimsky-all-in-one` image with a real `rimsky-sensor-http` image as a peer) and drives the spec's headline acceptance scenario — a real external change firing a real downstream node — ending the plan green.

**Prerequisite for Group-A passes (Pass 7, Pass 8):** the services harness consumes **locally-built** images. Before running Pass 7 or Pass 8, build them: `make core-images` (produces `rimsky-all-in-one:latest`) and `make service-images` (produces `rimsky-store-filesystem:latest`, `rimsky-sensor-http:latest`, etc.). The harness `t.Fatal`s if an image is missing — it never `t.Skip`s.

**Verification command convention:** every `go test -run` command names the *specific* test so the gate proves *this* gate, never a blanket suite. Package paths are written in full.

---

## Pass 1: Gate 7 — sub-graph recursion rejection through the real route

**Goal:** Prove that POSTing a delegate-cycle template to the real `POST /templates` route returns HTTP 400 with `subgraph_recursion_unsupported` (today only the in-memory validator is tested).
**Scope:** Tasks 1–2
**End state:** working
**Verification:** `go test -run TestTemplateRegister_RejectsDelegateCycleOverRoute ./lib/control/controlapi/...`

### Task 1: Add the route-level delegate-cycle rejection test

**Files:** `lib/control/controlapi/templates_test.go` (modify — add one test function)

**Grounding (verbatim from the codebase):**
- The route is registered at `lib/control/controlapi/templates.go:83`: `r.Post("/templates", gate(deps, "template:register", handleDeployTemplate(deps)))`.
- `handleDeployTemplate` (`lib/control/controlapi/templates.go:173`) runs `node.ValidateTemplate(&spec, validatorHooksFor(deps, spec))` and, on `!res.Ok()`, returns HTTP 400 with body `{"error": <ErrTemplateValidation>, "validation_errors": [{"path","msg"}, ...]}` (`templates.go:194-206`).
- The cycle error string is `subgraph_recursion_unsupported: delegate: cycle across graphs: ...`, appended in `detectDelegateCycles` at `lib/graph/node/template_validator_graphs.go:562-567`.
- Harness: `newHarness(t) (*harness, func())` at `lib/control/controlapi/app_test.go:54` (testcontainers Postgres + chi via `httptest.NewServer(app)`); `(*harness).httpJSON(t, method, path, body) (int, map[string]any)` at `app_test.go:104`.
- **`validTemplateBody` (`app_test.go:166`) builds a flat `nodes:` body, NOT a `graphs:` block — do not reuse it.** The delegate-cycle body must carry a `spec.graphs` array. The cycle shape (from `template_validator_graphs_test.go:255-291`): graphs `main → g1 → g2 → g1`, where nodes carry `delegate:` not `executor:` (so the executor-declared hook does not fire first; the cycle trips).

**Steps:**
1. Add `TestTemplateRegister_RejectsDelegateCycleOverRoute(t *testing.T)` to `lib/control/controlapi/templates_test.go`, mirroring `TestTemplateRegister_RejectsUnknownExecutor` (`templates_test.go:52-66`) for the 400-assertion shape.
2. Build the request body as a `map[string]any` of shape `{"spec": {"name": <unique>, "version": "1", "frame_resolution_mode": "coalesce", "graphs": [ {main → delegate g1}, {g1 → delegate g2}, {g2 → delegate g1} ]}}`, matching the struct cycle in `template_validator_graphs_test.go:255-291` (each delegating node uses `"delegate": "<graph>"`; each non-main graph declares `entry`/`exit` and the exit `subscribes` to the entry on `terminal/*`).
3. POST it: `status, body := h.httpJSON(t, "POST", "/templates", reqBody)`.
4. Assert `status == http.StatusBadRequest`.
5. Assert the response carries a `validation_errors` entry whose `msg` contains `subgraph_recursion_unsupported` (range `body["validation_errors"]`, fail if no entry matches).
6. Run the test to confirm it passes against current code.

**Verification:** `go test -run TestTemplateRegister_RejectsDelegateCycleOverRoute ./lib/control/controlapi/...`

### Task 2: Coupling-proof — neuter the cycle detector, confirm red, restore

**Files:** `lib/graph/node/template_validator_graphs.go` (edit out-and-back), `lib/control/controlapi/templates_test.go` (unchanged)

**Steps:**
1. In `lib/graph/node/template_validator_graphs.go`, temporarily neuter the cycle detection: at the `detectDelegateCycles` call site (`template_validator_graphs.go:106`), comment it out (so the cycle is no longer reported).
2. Run `! go test -run TestTemplateRegister_RejectsDelegateCycleOverRoute ./lib/control/controlapi/...` — this must exit **0** (the test now FAILS, because the cycle template is accepted with 201 instead of 400). This is the red confirmation proving the test is coupled to the detector.
3. Restore `template_validator_graphs.go:106` exactly (uncomment the `detectDelegateCycles` call).
4. Run `go test -run TestTemplateRegister_RejectsDelegateCycleOverRoute ./lib/control/controlapi/...` — confirm it passes again.

**Verification:** `go test -run TestTemplateRegister_RejectsDelegateCycleOverRoute ./lib/control/controlapi/...` (green after restore)

---

## Pass 2: Gate 2 — MCP `tools/call` parity with the HTTP verb

**Goal:** Prove that `tools/call instance_create` over `POST /mcp` reaches the real control-api handler and creates an instance equivalently to `POST /instances` (today every `tools/call` test uses a `fakeCatalog`).
**Scope:** Tasks 3–4
**End state:** working
**Verification:** `go test -run TestMCPSkin_ToolsCallParityCreatesInstance ./test/scenarios/auth/...`

### Task 3: Add the MCP `tools/call` parity test

**Files:** `test/scenarios/auth/lifecycle_test.go` (modify — add one test function in package `auth_test`)

**Grounding:**
- `newAuthFixture(t)` (`test/scenarios/auth/lifecycle_test.go:43-99`) builds `controlapi.NewApp` over SQLite behind `httptest.NewServer`, with the REAL auth middleware and the REAL MCP route. `NewApp` wires `POST /mcp` to the real catalog via `registerMCPRoute` (`controlapi/mcp_route.go:45`) and late-binds the finished router into the catalog (`controlapi/app.go:230-232`), so the fixture already routes `tools/call` → real handler.
- Request helper: `(*authFixture).request(t, method, path, key, body) (int, map[string]any)` (`lifecycle_test.go:110`).
- Mint admin via anonymous bootstrap: `POST /auth/keys` with `{"name":"admin","permissions":[{"action":"*"}]}`; the response `body["plaintext"]` is the bearer (`lifecycle_test.go:160-170`).
- Instance tool: name `instance_create`, action `instance:create`, route `POST /instances` (`controlapi/actions.go:206-209`). `tools/call` body: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"instance_create","arguments":{"template":"<tag-or-hash>"}}}`.
- Seed a deployed template first via `seedDeployedTemplate` (`lifecycle_test.go:284-307`), which returns a template hash usable as `template`.
- The closest existing real-catalog MCP test is `TestMCPSkin_OperatorRoleKeyWorks` (`lifecycle_test.go:755`) — mirror its fixture + `POST /mcp` call shape. **Do NOT** use the `fakeCatalog` (`controlapi/mcp/server_test.go:22`) or the `catalog_invoke_test.go` ad-hoc registry.

**Steps:**
1. Add `TestMCPSkin_ToolsCallParityCreatesInstance(t *testing.T)` to `lifecycle_test.go`.
2. Build the fixture, mint an admin key, and `seedDeployedTemplate` to get a deployed template hash `tplHash`.
3. **HTTP path:** `POST /instances` with `{"template": tplHash, "instance_key": "ck-http"}` and the admin bearer; assert `201`; capture the returned `instance_id` and the response field set.
4. **MCP path:** `POST /mcp` with the `tools/call` JSON-RPC body for `instance_create`, `arguments: {"template": tplHash, "instance_key": "ck-mcp"}`, admin bearer; assert `200` and that the JSON-RPC `result` contains a created instance (an `instance_id`), i.e. the same envelope shape the HTTP path returned (assert the result carries the instance-create response fields, not a `fakeCatalog` `{"name": ...}` placeholder).
5. **Parity assertions:** both produced a persisted instance — query the fixture DB (`f.db.Tables().Instances()`) or `GET /instances/{id}` for each and assert both rows exist with the same `template` hash and distinct `instance_key`s.
6. **Audit assertion:** assert the MCP-path call wrote an audit row to `rimsky_events` tagged with the MCP skin (the `WithProtocolSkin` path; assert via `GET /audit` filtered, or query `f.db.Tables().Events()` for a row whose payload/metadata marks `mcp`). If the skin tag is not separately surfaced, assert at minimum the create event exists for the MCP-created instance.
7. Run to confirm green.

**Verification:** `go test -run TestMCPSkin_ToolsCallParityCreatesInstance ./test/scenarios/auth/...`

### Task 4: Coupling-proof — break the catalog→handler re-entry, confirm red, restore

**Files:** `lib/control/controlapi/mcp/catalog.go` (edit out-and-back)

**Grounding:** the catalog→real-handler invoke is `c.Router.ServeHTTP(rec, inner)` at `controlapi/mcp/catalog.go:196` inside `Catalog.Invoke`.

**Steps:**
1. In `controlapi/mcp/catalog.go`, temporarily make `Invoke` not re-enter the router: right before `catalog.go:196`, return a placeholder result (e.g. `return map[string]any{"name": name}, nil`) so `tools/call` no longer runs the real handler.
2. Run `! go test -run TestMCPSkin_ToolsCallParityCreatesInstance ./test/scenarios/auth/...` — must exit **0** (the MCP path no longer creates a real instance; parity assertion fails).
3. Restore `catalog.go` exactly.
4. Run `go test -run TestMCPSkin_ToolsCallParityCreatesInstance ./test/scenarios/auth/...` — confirm green.

**Verification:** `go test -run TestMCPSkin_ToolsCallParityCreatesInstance ./test/scenarios/auth/...`

---

## Pass 3: Gate 4 — operator dashboard auth gate + populated reads

**Goal:** Prove the dashboard read endpoints are gated by `observability:read` (401/403 without grant) and return real counts over seeded runtime state (today's tests bypass the gate and assert empty-DB shape).
**Scope:** Tasks 5–7
**End state:** working
**Verification:** `go test -run TestObservabilityDashboard_GatedAndPopulated ./test/scenarios/auth/...`

### Task 5: Build an auth fixture variant that mounts the real Observability router

**Files:** `test/scenarios/auth/lifecycle_test.go` (modify — add a fixture constructor, or a parameter to `newAuthFixture`)

**Grounding (critical):** `newAuthFixture` passes no `Observability` into `AppDeps`, so the production gate block at `lib/control/controlapi/app.go:190` (`obs.Method("GET", "/v1/observability/*", deps.AuthState.gateByAction("observability:read", deps.AuthState.observabilityWrapper(deps.Observability)))`) **does not mount** — `/v1/observability/...` returns 404, not 403. To traverse the gate, the `AppDeps.Observability` field (type `ObservabilityRouter`, in `lib/control/controlapi/app.go`) must be non-nil. The dashboard routes are registered by `observability.Routes(r, deps)` — note this handler package lives at `lib/control/observability/` (a SIBLING of `controlapi`, not nested under it): `lib/control/observability/handler.go:50`, mounted under `/v1/observability`.

**Steps:**
1. Add a fixture constructor `newAuthFixtureWithObservability(t)` (or extend `newAuthFixture` with an option) that builds `AppDeps` identically but sets `Observability` to an `ObservabilityRouter` closure that calls `observability.Routes(r, observability.Deps{Tables: d.Tables(), Queue: d.Queue(), Discovery: observability.NewDiscovery(<nop prober>)})` (import path `lib/control/observability`). Mirror the `Deps` construction in `lib/control/observability/handler_test.go:213-238` and the wrapper shape at `lib/control/controlapi/app.go:241-245`.
2. Expose the underlying `persistence.Database` (`f.db`) so the test can seed rows (it already does — `authFixture.db`).
3. Build (`go build ./...`) to confirm the new constructor compiles.

**Verification:** `go build ./test/scenarios/auth/...`

### Task 6: Add the gate + populated-counts test

**Files:** `test/scenarios/auth/lifecycle_test.go` (modify — add one test function)

**Grounding:** `GET /v1/observability/system/summary` (`lib/control/observability/handler.go:762-803` `handleSystemSummary`) returns `node_counts`, `instances_active`/`instances_terminated`, `node_runs_claimed`/`node_runs_pending`. **`node_counts` is keyed by node STATE (`fresh`/`stale`/`running`/`failed`), not node type** (`handler.go:779`). Simplest way to populate non-zero counts through the real surface: `seedDeployedTemplate` + `POST /instances` (`lifecycle_test.go:259-307`) creates a real active instance and its nodes, so `instances_active` and the matching `node_counts` state bucket become non-zero without touching persistence internals.

**Steps:**
1. Add `TestObservabilityDashboard_GatedAndPopulated(t *testing.T)` using `newAuthFixtureWithObservability`.
2. **Gate (deny):** issue `GET /v1/observability/system/summary` with **no bearer** → assert 401; mint a key with only an unrelated grant (e.g. `{"action":"instance:read"}`) and repeat → assert **403** (the real `observability:read` gate denies).
3. **Gate (allow) + populated:** mint a key granting `observability:read` (and the grants needed to seed); `seedDeployedTemplate` + `POST /instances` to create a real active instance; then `GET /v1/observability/system/summary` with the `observability:read` key → assert **200**, `instances_active >= 1`, and that a `node_counts` STATE bucket (e.g. `fresh` or `stale` — node_counts is keyed by state, not type) is `>= 1` (a real populated read, not just key-presence).
4. Run to confirm green.

**Verification:** `go test -run TestObservabilityDashboard_GatedAndPopulated ./test/scenarios/auth/...`

### Task 7: Coupling-proof — drop the gate, confirm the deny assertion goes red, restore

**Files:** `lib/control/controlapi/app.go` (edit out-and-back)

**Steps:**
1. In `controlapi/app.go:190`, temporarily replace `deps.AuthState.gateByAction("observability:read", ...)` with the bare wrapper (no gate), so the no-bearer request would get 200 instead of 401.
2. Run `! go test -run TestObservabilityDashboard_GatedAndPopulated ./test/scenarios/auth/...` — must exit **0** (the deny assertions now fail).
3. Restore `app.go:190` exactly.
4. Run `go test -run TestObservabilityDashboard_GatedAndPopulated ./test/scenarios/auth/...` — confirm green.

**Verification:** `go test -run TestObservabilityDashboard_GatedAndPopulated ./test/scenarios/auth/...`

---

## Pass 4: Gate 6 — role-template mint → usable → enforced, per role

**Goal:** Prove each bundled role's JSON expands into a grant that, when minted as a key over `POST /auth/keys`, enforces exactly that grant (representative action 200, non-role action 403) — today only pure-function `CheckGrant` is tested.
**Scope:** Tasks 8–9
**End state:** working
**Verification:** `go test -run TestRoleTemplates_MintAndEnforce ./test/scenarios/auth/...`

### Task 8: Add the per-role mint-and-enforce test

**Files:** `test/scenarios/auth/lifecycle_test.go` (modify — add one test function)

**Grounding:**
- Bundled roles live under `cmd/rimsky/cli/roles/*.json`; load a role's JSON with `roles.Load(name)` and list names with `roles.AllNames` (`cmd/rimsky/cli/roles/embed.go`). A role's `permissions` field is already in the `[{"action": ...}]` wire shape (see `roleGrant` at `cmd/rimsky/cli/roles/audit_read_coverage_test.go:22-35`).
- The server has no role concept — minting a role-shaped key = `POST /auth/keys` with body `{"name": ..., "permissions": <the role's permissions array>}`. Closest pattern: `TestMCPSkin_OperatorRoleKeyWorks` (`lifecycle_test.go:755-783`), which mints an operator-shaped key and hits a real gated surface.
- Role→representative-action map (from the role JSONs): `admin` → grants `*` (e.g. `template:register` 200); `read-only` → grants `*:read` (e.g. `instance:read` 200, `instance:create` 403); `operator` → grants `instance:create` 200 but NOT `auth:create` (403); `publisher-service` → grants `message:send` only (e.g. `message:send` 200, `instance:read` 403). Pick one representative-allowed and one representative-denied action per role; for the denied action choose one the role's grant does **not** cover.

**Steps:**
1. Add `TestRoleTemplates_MintAndEnforce(t *testing.T)` using `newAuthFixture`. Mint an admin key via anonymous bootstrap.
2. Define a table of `{roleName, allowedAction→{method,path[,body]}, deniedAction→{method,path[,body]}}` for the bundled roles (`admin`, `read-only`, `operator`, `publisher-service`, plus `debug-operator`/`agent-supervisor` if a representative route exists). Load each role's permissions via `roles.Load(roleName)` and unmarshal its `permissions` array.
3. For each role: mint a key over `POST /auth/keys` with `{"name": roleName+"-key", "permissions": <loaded permissions>}` using the admin bearer; capture `plaintext`.
4. Assert the **allowed** route returns a non-403 status (200/201/appropriate success) with the role key, and the **denied** route returns **403** with the role key. (Choose routes with cheap side effects or use read routes / dry-run where a write would mutate; `?dry_run=true` is available on writes.)
5. Run to confirm green.

**Verification:** `go test -run TestRoleTemplates_MintAndEnforce ./test/scenarios/auth/...`

### Task 9: Coupling-proof — corrupt one role's grant, confirm red, restore

**Files:** `cmd/rimsky/cli/roles/operator.json` (edit out-and-back), `test/scenarios/auth/lifecycle_test.go` (unchanged)

**Steps:**
1. Temporarily remove the `instance:*` (or the representative-allowed) entry from `cmd/rimsky/cli/roles/operator.json` so the operator key would be denied its allowed action.
2. Run `! go test -run TestRoleTemplates_MintAndEnforce ./test/scenarios/auth/...` — must exit **0** (the operator allowed-action assertion now fails). This proves the test reads the real bundled role JSON, not a hardcoded grant.
3. Restore `operator.json` exactly (the embedded JSON is compiled in via `go:embed`, so the restore must be byte-exact).
4. Run `go test -run TestRoleTemplates_MintAndEnforce ./test/scenarios/auth/...` — confirm green.

**Verification:** `go test -run TestRoleTemplates_MintAndEnforce ./test/scenarios/auth/...`

---

## Pass 5: Gate 5 — capability handshake real probe→cache over the wire

**Goal:** Prove `RunHandshake` with the real `NewGRPCProber` dials a real peer, caches its advertised capabilities, and that `RefreshLoop` flips to unreachable when the peer dies (today only a `fakeProber` is used). Closes both `concept:observability` and `concept:discovery-cache`.
**Scope:** Tasks 10–11
**End state:** working
**Verification:** `go test -run TestHandshake_RealProberCachesAndHeals ./lib/control/observability/...`

### Task 10: Add the real-prober probe→cache test

**Files:** `lib/control/observability/handshake_test.go` (modify — add one test function)

**Grounding:**
- Real prober: `NewGRPCProber()` (`lib/control/observability/handshake.go:37`). Driver: `RunHandshake(ctx, prober, executors []PeerSpec, stores []PeerSpec, log *slog.Logger) *Discovery` (`handshake.go:175`); `PeerSpec{Name, Endpoint, ObservabilityEndpoint string}` (`handshake.go:160`).
- Cache reads: `(*Discovery).GetExecutor(name) (PeerEntry, bool)` (`discovery.go:157`); `Reachability` enum `ReachabilityReachable="reachable"` / `ReachabilityUnreachable="unreachable"` (`discovery.go:25-31`); `PeerEntry.Capabilities *ObservabilityCapabilities` with `DeclaredEvents []string` (`discovery.go:92`) and `ExpectedAttributesSchema []byte` (`discovery.go:85`).
- Real loopback peer that advertises observability caps: `stubtest.Listen(t, stub.New()) (*grpc.Server, addr string)` (`test/support/executors/stub/stubtest/listen.go:24`) registers both the executor server and `stub.RegisterObservability` (`test/support/executors/stub/observability.go:81`). Its `Capabilities` advertises `DeclaredEvents: ["ready","signal","checkpoint","progress","completed"]` and `ExpectedAttributesSchema: {"type":"object"}` (`observability.go:36-65`). Note it advertises `SupportsTraceGet:false` — assert on `DeclaredEvents`/`ExpectedAttributesSchema` (non-empty), **not** `SupportsTraceGet` (the `fakeProber` set that true; the real stub does not).
- **Do NOT** use `fakeProber` (`handshake_test.go:20-61`) — it is the exact coupling the gate closes.

**Steps:**
1. Add `TestHandshake_RealProberCachesAndHeals(t *testing.T)` to `handshake_test.go`.
2. Start a real loopback peer: `srv, addr := stubtest.Listen(t, stub.New())`.
3. Call `disc := RunHandshake(ctx, NewGRPCProber(), []PeerSpec{{Name:"x", Endpoint: addr}}, nil, <test logger>)`.
4. Assert `entry, ok := disc.GetExecutor("x")` with `ok == true`, `entry.Reachability == ReachabilityReachable`, and `entry.Capabilities.DeclaredEvents` equals the stub's advertised list (and `ExpectedAttributesSchema` non-empty) — proving real caps were probed over the wire and cached.
5. **Heal/flip:** stop the peer (`srv.Stop()`), run `RefreshLoop` for one interval against a short ticker (use a context cancelled after one refresh, mirroring `TestRefreshLoop_HealsUnreachable` at `handshake_test.go:102` but with the real prober), and assert `disc.GetExecutor("x")` flips to `ReachabilityUnreachable`.
6. Run to confirm green.

**Verification:** `go test -run TestHandshake_RealProberCachesAndHeals ./lib/control/observability/...`

### Task 11: Coupling-proof — break the Capabilities probe, confirm red, restore

**Files:** `lib/control/observability/handshake.go` (edit out-and-back)

**Grounding:** the probe RPC is `c.Capabilities(ctx, &genv1.ExecutorCapabilitiesRequest{})` in `gRPCProber.ProbeExecutor` (`handshake.go:45-46`).

**Steps:**
1. In `gRPCProber.ProbeExecutor` (`handshake.go`), temporarily make it return a zero/empty `*ObservabilityCapabilities` (or an error) without making the real `Capabilities` call, so no real caps are cached.
2. Run `! go test -run TestHandshake_RealProberCachesAndHeals ./lib/control/observability/...` — must exit **0** (the `DeclaredEvents` assertion fails because nothing real was probed).
3. Restore `handshake.go` exactly.
4. Run `go test -run TestHandshake_RealProberCachesAndHeals ./lib/control/observability/...` — confirm green.

**Verification:** `go test -run TestHandshake_RealProberCachesAndHeals ./lib/control/observability/...`

---

## Pass 6: Gate 3 — post-commit verify-before-run race

**Goal:** Prove the post-commit limb of `@blessed-invariant 5`: when a claim is stolen *between the acquisition commit and the verify-read*, the runner emits `orphaned_claim_lost_race` and does NOT execute (today's scenario test exercises only the candidate-SELECT skip; the post-commit limb is unit-only).
**Scope:** Tasks 12–14
**End state:** working
**Verification:** `go test -race -run TestVerifyBeforeRun_PostCommitSteal ./test/scenarios/...`

### Task 12: Add a test-only post-commit seam to the runner

**Files:** `lib/runtime/runner.go` (modify — add a nil-default hook field to `RunArgs`), `lib/runtime/runner_acquire.go` (modify — invoke the hook between commit and `verifyBeforeRun`)

**Grounding:** `verifyBeforeRun(ctx, args, acq)` is called at `lib/runtime/runner_acquire.go:341`, AFTER the acquisition tx commits and BEFORE the ownership re-read (`verifyBeforeRun` reads `args.Queue.GetClaimedBy(ctx, acq.DispatchID)` at `runner_acquire_postcommit.go:26`). The spec's acceptance scenario explicitly calls for "a forced ownership flip between commit and the verify-read" — a deterministic injection point is required, and the runner has none today.

**Rationale (autonomous call):** a deterministic integration test of this race needs a seam; the alternative (hope the race fires under `-race -count`) is flaky and unfit for a gate. The seam is a `nil`-default `func(context.Context)` field on `RunArgs` — **no production behavior change** (production passes nil; the hook is invoked only if non-nil). This is test infrastructure, not a behavior change.

**Steps:**
1. In `lib/runtime/runner.go`, add a field to `RunArgs`: `// PostCommitHook, if non-nil, runs after the acquisition tx commits and before verifyBeforeRun. Test-only seam (production passes nil).` `PostCommitHook func(ctx context.Context)`.
2. In `lib/runtime/runner_acquire.go`, immediately before the `if !verifyBeforeRun(ctx, args, acq)` call at line 341, add: `if args.PostCommitHook != nil { args.PostCommitHook(ctx) }`.
3. Build (`go build ./...`) and run the existing runner/scenario suites to confirm nothing regressed: `go test ./lib/runtime/... ./test/scenarios/...`.

**Verification:** `go build ./... && go test -run TestVerifyBeforeRunRace ./test/scenarios/...` (the pre-existing candidate-guard test still passes — seam is inert when nil)

### Task 13: Add the post-commit-steal race test

**Files:** `test/scenarios/verify_before_run_post_commit_test.go` (create)

**Grounding:** mirror `TestVerifyBeforeRunRace` (`test/scenarios/verify_before_run_race_test.go:43-115`) for harness setup: `h := scenario.Start(t, scenario.HarnessOpts{NoSupervisor: true})`, deploy a one-node `{Type:"worker",Executor:"stub"}` template, create an instance, find the node, get `mainScopeID := h.GetMainRunScopeID(iid)`. `RunNode` signature: `runtime.RunNode(ctx, RunArgs{...}, nil) (RunnerResult, error)` (`runner.go:406`); `RunnerResult.Ran bool` is false on the lost-race path (`runner.go:78-82`). The event is `orphaned_claim_lost_race`, appended at `runner_acquire_postcommit.go:78` with payload `{dispatch_id, supervisor_id}`.

**Steps:**
1. Create `test/scenarios/verify_before_run_post_commit_test.go` with `TestVerifyBeforeRun_PostCommitSteal(t *testing.T)`.
2. Set up the harness and an **unclaimed** node-run row (so the candidate SELECT succeeds and acquisition commits ownership to this runner) — unlike the existing test, do NOT pre-seed `claimed_by='fake-other'`; let the row be the normal enqueued dispatch (or seed it `claimed_by=NULL`).
3. Build `runtime.RunArgs` as in the existing test (`SupervisorID:"scenario-runner"`, `AcceptedExecutors:["stub"]`, real `executor.NewClientPool()`, `Resolver: executor.NewStaticResolver(...)` pointing `stub` at `h.StubAddr`), and set `PostCommitHook` to a closure that, when invoked, runs a raw SQL `UPDATE rimsky_node_runs SET claimed_by='thief-supervisor', claimed_at=NOW() WHERE id=$1` against `h.Pool` for the dispatch row — flipping ownership in the window between commit and verify-read.
4. Call `out, err := runtime.RunNode(h.Ctx, args, nil)`.
5. Assert `out.Ran == false` (the verify-before-run guard fired) and `err == nil`.
6. Assert the executor was **not** invoked: assert the node remained `cascade.NodeStateStale` (mirror the existing test's `h.FindNode`/state read) and/or that no `terminal/*` event was emitted for the node.
7. Assert an `orphaned_claim_lost_race` event exists for the dispatch: query `rimsky_events` (via `h.Pool` or `h.Persist.Events()`) for a row with `kind='orphaned_claim_lost_race'` and `payload->>'dispatch_id'` equal to the dispatch id.
8. Run under `-race` to confirm green.

**Verification:** `go test -race -run TestVerifyBeforeRun_PostCommitSteal ./test/scenarios/...`

### Task 14: Coupling-proof — neuter the verify-read, confirm red, restore

**Files:** `lib/runtime/runner_acquire_postcommit.go` (edit out-and-back)

**Steps:**
1. In `verifyBeforeRun` (`runner_acquire_postcommit.go:25-33`), temporarily make it `return true` unconditionally (skip the `GetClaimedBy` re-read), so a stolen claim is no longer detected.
2. Run `! go test -race -run TestVerifyBeforeRun_PostCommitSteal ./test/scenarios/...` — must exit **0** (with the guard neutered, the runner proceeds to execute the stolen row; `out.Ran` becomes true and/or no `orphaned_claim_lost_race` event is emitted, so the test fails).
3. Restore `runner_acquire_postcommit.go` exactly.
4. Run `go test -race -run TestVerifyBeforeRun_PostCommitSteal ./test/scenarios/...` — confirm green.

**Verification:** `go test -race -run TestVerifyBeforeRun_PostCommitSteal ./test/scenarios/...`

---

## Pass 7: Gate 10 — real filesystem stage-then-swap through a held subgraph

**Goal:** Prove a real claim producer's stage-then-swap is the value-delivering component in a held-subgraph end-to-end run: aggregate-success drives a real `os.Rename` swap on disk; aggregate-failure drops the staging (today the held-subgraph e2e points at the postgres store whose Commit is a no-op).
**Scope:** Tasks 15–17
**End state:** working
**Verification:** `go test -run TestFilesystemStageThenSwap_HeldSubgraphE2E ./lib/services/test/scenarios/...`
**Prerequisite:** `make core-images && make service-images` (for `rimsky-all-in-one:latest` + `rimsky-store-filesystem:latest`).

### Task 15: Extend the services stub executor to emit a scripted error

**Files:** `lib/services/test/stubexecutor/main.go` (modify)

**Grounding:** the services stub executor (`lib/services/test/stubexecutor/main.go:33-40`) currently sends exactly one `StreamClose_Success` for every dispatch — there is no error path. The Gate-10 abandon case needs the held co-holder set to aggregate to **failure** so auto-terminal fires `Abandon` (drop staging). The stub is **source-built on demand** by `harness.StartExecutorStubOnNetwork` via `testcontainers.WithDockerfile(FromDockerfile{Context: repoRoot(), Dockerfile: ".../Dockerfile.stubexecutor", KeepImage: true})` (`lib/services/test/harness/executor_stub.go:30-37`), so editing `main.go` is picked up on the next run (the Docker layer cache busts when the copied source changes). This is **test infra, not a behavior change** — production never runs this binary, and the default (no env set) stays success-only.

**Steps:**
1. In `lib/services/test/stubexecutor/main.go`, add an env read in `main` for `EXECUTOR_STUB_FORCE_ERROR` and thread the flag into the `server` struct.
2. In `Execute`, when the flag is set, emit a `StreamClose` with an **error** outcome instead of success — mirror the `StreamClose_Success` construction at `main.go:34-39` but use the error outcome variant (`StreamClose_Error` with an `Error{ErrorClass: "stub/forced_error", ...}`; copy the exact proto field names from how `test/support/executors/stub/stub.go` builds an error `StreamClose`, since that scenario stub already emits errors — `lib/services` cannot import it, so duplicate the shape).
3. Keep the default path unchanged: with the env unset, `Execute` still sends one `StreamClose_Success`.
4. Confirm the default success path is intact by running an existing services scenario that uses the stub: `go test -run TestAllInOneSQLite ./lib/services/test/scenarios/...`.

**Verification:** `go build ./lib/services/test/... && go test -run TestAllInOneSQLite ./lib/services/test/scenarios/...`

### Task 16: Add the held-subgraph filesystem-swap e2e test

**Files:** `lib/services/test/scenarios/atomic_staging/fs_held_swap_e2e_test.go` (create)

**Grounding:**
- Bring-up: `harness.NewNetwork` → `harness.StartFilesystemStore(ctx, t, netName, "store-fs", harness.FilesystemStoreSpec{...})` (`lib/services/test/harness/store_filesystem.go:71`) → `harness.BringUpRimsky(ctx, t, harness.WithExistingNetwork(netName), harness.WithClaimProducer("content", "grpc://store-fs:9100", "sync"), harness.WithExecutor(...), ...)`. The helper returns `FilesystemStoreEndpoint{InternalEndpoint, HostDir}`; `HostDir` is the host-side dir mounted at the container's `/workspace`, so the test inspects the on-disk swap via `HostDir`.
- Pick-policy commit-time move config: **use the harness type `harness.FilesystemStoreSpec` (`store_filesystem.go:31`), NOT the in-process `fsstore.PickPolicy` struct** (the `fs_pick_policy_basic_test.go` test uses the in-process struct directly and is NOT a harness mirror — do not follow it for config). The harness renders `on_commit: %s` from a bare string (`store_filesystem.go:162`), but the store unmarshals `on_commit` as an `action.Action`, and `pop_and_move` **requires a target**, expressed as a **one-key map** (`lib/protocols/action/yaml.go:36`, `action.go:78`): a bare `pop_and_move` string errors. So set the policy's `OnCommit` to the flow-style one-key-map string `"{pop_and_move: <target-subdir>}"`. The real swap is `os.Rename(folderAbs, targetAbs)` at `lib/services/stores/filesystem/store/pick_policy.go:364`. Seed folders via `FilesystemStoreSpec.SeedFolders`.
- **`holds:` MUST be expressed as JSON** in the template POST body (`nodes[].holds`) — depguard blocks `lib/graph/node` structs from `lib/services`. The held-subgraph shape (translated from the in-process `holds_only_auto_terminal_e2e_test.go:87-105` to JSON): an `acquirer` node with a claim ref aliased `held` (lifetime `held`) on the `content` producer at the pick-policy selector; a co-holding `verifier` node carrying `"holds": {"held": {"from": "acquirer"}}` and `subscribes` to the acquirer's `terminal/*`. Deploy via `ep.PostJSON(t, "/templates", body)` then `/templates/{id}/deploy`; create instance via `ep.PostJSON(t, "/instances", {...})`. Read node state via `GET /v1/observability/nodes/{instance_id}/{node_type}` (`RimskyEndpoint.GetJSON`).
- Aggregate outcome: auto-terminal fires Commit on all-success, Abandon on any-failure (`auto_terminal_aggregate_outcome_test.go`). **Drive the two cases with two stub-executor instances** (the stub cannot vary per node within one instance — see Task 15): start a success stub `harness.StartExecutorStubOnNetwork(ctx, t, netName, "exec-ok")` and an error stub via the same helper plus the `EXECUTOR_STUB_FORCE_ERROR` env (add a `StartErroringExecutorStub` variant in Task 15's file or pass the env through a small helper). Wire both with `harness.WithExecutor("ok", "exec-ok:9300")` and `harness.WithExecutor("err", "exec-err:9300")`. The **acquirer always uses `executor: "ok"`** (so it acquires + stages); the **commit-case verifier uses `"ok"`** (success → Commit), the **abandon-case verifier uses `"err"`** (error → aggregate-failure → Abandon).

**Steps:**
1. Create `fs_held_swap_e2e_test.go` with `TestFilesystemStageThenSwap_HeldSubgraphE2E(t *testing.T)`.
2. Start the filesystem store with a pick-policy whose `OnCommit` is `"{pop_and_move: committed}"` (a target subdir under the store root), seeding at least one staged folder under the policy's source dir; capture `HostDir`.
3. Start a success stub (`exec-ok`) and an erroring stub (`exec-err`, `EXECUTOR_STUB_FORCE_ERROR=1`); bring up rimsky wiring the fs store as claim producer `content` and both executors.
4. **Commit case:** deploy a held-subgraph template (acquirer `executor: ok` + co-holding verifier `executor: ok`), create an instance, wait for both nodes to reach `fresh`. Assert on disk under `HostDir` that the staged folder has been **moved** into the `committed` target dir (the real `pop_and_move` rename happened) and is gone from the source dir.
5. **Abandon case:** deploy a second held-subgraph template (acquirer `executor: ok` + verifier `executor: err`), create an instance, wait for terminal. Assert on disk that the staged folder was **NOT** moved into `committed` (the abandon path ran; no swap into production). If the verifier's error retries instead of terminating, add an `error_types` block to the template routing the stub's error class (`stub/forced_error`) to `give_up` so the failure is terminal — ground the `error_types` shape against `test/scenarios/give_up_test.go` and `concept:error-policy`.
6. Run to confirm green. (If a real bug surfaces — e.g. auto-terminal does not reach the fs producer's Commit/Abandon — fix it forward under the project rules; the green gate enforces the fix.)

**Verification:** `go test -run TestFilesystemStageThenSwap_HeldSubgraphE2E ./lib/services/test/scenarios/...`

### Task 17: Coupling-proof — neuter the commit-time rename, confirm red, restore

**Files:** `lib/services/stores/filesystem/store/pick_policy.go` (edit out-and-back)

**Grounding:** the commit-time swap is the single `os.Rename(folderAbs, targetAbs)` at `pick_policy.go:364` (the `PopAndMove` action, reached from `Commit` via `store.go:263` `applyPickAction(..., pp.OnCommit)`).

**Steps:**
1. In `pick_policy.go`, temporarily make the `PopAndMove` action a no-op (skip the `os.Rename` at line 364, return nil), so Commit no longer moves the staged folder.
2. **Rebuild the store image** (`make service-images`, or just the filesystem-store target) so the running container reflects the neutered code.
3. Run `! go test -run TestFilesystemStageThenSwap_HeldSubgraphE2E ./lib/services/test/scenarios/...` — must exit **0** (the commit-case on-disk-move assertion fails because the folder was never moved).
4. Restore `pick_policy.go` exactly and rebuild the image (`make service-images`).
5. Run `go test -run TestFilesystemStageThenSwap_HeldSubgraphE2E ./lib/services/test/scenarios/...` — confirm green.

**Verification:** `go test -run TestFilesystemStageThenSwap_HeldSubgraphE2E ./lib/services/test/scenarios/...`

---

## Pass 8 (ACCEPTANCE): Gate 1 + Gate 8 — real sensor → real cascade, then regenerate VERIFICATION.md

**Goal:** The headline acceptance scenario: a real `rimsky-sensor-http` image observes a real external change and a real downstream node fires through rimsky's cascade — and a real publisher's message persists with `sender_kind: publisher` and a derived sender. Then regenerate `VERIFICATION.md` to the honest post-gate state.
**Scope:** Tasks 18–21
**End state:** working
**Verification:** `go test -run TestSensorHTTP_RealExternalChangeFiresDownstreamNode ./lib/services/test/scenarios/...`
**Prerequisite:** `make core-images && make service-images` (for `rimsky-all-in-one:latest` + `rimsky-sensor-http:latest`).

### Task 18: Add the `StartSensorHTTP` harness helper

**Files:** `lib/services/test/harness/sensor_http.go` (create)

**Grounding:** mirror `StartExecutorStubOnNetwork` (`lib/services/test/harness/executor_stub.go:28`) — the sensor takes **no config file** (pure env), unlike `StartFilesystemStore`. The `rimsky-sensor-http:latest` image (Makefile:121) reads env `RIMSKY_SENSOR_HTTP_PORT` (default 9082), `RIMSKY_ENDPOINT` (where it pushes messages, default `http://localhost:8080`), and optional `RIMSKY_SENSOR_HTTP_STATE_DSN`. It registers a gRPC `Publisher` server on its port. rimsky's stable in-network alias is `rimsky:8080` (`RimskyEndpoint.InternalURL == "http://rimsky:8080"`). The sensor must be on the network BEFORE `BringUpRimsky` (rimsky eager-dials publishers for a Capabilities handshake at startup). The **watched URL is per-subscription** (resolved from `resolved_config.url`, `sensor-http/sensor.go:162`), NOT env — so the helper does NOT take a `watchedURL`. Because the sensor watches a URL it must reach from inside its container, the helper must enable **host access** so the sensor can dial a host-side `httptest.Server`: testcontainers exposes host ports to a container via `testcontainers.WithHostPortAccess(port)` (reachable at the alias `host.testcontainers.internal`).

**Steps:**
1. Create `lib/services/test/harness/sensor_http.go` with `const sensorHTTPImage = "rimsky-sensor-http:latest"` and `func StartSensorHTTP(ctx context.Context, t testing.TB, networkName, alias string, hostAccessPorts ...int) (endpoint string)`.
2. Run the image on the network (`tcnet.WithNetworkName([]string{alias}, networkName)`), `WithEnv` setting `RIMSKY_SENSOR_HTTP_PORT=9082` and `RIMSKY_ENDPOINT=http://rimsky:8080` (the stable internal alias — known before rimsky is up). For each port in `hostAccessPorts`, add `testcontainers.WithHostPortAccess(port)` so the sensor can reach the host's `httptest.Server` at `host.testcontainers.internal:<port>`.
3. `WithExposedPorts("9082/tcp")`, wait on `wait.ForListeningPort("9082/tcp")`, register `t.Cleanup` to terminate.
4. Return the in-network endpoint string in the form `WithPublisher` expects — confirm against `rimsky.go`'s publisher rendering (the `publishers: {<name>: {endpoint: ...}}` block at `rimsky.go:531-538`); match what the executor/claim-producer endpoints use (bare `<alias>:9082` or `grpc://<alias>:9082`).
5. Build to confirm it compiles: `go build ./lib/services/test/harness/...`.

**Verification:** `go build ./lib/services/test/harness/...`

### Task 19: Add the real-sensor → cascade acceptance test (Gate 1 + Gate 8)

**Files:** `lib/services/test/scenarios/sensor_cascade_e2e_test.go` (create)

**Grounding:** drive shape from `lib/services/test/scenarios/sqlite_all_in_one_test.go` (NewNetwork → start peer → BringUpRimsky → deploy template → create instance → wait for node state). The sensor is wired with `harness.WithPublisher(name, endpoint)`. A publisher emits to rimsky via `POST /instances/{id}/messages` with `sender_kind: "publisher"` and a `sender` derived from the publisher identity (`sensor-http/sensor.go:433-434`); the subscribing node's subscription matches the message kind, and the cascade flips it `stale` → re-run → `fresh`. Node-state read: `GET /v1/observability/nodes/{instance_id}/{node_type}` returns `{node:{state}, events:[{kind}]}` (`lib/control/observability/handler.go:66`). The sensor's watched URL is per-subscription, reachable from the sensor container via the host-gateway alias.

**Steps:**
1. Create `sensor_cascade_e2e_test.go` with `TestSensorHTTP_RealExternalChangeFiresDownstreamNode(t *testing.T)`.
2. Start a host-side `httptest.Server` whose body the test can mutate; record its port `hostPort`.
3. `netName := harness.NewNetwork(...)`; `sensorEP := harness.StartSensorHTTP(ctx, t, netName, "sensor-http", hostPort)` (host access for the httptest port); `ep := harness.BringUpRimsky(ctx, t, harness.WithExistingNetwork(netName), harness.WithPublisher("watcher", sensorEP))`.
4. Deploy a template whose node subscribes to the sensor's message kind/type; create an instance; register the publisher subscription (the binding into `rimsky_publisher_subscriptions`) with the watched URL set to `http://host.testcontainers.internal:<hostPort>` — inspect `sensor-http/sensor.go` + the subscription-create surface for the exact request shape (`resolved_config.url`). Drive the node to an initial `fresh`.
5. **Fire the real external change:** mutate the host `httptest.Server`'s response body. Wait (bounded poll) for the sensor to observe it and emit.
6. **Gate 1 assertion:** the subscribing downstream node transitions to `stale` then re-runs to `fresh` (poll `GET /v1/observability/nodes/...`).
7. **Gate 8 assertion:** a `message` row persisted with `sender_kind = "publisher"` and a `sender` derived from the publisher identity (not the request body) — assert via the messages/observability surface or a query against `ep.HostDSN`.
8. **Coupling (negative control, since the sensor is a prebuilt image and cannot be source-reverted in-test):** also deploy a second node whose subscription does NOT match the sensor's message kind; assert it does **not** go stale when the sensor fires — proving the cascade fired specifically because of the matching subscription, not spuriously.
9. Run to confirm green. (If the real loop surfaces a bug — sensor→emit→cascade wiring — fix it forward; the green gate enforces it.)

**Verification:** `go test -run TestSensorHTTP_RealExternalChangeFiresDownstreamNode ./lib/services/test/scenarios/...`

### Task 20: Full-suite + race verification

**Files:** none (verification only)

**Steps:**
1. Run the full Go build, suite, and lint per `.claude/rules/rules.md`: `go build ./... && go test ./... && make lint`.
2. Run the race-sensitive legs touched by this plan: `go test -race -count=1 ./lib/runtime/... ./test/scenarios/... ./lib/graph/scheduler/...`.
3. Fix any failure forward before proceeding.

**Verification:** `go build ./... && go test ./... && make lint`

### Task 21: Regenerate VERIFICATION.md to the honest post-gate state (Drift D2)

**Files:** `VERIFICATION.md` (overwrite)

**Grounding:** the spec's D2 section. The ten gates are now green, so `VERIFICATION.md`'s concept→proving-test map can cite, for each concept the `/coverage` report flagged, the test that *best* proves it under the real-acceptance bar.

**Steps:**
1. Re-point the weak citations the `/coverage` report listed to the new gates: `sensor`/`publisher` → `TestSensorHTTP_RealExternalChangeFiresDownstreamNode`; `cascade-graph` → `TestObservabilityDashboard_GatedAndPopulated`; `observability`/`discovery-cache` → `TestHandshake_RealProberCachesAndHeals`; `role-template` → `TestRoleTemplates_MintAndEnforce`; `supervisor` (post-commit limb) → `TestVerifyBeforeRun_PostCommitSteal`; `sub-graph` (route rejection) → `TestTemplateRegister_RejectsDelegateCycleOverRoute`; `atomic-staging`/`claim-producer` (real swap) → `TestFilesystemStageThenSwap_HeldSubgraphE2E`; `control-api`/`permission` (MCP parity) → `TestMCPSkin_ToolsCallParityCreatesInstance`.
2. Correct the verdict prose: replace any "every feature / PASS / 0 shape-only" absolute with the honest post-gate state (the ten gaps are now closed by real acceptance gates; cite them). Do not assert coverage a gate did not establish.
3. This is a docs-only edit (no behavior change) — verification is that the document references real, existing tests.

**Verification:** `rg -c "TestSensorHTTP_RealExternalChangeFiresDownstreamNode|TestMCPSkin_ToolsCallParityCreatesInstance|TestFilesystemStageThenSwap_HeldSubgraphE2E|TestHandshake_RealProberCachesAndHeals|TestVerifyBeforeRun_PostCommitSteal|TestRoleTemplates_MintAndEnforce|TestObservabilityDashboard_GatedAndPopulated|TestTemplateRegister_RejectsDelegateCycleOverRoute" VERIFICATION.md` (all eight new gate tests are cited)

---

## Manual checks after completion

None. Every gate is an automated command. The acceptance pass (Pass 8) is an automated end-to-end gate, not a manual check. The only operator action outside the test commands is the image-build prerequisite (`make core-images && make service-images`) before the Group-A passes (Pass 7, Pass 8), which is a build step, not a verification.
