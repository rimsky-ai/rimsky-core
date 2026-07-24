# Acceptance Coverage Recovery — Design

**Date:** 2026-06-02
**Source:** `.ok-planner/coverage/2026-06-02-coverage-report.md` (the `/coverage`
diagnostic this spec closes)

## Goal

rimsky carries a large real end-to-end test corpus, but the `/coverage`
diagnostic found **ten acceptance gaps**: capabilities the platform implements
and prominently claims, where no test drives the *real value path* end-to-end —
the existing "proving" test stubs the very component the claim exists to
exercise, bypasses the real entry point, or asserts a shape instead of a real
outcome. It also found **one drift symptom**: `VERIFICATION.md` over-claims
complete behavioral coverage.

This spec is one unit of work: **add a real acceptance gate for each of the ten
gaps, fix any bug a gate flushes out, and regenerate `VERIFICATION.md` against
the honest result.** It deliberately spans many surfaces because the deliverable
is a single property — *every claimed use case has a real acceptance gate* — not
a feature. There are no product-scope forks: every gate has the same shape
(drive the real value path, assert the real observable outcome) and the project's
existing fix-discipline (`.claude/rules/rules.md` "Fix Every Bug You Find")
governs anything a gate surfaces.

Each gate's prose acceptance scenario below becomes that gate's mandatory
acceptance pass when `write-plan` turns this spec into a plan; `execute-plan`
writes and runs each against the real product.

## Approach

**Reuse the existing harnesses; do not build a new test framework.** Three real
harnesses already exist and each gate slots into the right one:

- **Services integration harness** (`lib/services/test/harness/`) — boots the
  real `rimsky-all-in-one:latest` image on a Docker network via testcontainers
  (`harness.BringUpRimsky`), wires real bundled service images as peers
  (`harness.WithClaimProducer` / `harness.WithExecutor` / `harness.WithPublisher`),
  and can run on the baked SQLite default (`harness.WithSQLite`) or a Postgres
  testcontainer. Real images, real wire. Used by gates that need a *real bundled
  component* (sensor, publisher, filesystem swap).
- **Scenario harness** (`test/support/scenario/`, `scenario.Start`) — assembles a
  real scheduler + supervisor + control-api in-process over a real Postgres
  testcontainer with a real-gRPC stub executor; `DeployTemplate` / `CreateInstance`
  go over real HTTP. Used by gates whose value is rimsky's own orchestration.
- **Control-api app** (`lib/control/controlapi`, `NewApp` behind
  `httptest.NewServer`, as in `test/scenarios/auth/lifecycle_test.go`'s
  `newAuthFixture`) — the real chi router + real auth middleware over real
  persistence. The auth-fixture pattern (`newAuthFixture`, and
  `lib/control/observability/handler_test.go`'s `newSQLiteDriver`) is
  **SQLite-backed and needs no Docker**; a Postgres-backed control-api harness
  (`lib/control/controlapi/app_test.go`'s `newHarness`) is also available where a
  gate wants it. The backend is not the point for these gates — the route/gate
  behavior is. Used by gates whose value is a control-plane route's behavior.

**One harness addition is required** (gate #1): the services harness has
`harness.WithPublisher(name, endpoint)` wiring but **no helper that starts a real
sensor image** (it has `harness.StartFilesystemStore` for the store but no
sensor analogue). Gate #1 adds `harness.StartSensorHTTP` / `harness.StartSensorCron`
(mirroring `StartFilesystemStore` in `lib/services/test/harness/store_filesystem.go`,
which starts `rimsky-store-filesystem:latest` on the network and returns an
endpoint). Gates #8 and #10 reuse it / the existing store helper.

**Bug-fix discipline.** A gate that flushes a real bug is fixed in place under
the project's own rules — fix forward, no workarounds, verify the fix. If a fix
changes a concept's Definition / Boundaries / Invariants, that mutation is
captured at `execute-plan` time (it cannot be predicted now); see *Design
changes* below.

---

## The ten acceptance gates

Each gate states: the claim and where it's claimed; the real entry point; the
value-delivering component (and whether it is *orchestration* — a stub executor
is acceptable — or a *real component doing real work*); the **acceptance
scenario** (real input → real observable effect at real surface); and why
today's coverage is nominal.

Gates are grouped by harness for the planner's sequencing. The grouping does not
split the spec. **Gate numbers are the `/coverage` report's gap IDs, not a
sequential 1–10:** report gap #9 (the claim-producer protocol's real
backing-state work) is closed *within* Gate 10, so there are nine gate headers
for ten gaps — there is intentionally no "Gate 9" header.

### Group A — Services integration harness (real images)

#### Gate 1 — A real sensor observes real external state and fires a real downstream node

- **Claim** — "Watching external state and reacting": a sensor observes an
  external system and the cascade fires the subscribing downstream node. README §2
  (headline pattern #1); `concept:cascade` ("the killer primitive … reactivity to
  external change is the same machinery"); `concept:sensor`.
- **Real entry point** — a real `rimsky-sensor-http:latest` (or
  `rimsky-sensor-cron:latest`) image running as a peer of a real rimsky stack,
  emitting via `route:POST /instances/{id}/messages` with `sender_kind: publisher`.
- **Value-delivering component** — the real bundled sensor process observing real
  external state. **Real component**, not orchestration — a stubbed sensor does
  not satisfy this gate.
- **Acceptance scenario** — Bring up `rimsky-all-in-one:latest` via
  `harness.BringUpRimsky`, start a real `rimsky-sensor-http` image on the network
  (new `harness.StartSensorHTTP`) pointed at a controllable HTTP endpoint, and
  deploy a template whose node subscribes to the sensor's message kind. **Change
  the watched endpoint's body** → a real `message` row persists with
  `sender_kind: publisher`, and the subscribing downstream node transitions to
  `stale` then re-runs to `fresh` through the cascade. (Cron variant: configure a
  due window and assert the same on tick.)
- **Why today is nominal** — every sensor test is client-and-server-in-one:
  `lib/services/sensors/sensor-http/sensor_test.go::TestTick_PollsAndPushesOnChange`
  and `lib/services/sensors/sensor-cron/sensor_test.go::TestTick_FiresDueSubscriptionAndAdvances`
  call `Tick()` in-process against a pinned clock and POST to a fake rimsky; the
  smoke test `lib/services/test/smoke/data_platform_smoke_test.go::TestDataPlatformSmoke_SensorHTTP`
  never starts the sensor binary. `lib/services/test/scenarios/` has no sensor
  scenario.

#### Gate 8 — A real publisher delivers into a real rimsky end-to-end

- **Claim** — a publisher peer emits messages into rimsky through the universal
  message-emit path. README §2/§4; `concept:publisher`. This is the same
  end-to-end path as Gate 1 viewed from the emit side.
- **Real entry point** — a real bundled publisher (a sensor *is* a publisher)
  emitting to `route:POST /instances/{id}/messages`.
- **Value-delivering component** — a real publisher process exercising the emit
  path + capability check. **Real component.**
- **Acceptance scenario** — As Gate 1's bring-up: assert the persisted `message`
  row carries `sender_kind: publisher` and a `sender` derived from the
  publisher's identity (not the request body), against a real
  `table:rimsky_publisher_subscriptions` binding. Satisfied by the same harness
  run as Gate 1 — one test may close both; keep them as distinct asserted facts
  (downstream cascade for #1, derived-sender + persisted message for #8).
- **Why today is nominal** — `cmd/rimsky/conformance_publisher_test.go::TestPublisherConformance_FixtureCron`
  drives a *fixture* publisher into a *fake* receiver, not a real rimsky.

#### Gate 10 — A real producer's stage-then-swap is the value-delivering component in a held subgraph

- **Claim** — held subgraphs commit-or-abandon atomically: on aggregate success
  the producer atomically swaps staged data into the canonical view; on any
  failure it drops the staging. README §2 ("rimsky's atomic-staging primitive
  makes the all-or-nothing the default"); `concept:atomic-staging`.
- **Real entry point** — a held subgraph (`holds:` co-holders + subgraph-lifetime
  claim) on a real `rimsky-store-filesystem:latest` pick-policy claim, driven
  through a real rimsky stack to auto-terminal.
- **Value-delivering component** — the real filesystem store's Commit performing a
  real `os.Rename` swap (`lib/services/stores/filesystem/store/store.go::Commit`,
  pick-policy `OnCommit` action). **Real component.** The swap *exists and is
  shipped* — this gate is missing coverage, not a missing feature.
- **Acceptance scenario** — Bring up rimsky with a real filesystem store
  (`harness.StartFilesystemStore`) configured with a pick-policy claim. Drive a
  held subgraph to **aggregate-success** → the producer's real Commit moves the
  staged folder to its committed on-disk location (assert the on-disk swap via the
  store's filesystem state). Drive a second instance to an **injected
  aggregate-failure** → the real Abandon drops the staging (assert it is gone).
- **Why today is nominal** — the closest test,
  `lib/services/test/scenarios/atomic_staging/pg_verifier_commit_abandon_test.go`,
  boots the fused **postgres** store's gRPC server in-process
  (`stores/postgres/server.Run`) against a real Postgres testcontainer and drives
  Open / Commit / Abandon over a **direct gRPC client** — no `harness.BringUpRimsky`,
  no `holds:`, no cascade, no rimsky stack. Its `Commit`/`Abandon` are deliberate
  no-ops for scope-bytes claims (per `concept:atomic-staging` Notes), so it pins
  only that the terminal verbs return cleanly over the wire — never a real swap,
  and never reached through rimsky's held-subgraph auto-terminal. The real
  filesystem swap is wire-tested in isolation
  (`lib/services/test/scenarios/stores/fs_pick_policy_basic_test.go::TestFsPickPolicy_BasicRingCycle`,
  a pure claim-producer wire exerciser) but likewise never reached through
  rimsky's cascade. No test puts a real producer swap and rimsky's held-subgraph
  dispatch together.

> Note: this gate also closes report gap #9 (the claim-producer protocol's only
> asserted "real work" today is the postgres store's error vocabulary). Driving
> the real filesystem producer's swap through rimsky exercises real backing-state
> work end-to-end.

### Group B — Control-api (real chi router + Postgres)

#### Gate 2 — The MCP skin's `tools/call` produces the same result as the HTTP verb

- **Claim** — every operator action on the control-api is also an MCP tool at
  `route:POST /mcp`, "so an LLM-driven supervisor can drive the platform on the
  same verbs a human would." README §1/§4; `concept:control-api`,
  `concept:permission`.
- **Real entry point** — `route:POST /mcp` `tools/call`, through the real MCP
  catalog into the real control-api handlers.
- **Value-delivering component** — the real MCP catalog→handler invoke path.
  **Orchestration** (the skin must reach the same handlers as the HTTP route).
- **Acceptance scenario** — Against the real control-api (`newAuthFixture`-style:
  real router + real auth middleware over SQLite-backed persistence), perform an
  operation over HTTP (e.g. `route:POST /instances`) and the equivalent over
  `route:POST /mcp`
  (`tools/call` with the corresponding tool); assert both create the instance and
  return the same result envelope, and that the MCP path writes an audit row to
  `table:rimsky_events` tagged with the MCP skin.
- **Why today is nominal** — `tools/list` filtering and the `mcp:read` gate are
  really covered (`test/scenarios/auth/lifecycle_test.go::TestMCPSkin_RequiresMCPReadGate`),
  but every `tools/call` test uses a `fakeCatalog` in `lib/control/controlapi/mcp/`
  whose `Invoke` records the name and returns a placeholder; parity with the HTTP
  verb is asserted nowhere.

#### Gate 4 — The operator dashboard's auth gate and populated-state reads

- **Claim** — the operator dashboard read endpoints (observability summary, event
  feed, frames, per-instance node state, dispatches), gated behind
  `observability:read`. `concept:cascade-graph`; README §4.
- **Real entry point** — `route:GET /v1/observability/system/summary` (and the
  frames / event-feed / per-instance endpoints) through the real `controlapi.NewApp`.
- **Value-delivering component** — the gated dashboard read over real runtime
  state. **Orchestration** (the gate + the projection).
- **Acceptance scenario** — Drive the dashboard endpoints through the real app:
  (a) with no key → 401/403 from the real `observability:read` gate; (b) after
  seeding a real instance + node-runs + dispatches → the summary counts reflect
  the seeded state.
- **Why today is nominal** — `lib/control/observability/handler_test.go::TestHandler_SystemSummary_DispatchCounts`
  and `::TestHandler_ListFrames_Empty` mount a bare router with **no auth
  middleware** and assert response shape against an empty DB; the production gate
  is never exercised on this surface and no populated read is asserted.

#### Gate 6 — Role-template expansion → minted, usable, enforced key

- **Claim** — a CLI-bundled role JSON expands into a permission grant at
  key-creation time and the minted key enforces exactly that grant.
  `concept:role-template`; README §4.
- **Real entry point** — `route:POST /auth/keys` with a bundled role, then a real
  gated route exercising the minted key.
- **Value-delivering component** — role expansion → real key mint → real gate
  enforcement. **Orchestration** with a pure-function expansion front.
- **Acceptance scenario** — Mint a key over `route:POST /auth/keys` with each
  bundled role's expanded grant; assert each role's representative action returns
  200 and a non-role action returns 403 through the real gate.
- **Why today is nominal** — the proving tests are pure-function:
  `cmd/rimsky/cli/roles/audit_read_coverage_test.go::TestRolesCoverAuditRead`
  calls `auth.CheckGrant` in-process and
  `cmd/rimsky/cli/auth_common_test.go::TestApplyGrantPatches_AddRemove` tests the
  CLI helper in memory. (`lifecycle_test.go::TestMCPSkin_OperatorRoleKeyWorks`
  covers one operator-shaped grant incidentally; the gap is the systematic
  per-role mint-and-enforce.)

### Group C — Scenario harness (real supervisor/scheduler + Postgres)

#### Gate 3 — The post-commit limb of the verify-before-run guard

- **Claim** — verify-before-run: after an acquisition commits, the supervisor
  re-reads ownership before dispatching, so a claim stolen between commit and
  dispatch produces `orphaned_claim_lost_race` and **no** Execute. A named
  load-bearing safety property — README §1/§3; `concept:supervisor`,
  `@blessed-invariant 5`.
- **Real entry point** — `runtime.RunNode` (the per-candidate cycle the real
  supervisor loop invokes) under concurrent contention, against real Postgres.
- **Value-delivering component** — the supervisor's post-commit verify-read guard.
  **Orchestration.**
- **Acceptance scenario** — Two concurrent `RunNode` callers race the same
  dispatch row against real Postgres; the loser commits its acquisition tx, then a
  forced ownership flip *between commit and the verify-read* must emit an
  `orphaned_claim_lost_race` event (in `table:rimsky_events`) and make **no**
  Execute call at the real stub executor.
- **Why today is nominal** — `test/scenarios/verify_before_run_race_test.go::TestVerifyBeforeRunRace`
  exercises only the candidate-SELECT skip (`claimed_by IS NULL`); the test's own
  comment concedes the post-commit limb is unit-only (`verifyBeforeRun` is
  unexported). The named race has no integration coverage.

#### Gate 7 — Sub-graph recursion rejection through the real registration route

- **Claim** — registering a recursive (delegate-cycle) sub-graph template is
  rejected with `subgraph_recursion_unsupported`. `concept:sub-graph`.
- **Real entry point** — `route:POST /templates`.
- **Value-delivering component** — registration-time rejection through the real
  route. **Orchestration.**
- **Acceptance scenario** — POST a delegate-cycle template to `route:POST /templates`;
  assert HTTP 400 carrying `subgraph_recursion_unsupported`.
- **Why today is nominal** — `lib/graph/node/template_validator_graphs_test.go::TestCanonicalizeGraphs_RejectDelegateCycle`
  calls `ValidateTemplate` on an in-memory struct; the happy path is route-tested
  (`test/scenarios/subgraph_exit_carry_e2e_test.go::TestSubgraphExitCarryE2E`) but
  the rejection path never goes through the real registration route. (Small,
  well-understood — a candidate for going straight to a plan task.)

### Group D — Observability handshake

#### Gate 5 — The capability handshake's real probe→cache over the wire

- **Claim** — at startup rimsky probes each service's observability gRPC protocol
  and caches its advertised capabilities (declared events,
  `expected_attributes_schema`); the cache feeds registration + dispatch
  validators. `concept:observability`, `concept:discovery-cache`. One gate closes
  both.
- **Real entry point** — `RunHandshake` with the real `NewGRPCProber` against a
  real peer (`lib/control/observability/handshake.go`).
- **Value-delivering component** — the real `gRPCProber` dialing a real peer and
  filling the discovery cache. **Orchestration.**
- **Acceptance scenario** — Run `RunHandshake` with `NewGRPCProber()` against a
  loopback stub executor that advertises capabilities (declared events +
  `expected_attributes_schema`); assert `disc.GetExecutor(name)` returns Reachable
  with those capabilities cached. Kill the stub, let `RefreshLoop` run, assert it
  flips to unreachable.
- **Why today is nominal** — `lib/control/observability/handshake_test.go::TestRefreshLoop_HealsUnreachable`
  and `::TestRunHandshake_UnreachableExecutor_NoError` use a `fakeProber`, and the
  schema-resolver test populates the cache via `disc.SetExecutor` directly; the
  real probe→cache fill (the cache's reason to exist) is never driven over the
  wire — only the refresh-heal policy is.

---

## Drift fix

### D2 — Regenerate `VERIFICATION.md` against the honest result

`VERIFICATION.md` (committed 2026-06-02) asserts "70 of 71 concepts behavioral …
PASS … 0 concepts shape-only or missing," leaning on several proving-test
citations that are shape-only, in-memory, fake-prober, or mis-attributed (the
`/coverage` report enumerates them). Once the ten gates above are green:

- Regenerate `VERIFICATION.md` so its concept→proving-test map cites, for each
  concept, the test that *best* proves it under the real-acceptance bar
  (re-pointing the weak citations the report listed), and
- correct the verdict to the honest state rather than "every feature / PASS."

This is self-eliminating: once the document tells the truth, a re-run of
`/coverage` will not re-propose it. If the gates are landed incrementally,
update `VERIFICATION.md` as each lands rather than in one final sweep — but the
document must not assert coverage a gate has not yet established.

---

## Non-goals

- **CLI-against-real-server.** The `rimsky` CLI is a declared thin pass-through;
  its entry points are exercised against fake/in-memory servers today. The report
  flagged this as a minor, unranked, optional gap; it is out of scope here.
- **`atomic-staging` SQL-substrate producer-side swap.** The postgres store's
  Commit/Abandon are no-ops for scope-bytes claims *by design* — `concept:atomic-staging`
  Notes already record the SQL-substrate producer-side lifecycle as unshipped.
  Building it is a separate feature, not part of recovering coverage. Gate 10
  asserts the swap on the substrate where it *is* shipped (filesystem).
- **No new test framework.** Gates reuse the three existing harnesses plus the one
  sensor-start helper noted above.

## Error handling

These gates assert error paths as first-class outcomes, not just happy paths:

- Gate 3 asserts an error-class outcome (`orphaned_claim_lost_race` + suppressed
  dispatch).
- Gate 4 asserts the gate's 401/403 denial.
- Gate 6 asserts the 403 non-grant denial.
- Gate 7 asserts the 400 rejection.
- Gate 10 asserts the abandon (drop-staging) failure-path branch.

A gate that cannot reach its asserted error outcome because the behavior is wrong
is a bug to fix forward, not a reason to weaken the assertion.

## Testing strategy

- The gates *are* the tests. Each lives next to its peers: Group A under
  `lib/services/test/scenarios/`; Group B under `lib/control/controlapi/` (or
  `test/scenarios/auth/` for the auth-fixture-style gates); Group C under
  `test/scenarios/`; Group D under `lib/control/observability/`.
- Docker is required for **Group A** (real images via testcontainers) and
  **Group C** (Postgres testcontainer). **Groups B and D** run on lightweight
  in-process fixtures (SQLite-backed control-api app; loopback gRPC stub) and need
  no Docker. Any gate with an external-resource dependency must **fail hard** when
  the resource is absent (`t.Fatal`), never `t.Skip` — a skip would re-create the
  exact "proves nothing where skipped" nominal-coverage this spec exists to
  eliminate.
- Group A gates require the bundled images built locally (`make core-images` +
  `make service-images`) before the harness runs, per the services-harness
  contract.
- Coupling proof: where a gate asserts an invariant (Gate 3's race, Gate 10's
  swap), prefer demonstrating that disabling the enforcement makes the gate fail
  (the named-lock test's mutation-proof pattern), so the gate cannot silently rot
  into a tautology.
- Race sensitivity: Gate 3 (and any gate touching the queue/supervisor/scheduler)
  runs under `-race` per `.claude/rules/rules.md`.
- Full-suite verification after each gate lands: `go build ./... && go test ./...
  && make lint`, plus the scenario/storage suites for the touched packages.

## Acceptance criteria (spec-level)

The spec is satisfied when:

1. Each of the ten gates exists as a test that drives the real entry point,
   exercises the value-delivering component for real (no stub standing in for the
   thing the gate exists to exercise), and asserts the real observable outcome —
   and passes.
2. Any bug a gate flushed is fixed forward and verified; no gate is weakened to
   pass.
3. `VERIFICATION.md` reflects the honest post-gate coverage state.
4. A re-run of `/coverage` enumerates clean for these ten items (they no longer
   appear as gaps).

## Design changes

**None up front.** This spec adds acceptance coverage; it does not change any
concept's Definition, Purpose, Boundaries, or Invariants, and it introduces no
new load-bearing noun. `concept:atomic-staging` is already accurate (Gate 10
covers a shipped behavior the doc correctly describes); no concept text changes.

**Contingency:** if a gate flushes a real bug whose fix alters a concept's
invariants or boundaries (e.g. the post-commit race in Gate 3 turns out to be
mis-specified), `execute-plan` captures that mutation as a concept edit at the
time it is made, per the design-docs change discipline — it cannot be enumerated
in advance because no such divergence is known now.
