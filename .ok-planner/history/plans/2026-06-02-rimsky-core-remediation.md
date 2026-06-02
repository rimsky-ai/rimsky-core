# Rimsky Core Remediation Implementation Plan

**Spec:** .ok-planner/specs/2026-06-02-rimsky-core-remediation-design.md
**Goal:** Fix every filed issue and audit-surfaced runtime bug so rimsky works as documented, each runtime fix proven by a red-then-green end-to-end test that drives the real system.
**Architecture:** Go monorepo (`lib/graph` → `lib/runtime` → `lib/control`; `lib/foundation`, `lib/protocols`, `lib/services` modules) plus a TypeScript executor under `lib/services/executors/claude-agent/`. Tests drive real Postgres via testcontainers (`test/support/scenario` full-stack harness; `test/support/pgmigrate.OpenDriver` real-driver harness; `lib/services/test/harness` bundled-services harness). Proof-first: a behavior fix lands as a red task whose verification asserts the new test FAILS (`! <test>`), then a green task whose verification asserts it passes.
**Tech Stack:** Go (chi, pgx/v5, modernc sqlite, robfig/cron, slog), TypeScript (vitest), testcontainers-go, Docker.

**Docker is required** for every pass whose verification runs a `test/scenarios/...`, `lib/foundation/persistence/...`, or `lib/services/test/...` test (testcontainers spins real Postgres / built images). Passes note this in their header. Per `.claude/rules/rules.md` the tree is pre-v1 — break freely, no compat shims.

**Proof-first convention used throughout:** a red task's **Verification** is `! <exact test command>` — it exits 0 only when the named test FAILS against current code, so a shape test that is green-from-birth fails the gate. The paired green task's **Verification** runs the same named test and asserts it passes. `execute-plan` runs both as the gate.

---

## Pass 1: Fix `AggregationPolicy` YAML tags (#8)

**Goal:** YAML template keys `cancel_siblings` / `max_failures` bind through the CLI's `yaml.Unmarshal`.
**Scope:** Tasks 1–2
**End state:** working
**Verification:** `go test -run TestAggregationPolicyYAMLBinds ./lib/foundation/spec/...`

### Task 1 (RED): Add a test that the YAML keys bind

**Files:** `lib/foundation/spec/aggregation_policy_test.go` (new or extend)

**Steps:**
1. Add `TestAggregationPolicyYAMLBinds`: `yaml.Unmarshal` a fragment `kind: strict\ncancel_siblings: true\nmax_failures: 3` into `AggregationPolicy` (use `gopkg.in/yaml.v3`, the same library `cmd/rimsky/cli/templates.go:85::readSpecFile` uses).
2. Assert `CancelSiblings == true` and `MaxFailures == 3`.
3. Run it and confirm it FAILS today (yaml.v3 falls back to lowercased field names `cancelsiblings`/`maxfailures`, so the documented snake_case keys are dropped → both fields stay zero).

**Verification:** `! go test -run TestAggregationPolicyYAMLBinds ./lib/foundation/spec/...`

### Task 2 (GREEN): Add the yaml struct tags

**Files:** `lib/foundation/spec/aggregation_policy.go`

**Steps:**
1. In `AggregationPolicy`, add `yaml:` tags to all three fields matching the package convention: `Kind string \`yaml:"kind" json:"kind"\``; `CancelSiblings bool \`yaml:"cancel_siblings,omitempty" json:"cancel_siblings,omitempty"\``; `MaxFailures int \`yaml:"max_failures,omitempty" json:"max_failures,omitempty"\``.
2. Do NOT merge GitHub PR #10 — this is our own fix with the test above.

**Verification:** `go test -run TestAggregationPolicyYAMLBinds ./lib/foundation/spec/... && go build ./...`

---

## Pass 2: Fix `rimsky watch` cursor encoding (#1)

**Goal:** The CLI passes back the server's opaque base64 `next_cursor` token instead of fabricating a numeric one; `watch` streams across multiple event batches without a 500.
**Scope:** Tasks 3–4
**End state:** working
**Verification:** `go test -run TestEventsFollowOpaqueCursor ./cmd/rimsky/cli/...`

Background (grounded): the server encodes `next_cursor` as base64(JSON `{o,i}`) in `lib/foundation/persistence/{sqlite,postgres}/events.go::encodeEventCursor`; `decodeEventCursor` base64-decodes the incoming `cursor` and 500s on a non-base64 value (`events.list: bad cursor`). The CLI fabricates `cursor = fmt.Sprintf("%d", lastSeenID)` at `cmd/rimsky/cli/watch.go:69` and `cmd/rimsky/cli/instances.go:512`. The CLI test mock (`cmd/rimsky/cli/internal/clitest/server.go::handleListEvents` ~712, `state.go::EventsFor` ~500) currently mirrors the buggy numeric contract, which is why existing follow tests pass — that mock must be made honest.

### Task 3 (RED): Make the CLI test mock honest, then add a follow test that fails today

**Files:** `cmd/rimsky/cli/internal/clitest/server.go`, `cmd/rimsky/cli/internal/clitest/state.go`, `cmd/rimsky/cli/events_cursor_test.go` (new)

**Steps:**
1. Fix the mock to use the REAL cursor contract: `clitest` `handleListEvents` must emit `next_cursor` as an opaque base64 token (base64 of the last returned event's keyset, e.g. `{"o":<occurred>,"i":<id>}`) and decode the incoming `cursor` the same way — byte-for-byte the shape `lib/foundation/persistence/sqlite/events.go` uses. Reject a non-base64 `cursor` with 400/500 exactly as the real server does (so the test can't silently accept a numeric token).
2. Add `TestEventsFollowOpaqueCursor` driving `RunInstanceEvents` (or `RunWatch`) in `--follow` mode against the now-honest mock across **two full pages** of events; assert every event ID prints exactly once and no request errors.
3. Run it and confirm it FAILS today: the unfixed CLI sends `fmt.Sprintf("%d", lastSeenID)`, which the now-honest mock rejects as a bad cursor → the follow loop errors.

**Verification:** `! go test -run TestEventsFollowOpaqueCursor ./cmd/rimsky/cli/...`

### Task 4 (GREEN): CLI passes the opaque token through

**Files:** `cmd/rimsky/cli/watch.go`, `cmd/rimsky/cli/instances.go`

**Steps:**
1. In both `RunWatch` (~line 68-69) and `RunInstanceEvents` (~line 510-512): delete the `cursor = fmt.Sprintf("%d", lastSeenID)` line. Maintain a `nextCursor string` assigned ONLY from `page.NextCursor` (the opaque server token). On a partial page (`page.NextCursor == ""`) leave the cursor empty (re-scan newest page); the existing `e.ID <= lastSeenID` dedup guard (watch.go:80, instances.go:490) suppresses already-printed rows.
2. Keep `lastSeenID` purely as the local dedup high-watermark; it is never sent as a cursor.

**Verification:** `go test -run TestEventsFollowOpaqueCursor ./cmd/rimsky/cli/... && go test ./cmd/rimsky/cli/... -count=1`

---

## Pass 3: Entrypoint honors a role argument (#3)

**Goal:** `rimsky-entrypoint` runs only the named role when given one (`command: [rimsky-scheduler]`), and all three when given none; the false "role by container command" docs are corrected.
**Scope:** Tasks 5–7
**End state:** working
**Verification:** `go test -run TestEntrypointRoleSelection ./cmd/rimsky-entrypoint/...`

Background (grounded): `cmd/rimsky-entrypoint/main.go::main` hard-codes `children = ["rimsky-scheduler","rimsky-supervisor","rimsky-control-api"]` and never reads `os.Args`; both `dockerfiles/Dockerfile.rimsky` and `Dockerfile.all-in-one` set `ENTRYPOINT rimsky-entrypoint`. `cmd/rimsky-entrypoint/main_test.go` already overrides `binaryDir` with fixture binaries.

### Task 5 (RED): Test that a role argument selects a single role

**Files:** `cmd/rimsky-entrypoint/main_test.go`

**Steps:**
1. Add `TestEntrypointRoleSelection` using the existing fixture-binary pattern (`binaryDir` override): invoke the entrypoint's spawn logic with args `["rimsky-scheduler"]` and assert ONLY the scheduler fixture is spawned (e.g., the others' marker files are absent / only one child PID started). Add a sub-case: an unknown role arg (`["bogus"]`) exits non-zero with a clear error. Add a sub-case: no args spawns all three (today's behavior, must be preserved).
2. Refactor `main`'s spawn body into a testable function (e.g. `selectChildren(args []string) ([]string, error)`) if needed so the test can call it without spawning real processes; keep `main` a thin wrapper.
3. Run it and confirm the role-selection and unknown-role cases FAIL today (args are ignored; all three always spawn; unknown role never errors).

**Verification:** `! go test -run TestEntrypointRoleSelection ./cmd/rimsky-entrypoint/...`

### Task 6 (GREEN): Implement role selection + migrate story

**Files:** `cmd/rimsky-entrypoint/main.go`

**Steps:**
1. Read `os.Args[1:]`. With no args → spawn all three (unchanged). With one arg that names a known role (`rimsky-scheduler`/`rimsky-supervisor`/`rimsky-control-api`) → spawn only that role. With an unknown arg → exit non-zero with a clear error listing valid roles.
2. Migrate story: `runOnce("rimsky-migrate")` currently runs before spawning. For single-role containers, run migrate only when the selected role is `rimsky-control-api` (or gate behind an env like `RIMSKY_ENTRYPOINT_MIGRATE=1` / skip with `=0`); document the chosen rule in a code comment. The no-arg all-in-one path keeps running migrate as today. Keep it simple and explicit.

**Verification:** `go test -run TestEntrypointRoleSelection ./cmd/rimsky-entrypoint/... && go build ./...`

### Task 7 (DOC): Correct the role-by-command documentation

**Files:** `CLAUDE.md`, `dockerfiles/Dockerfile.rimsky`

**Steps:**
1. Update `CLAUDE.md`'s image description: the `rimsky` image runs all three roles by default and runs a single role when `command:` names one (now true); the all-in-one bakes zero-config SQLite and runs all three.
2. Fix the `Dockerfile.rimsky` header comment to describe the now-real role-by-command behavior accurately.

**Verification:** `grep -n 'role by container command' CLAUDE.md` returns no stale claim (or the line now describes the implemented behavior).

---

## Pass 4: MCP `/mcp` connect-and-control (#7)

**Goal:** The default Claude Code `type: http` client can connect: `notifications/initialized` is consumed without an erroneous reply, `GET /mcp` returns a valid stream instead of 405, and a session id is issued. Live push/subscriptions stays out of scope (V2).
**Scope:** Tasks 8–10
**End state:** working
**Verification:** `go test -run TestMCPStreamableHTTPHandshake ./lib/control/controlapi/...`

Background (grounded): `lib/control/controlapi/mcp_route.go:85` registers only `POST /mcp`; `lib/control/controlapi/mcp/server.go::Server.ServeHTTP` is JSON-RPC-over-POST only, always `application/json`, no session id, and routes the post-`initialize` `notifications/initialized` notification to the default branch which `writeRPCError(... CodeMethodNotFound ...)` — a reply to a notification (JSON-RPC violation).

### Task 8 (REPRO): Capture the exact handshake the client needs

**Files:** `.ok-planner/plans/2026-06-02-rimsky-core-remediation-notes.md` (implementer notes; create if absent)

**Steps:**
1. Stand up control-api locally (all-in-one or `go run ./cmd/rimsky-control-api` against a dev store) and point a Claude Code `.mcp.json` `type: http` entry at `/mcp`; capture exactly where the handshake fails (the GET probe 405, the session-id expectation, the notifications/initialized handling). Record the minimal transport surface required to connect.
2. This task is investigation feeding Tasks 9–10; record findings in the notes file. (Not a behavior change — no red gate; verification is that the notes file documents the required surface.)

**Verification:** `test -s .ok-planner/plans/2026-06-02-rimsky-core-remediation-notes.md`

### Task 9 (RED): Handshake test fails today

**Files:** `lib/control/controlapi/mcp/server_test.go`

**Steps:**
1. Add `TestMCPStreamableHTTPHandshake` driving the full sequence against the real `Server.ServeHTTP` (and the route, via the controlapi test harness if a `GET` handler is needed): `initialize` (assert a session id is returned, e.g. an `Mcp-Session-Id` header), then `notifications/initialized` (assert NO JSON-RPC error body is returned — a notification gets a 202/empty, never a `method not found` reply), then `tools/list` and `tools/call` succeed, and a `GET /mcp` returns a valid streamable response (200 / `text/event-stream`), not 405.
2. Run it and confirm it FAILS today (notifications/initialized gets an error reply; GET 405; no session id).

**Verification:** `! go test -run TestMCPStreamableHTTPHandshake ./lib/control/controlapi/...`

### Task 10 (GREEN): Implement the connect-and-control transport surface

**Files:** `lib/control/controlapi/mcp/server.go`, `lib/control/controlapi/mcp_route.go`

**Steps:**
1. Handle `notifications/initialized` (and any other `notifications/*` the client sends): consume it and return 202/empty with no JSON-RPC body (notifications get no response).
2. Issue and accept a session id (`Mcp-Session-Id`) on `initialize` per the repro findings.
3. Register a `GET /mcp` handler that returns a valid (possibly idle/keep-alive) `text/event-stream` so the client's stream probe succeeds, instead of falling through to 405. No domain push is required (V1 has no server-initiated notifications); the stream may stay open and idle.
4. Keep all existing tool/resource behavior unchanged.

**Verification:** `go test -run TestMCPStreamableHTTPHandshake ./lib/control/controlapi/... && go test ./lib/control/controlapi/... -count=1`

---

## Pass 5: `holds:` co-holdership — red proof (#2)

**Goal:** A failing end-to-end test that a `holds:`-only template engages the held auto-terminal path (Commit on all-success).
**Scope:** Task 11
**End state:** working
**Verification:** `! go test -run TestHoldsOnlyAutoTerminal ./test/scenarios/...` (Docker required)

Background (grounded): the co-holder row + claim-resolution paths understand `holds:`, but the held-subgraph detection layer is `inherits:`-only — `lib/graph/node/inheritance.go::HoldingSubgraphsForTemplate`/`IsHeld`, `lib/runtime/runner_acquire_holders.go::insertHeldClaimHoldersAtAcquire`, `lib/runtime/auto_terminal.go::expectedInheritorsMissing`.

### Task 11 (RED): holds-only auto-terminal test

**Files:** `test/scenarios/verifier/holds_only_auto_terminal_e2e_test.go` (new), package per the `test/scenarios` harness

**Steps:**
1. Using `scenario.Start` (full stack, testcontainers PG) deploy a template where an acquirer node opens a claim and a downstream node declares `holds: {<alias>: {from: <acquirer>}}` (NO `inherits:`). Drive both to terminal (stub executor success). Assert the acquirer's `rimsky_claim_handles` row reaches `committed` (auto-terminal fired Commit over the co-holder set) via `h.QueryRowSQL`/`InTx`.
2. Run it and confirm it FAILS today: the holds-only claim is never recognized as held (`IsHeld()` false), the acquirer row is never seeded at acquire, and auto-terminal never fires the documented Commit.

**Verification:** `! go test -run TestHoldsOnlyAutoTerminal ./test/scenarios/... -count=1`

---

## Pass 6: `holds:` co-holdership — wire the held-subgraph layer (#2)

**Goal:** Teach the held-detection layer about `holds:` so the Pass 5 test passes.
**Scope:** Task 12
**End state:** working
**Verification:** `go test -run TestHoldsOnlyAutoTerminal ./test/scenarios/... -count=1` (Docker required)

### Task 12 (GREEN): Honor `holds:` in subgraph detection

**Files:** `lib/graph/node/inheritance.go`, `lib/runtime/runner_acquire_holders.go`, `lib/runtime/auto_terminal.go`

**Steps:**
1. `HoldingSubgraphsForTemplate` / `IsHeld`: build subgraph members from each node's `Holds` block (acquirer + every `holds:`-declaring co-holder), in addition to `Inherits` (which Pass 7 then removes). A claim with ≥1 `holds:` co-holder is held.
2. `insertHeldClaimHoldersAtAcquire`: seed the acquirer's own holder row when the alias is held via `holds:` (its `IsHeld()` now returns true).
3. `expectedInheritorsMissing`: derive the expected member set from the holds-aware subgraph so the premature-firing guard waits for declared co-holders.
4. Keep the existing co-holder insertion / `collectCoHeldClaims` paths as-is.

**Verification:** `go test -run TestHoldsOnlyAutoTerminal ./test/scenarios/... -count=1 && go test ./lib/graph/node/... ./lib/runtime/... -count=1`

---

## Pass 7: Delete the legacy `inherits:` directive (#2)

**Goal:** Remove `inherits:` entirely now that `holds:` is fully wired (pre-v1, no compat, zero test coverage, design calls it superseded).
**Scope:** Tasks 13–14
**End state:** working
**Verification:** `go build ./... && go test ./lib/graph/node/... ./lib/runtime/... -count=1 && go test -run TestHoldsOnlyAutoTerminal ./test/scenarios/... -count=1`

### Task 13 (CODE): Remove `inherits:` from spec, validator, and runtime

**Files:** `lib/foundation/spec/template.go`, `lib/graph/node/inheritance.go`, `lib/runtime/runner_acquire_holders.go`, `lib/runtime/runner_locks.go`

**Steps:**
1. Remove the `Inherits []InheritEntry` field from the node spec (and `InheritEntry` if now unused).
2. `inheritance.go`: delete `ValidateInheritance`'s inherits walk and the inherits-member branch of `HoldingSubgraphsForTemplate`; keep the holds-based computation from Pass 6.
3. `runner_acquire_holders.go`: delete the `inherits:` branch of `insertCoHolderClaimHoldersAtAcquire`.
4. `runner_locks.go`: delete `collectInheritedClaims` and its call site; keep `collectCoHeldClaims`.
5. Remove now-dead helpers/tests that referenced `Inherits`.

**Verification:** `go build ./... && go test ./lib/graph/node/... ./lib/runtime/... -count=1`

### Task 14 (DOC): Update concept docs for the holds:/inherits: change

**Files:** `.ok-planner/design/concepts/claim-co-holdership.md`, `claim.md`, `claim-handle.md`, `atomic-staging.md`, `fan-out.md`

**Steps:**
1. Remove the `inherits` alias / "legacy singular directive" framing from `claim-co-holdership.md` aliases and body; `holds:` is the sole directive.
2. Scrub remaining `inherits:` mentions in `claim.md`, `claim-handle.md`, `atomic-staging.md`, `fan-out.md`. Append a dated Notes entry citing `spec:2026-06-02-rimsky-core-remediation`. Keep concept bodies path-free per the self-containment rule.

**Verification:** `! grep -rn 'inherits' .ok-planner/design/concepts/claim-co-holdership.md` (no remaining inherits framing; adjust grep per residual legitimate uses).

---

## Pass 8: Durable claim lifetime threaded at acquire (D5)

**Goal:** A `lifetime: durable` claim (and its fan-out sub-claims) persists with `lifetime='durable'` via the real acquire path.
**Scope:** Tasks 15–16
**End state:** working
**Verification:** `go test -run TestDurableLifetimePersistedOnAcquire ./test/scenarios/asset/... -count=1` (Docker required)

Background (grounded): `lib/runtime/runner_acquire_claims.go::acquireClaim` builds `ClaimHandleInsertInput` without `Lifetime`; `lib/runtime/runner_locks.go::buildLockSpecs` drops `sref.Lifetime`; `claimproducer.ClaimSpec` has no lifetime field; sub-claims drop it at `runner_acquire_helpers.go::acquireFanOutIfDeclared` (`AcquireSubClaimsInput` has no `Lifetime`). The persistence layer already honors `ClaimHandleInsertInput.Lifetime` (defaults empty→`subgraph`).

### Task 15 (RED): durable lifetime persistence test

**Files:** `test/scenarios/asset/durable_lifetime_acquire_e2e_test.go` (new), package `asset`

**Steps:**
1. Using `pgtest.OpenDriver`, drive the real acquire path for a node declaring a `durable` claim (producer advertising the DataProcessing mix-in per `lib/foundation/spec/template.go` canonicalizer requirement). Assert `ClaimHandles().Get(...)` returns `Lifetime == spec.ClaimLifetimeDurable`. Add a fan-out sub-case asserting child claim rows (`parent_claim_handle_id = <parent>`) are also `durable`.
2. Confirm it FAILS today (rows persist `subgraph`).

**Verification:** `! go test -run TestDurableLifetimePersistedOnAcquire ./test/scenarios/asset/... -count=1`

### Task 16 (GREEN): Thread `lifetime` through the acquire path

**Files:** `lib/protocols/claimproducer/types.go`, `lib/runtime/runner_locks.go`, `lib/runtime/runner_acquire_claims.go`, `lib/runtime/runner_acquire_helpers.go`

**Steps:**
1. Add a `Lifetime string` field to `ClaimSpec` (plain `string`, holding `"subgraph"`/`"durable"`). `lib/protocols` may **not** import `lib/foundation/spec` — `.golangci.yml` `protocols-purity` denies `lib/protocols/**` → `lib/foundation/**` — so do NOT type the field `spec.ClaimLifetime`; convert at the persistence boundary. The producer never sees it — do NOT add it to `OpenRequest`/the proto.
2. `buildLockSpecs`: set `Lifetime: sref.Lifetime` on the `ClaimSpec` literal (`NodeStoreRef.Lifetime` is already a plain `string`).
3. `acquireClaim`: set `Lifetime` on the `ClaimHandleInsertInput` literal. `ClaimHandleInsertInput.Lifetime` is typed `spec.ClaimLifetime`, so convert: `Lifetime: fspec.ClaimLifetime(<claimspec>.Lifetime)`. The local var `spec` in this file shadows the package name, so import `lib/foundation/spec` under an alias (e.g. `fspec`).
4. `acquireFanOutIfDeclared`: set `Lifetime: spec.ClaimLifetime(parentClaimSpec.Lifetime)` on the `AcquireSubClaimsInput` literal (its `Lifetime` field is typed `spec.ClaimLifetime`; `runner_subclaim.go` already threads `in.Lifetime` to the sub-claim insert).

**Verification:** `go test -run TestDurableLifetimePersistedOnAcquire ./test/scenarios/asset/... -count=1 && go test ./lib/runtime/... -count=1`

---

## Pass 9: `{{child.partition_key}}` bound at fan-out dispatch (E14)

**Goal:** A fan-out leaf node whose attributes use `{{child.partition_key}}` resolves to its own partition key.
**Scope:** Tasks 17–18
**End state:** working
**Verification:** `go test -run TestChildPartitionKeyBinds ./test/scenarios/... -count=1` (Docker required)

Background (grounded): `lib/graph/attribute/substitution.go::resolveChildValue` reads `ResolveContext.ChildPartitionKey`, but `lib/runtime/runner_dispatch.go::buildResolveContextForDispatch` never sets it; the partition key is available via `resolveAcqScope(ctx, args, acq).PartitionKey` (already called for overrides at ~line 463).

### Task 17 (RED): child.partition_key resolution test

**Files:** `test/scenarios/child_partition_key_e2e_test.go` (new), package `scenarios`

**Steps:**
1. Using `scenario.Start` with a remote stub-store fan-out (model: `fanout_success_cascade_e2e_test.go`), deploy a fan-out leaf whose attribute schema sources a field from `{{child.partition_key}}`, partitions `["a","b","c"]`. Capture each leaf's `ExecuteRequest` via `h.Stub.Observed()` and assert the resolved attribute equals that leaf's partition key.
2. Confirm it FAILS today (`ErrMissingSource` → dispatch resolution failure / empty).

**Verification:** `! go test -run TestChildPartitionKeyBinds ./test/scenarios/... -count=1`

### Task 18 (GREEN): Set ChildPartitionKey in the dispatch context

**Files:** `lib/runtime/runner_dispatch.go`

**Steps:**
1. Hoist the single `resolveAcqScope(ctx, args, acq)` call in `resolveAttributes` above the `buildResolveContextForDispatch` call and pass `scope.PartitionKey` in as a parameter (avoids a duplicate RunScope read).
2. Set `ChildPartitionKey: <partitionKey>` on the `ResolveContext` returned by `buildResolveContextForDispatch`. Empty string for non-fan-out runs is the correct "no binding" signal.

**Verification:** `go test -run TestChildPartitionKeyBinds ./test/scenarios/... -count=1 && go test ./lib/runtime/... -count=1`

---

## Pass 10: `candidate_handle` reaches the fan-out leaf (E4)

**Goal:** A DataProcessing fan-out leaf's `ExecuteRequest` carries its sub-claim's `candidate_handle`.
**Scope:** Tasks 19–21
**End state:** working
**Verification:** `go test -run TestLeafCarriesCandidateHandle ./test/scenarios/... -count=1` (Docker required)

Background (grounded, larger than the audit framed): `runner_dispatch.go::makeClaimHandle`/`buildExecuteRequest` never set `CandidateHandle`; AND the leaf child run does not even resolve its sub-claim — `acq.Locks` holds a fresh parent-selector claim, and nothing dereferences `FanOutChildRunPlan.SubClaimHandleID` at dispatch. So the persisted `producer_candidate_handle` must first be made reachable from the leaf.

### Task 19 (RED): leaf candidate_handle test

**Files:** `test/scenarios/leaf_candidate_handle_e2e_test.go` (new), package `scenarios`; `test/support/executors/stub/stub.go` (capture field)

**Steps:**
1. Extend `stub.ObservedRequest` to record `CandidateHandle` (the append site ~line 264) — required because the stub does not capture it today.
2. Using `scenario.Start` with the stub DataProcessing store (`test/support/stores/stub/dataprocessing/`, which mints a candidate per `BeginCandidate`) and a `fan_out:` over partitions `["a","b","c"]`, assert each leaf's observed `ExecuteRequest` carries the non-empty candidate handle the producer returned for that partition.
3. Confirm it FAILS today (every leaf's candidate handle is empty).

**Verification:** `! go test -run TestLeafCarriesCandidateHandle ./test/scenarios/... -count=1`

### Task 20 (GREEN part A): Link the sub-claim to its child run

**Files:** `lib/runtime/fanout_dispatch.go`, `lib/foundation/persistence/claim_handles.go` (+ sqlite/postgres impls)

**Steps:**
1. In `CreateFanOutChildren`, where both the child `runID` and `FanOutChildRunPlan.SubClaimHandleID` are in hand, set the sub-claim's `node_run_id` to the **child** run id (add/reuse a persistence setter, e.g. `ClaimHandles().UpdateNodeRunID(ctx, subClaimHandleID, childRunID, tx)`), inside the existing tx. This makes each sub-claim resolvable from the leaf by `node_run_id = its own DispatchID`.

**Verification:** `go build ./... && go test ./lib/foundation/persistence/... -run ClaimHandle -count=1` (Docker required)

### Task 21 (GREEN part B): Read the candidate handle at leaf dispatch and set it on the wire

**Files:** `lib/runtime/runner.go`, `lib/runtime/runner_dispatch.go`

**Steps:**
1. At leaf acquisition, look up the sub-claim row by `node_run_id = cand.DispatchID` and carry its `ProducerCandidateHandle` onto the leaf's `AcquiredLock` (add a `ProducerCandidateHandle []byte` field to `AcquiredLock`).
2. In `makeClaimHandle`, set `out.CandidateHandle = lk.ProducerCandidateHandle`. (`makeHeldClaimHandle` needs no change — co-held claims have no candidate.)

**Verification:** `go test -run TestLeafCarriesCandidateHandle ./test/scenarios/... -count=1 && go test ./lib/runtime/... -count=1`

---

## Pass 11: Retention sweeps wired + retention config plumbed (E10)

**Goal:** The scheduler tick reaps old `rimsky_lineage` and `rimsky_node_runs` rows per a parsed `retention:` config; the already-existing claim-handle/message-idempotency retention sweeps also become reachable (they gate on the same never-populated `Retention`).
**Scope:** Tasks 22–24
**End state:** working
**Verification:** `go test -run TestRetentionSweepsReapOnTick ./test/scenarios/... -count=1` (Docker required)

Background (grounded, two-layer gap): `SweepLineageRetention`/`SweepRunTreeRetention` (`lib/runtime/retention_sweeps.go`) are never called; AND `scheduler.Config.Retention` is never populated — there is no `retention:` YAML block parsed and `StartScheduler`'s `scheduler.Config{...}` literal never sets `Retention`, so even the wired sweeps are dead.

### Task 22 (RED): tick-reaps-old-rows test

**Files:** `test/scenarios/retention_sweep_e2e_test.go` (new), package `scenarios` (or a scheduler-tick driver test under `lib/graph/scheduler/` using `pgtest.OpenDriver`)

**Steps:**
1. Seed old `rimsky_lineage` rows (observed_at past the cutoff) and >N terminal frames per instance. Construct a `scheduler.Config` with `Retention{LineageTrailing: 1h, RecentFramesKept: 2}` and run one `scheduler.Tick`. Assert the stale lineage rows and all-but-the-2-most-recent terminal `rimsky_node_runs` rows are deleted.
2. Confirm it FAILS today (the tick never calls the two sweeps; rows survive).

**Verification:** `! go test -run TestRetentionSweepsReapOnTick ./test/scenarios/... -count=1`

### Task 23 (GREEN part A): Call the sweeps in the tick

**Files:** `lib/graph/scheduler/scheduler.go`

**Steps:**
1. After the message-idempotency retention block in `tick`, add the lineage sweep (`if cfg.Persist != nil && cfg.Retention.LineageTrailing > 0 { ... SweepLineageRetention(ctx, cfg.Persist.Lineage(), cfg.Retention, now, log) }`) and the run-tree sweep (`if cfg.Persist != nil && cfg.Retention.RecentFramesKept > 0 { ... SweepRunTreeRetention(ctx, cfg.Retention, cfg.Persist, now, log) }`), mirroring the existing sweeps' now/Clock + log-and-swallow shape.

**Verification:** `go build ./... && go test ./lib/graph/scheduler/... -count=1`

### Task 24 (GREEN part B): Parse and thread the `retention:` config

**Files:** `lib/control/config/stores.go`, `lib/control/config/scheduler.go`, `cmd/rimsky-scheduler/main.go`

**Steps:**
1. Add a `retention:` block to the config wrapper + `RimskyConfig` with keys `recent_frames_kept` (int), `lineage_trailing` (duration), `claim_handles_trailing` (duration), `message_idempotencies_trailing` (duration), matching `runtime.RetentionConfig`. Apply the documented defaults in the loader so retention is on by default.
2. Add `Retention runtime.RetentionConfig` to `SchedulerConfig`; set `Retention: cfg.Retention` on the `scheduler.Config{...}` literal in `StartScheduler`; thread `rimskyCfg.Retention` into the `config.SchedulerConfig{...}` literal in `cmd/rimsky-scheduler/main.go`.

**Verification:** `go test -run TestRetentionSweepsReapOnTick ./test/scenarios/... -count=1 && go test ./lib/control/config/... -count=1`

---

## Pass 12: Per-reason park cap vocabulary (E11)

**Goal:** A configured per-reason park cap keyed by a real stored ParkReason (`await_callback`/`snooze`) actually trips parked rows.
**Scope:** Tasks 25–26
**End state:** working
**Verification:** `go test -run TestMaxParkDurationAcceptsRealReasons ./lib/control/config/... -count=1`

Background (grounded): stored reasons are `await_callback`/`snooze` (`lib/foundation/spec/parked_reason.go`); the config validator `lib/control/config/stores.go::validateMaxParkDurationKeys` accepts `time_wait`/`callback_wait`/`retry_backoff`/`other`/… and **rejects the real reasons**, so a documented per-reason cap is rejected at config load and never reaches the sweep. **The bug is in the config validator; the runtime sweep (`SweepParkedNodes`/`sweepParkedByReason` → exact-equality `ListParkedDiagnostic`) already works when handed a real reason key.** So the proof must exercise the config layer — and `lib/runtime` may not import `lib/control/config` under `runtime-purity` anyway.

### Task 25 (RED): config accepts the real park-reason keys

**Files:** `lib/control/config/stores_test.go` (extend) or a new `lib/control/config/max_park_duration_test.go`

**Steps:**
1. Add `TestMaxParkDurationAcceptsRealReasons`: validate a config with `max_park_duration: {await_callback: 5s, snooze: 1h}` through the real validator (`validateMaxParkDurationKeys` / the `LoadRimskyConfigYAML` entry point). Assert it validates WITHOUT error. Add a sub-case asserting a now-invalid key (e.g. `callback_wait`) is REJECTED with a clear error.
2. Confirm it FAILS today: `await_callback`/`snooze` are rejected by the current validator (the documented operator config can't even load).

**Verification:** `! go test -run TestMaxParkDurationAcceptsRealReasons ./lib/control/config/... -count=1`

### Task 26 (GREEN): Align the config reason vocabulary

**Files:** `lib/control/config/stores.go`, `lib/runtime/sweep_parked.go`

**Steps:**
1. `validateMaxParkDurationKeys`: accept only `await_callback`/`snooze`; update the error string to list those.
2. Scrub the stale vocabulary in the surrounding doc comments (`RimskyConfig.MaxParkDuration`, `wrapper.MaxParkDuration`, `ParkedSweepArgs.PerReasonMaxPark`, `scheduler.Config`/`SupervisorConfig.MaxParkDuration` docs); fix example keys (`callback_wait: 7d` → `await_callback: 7d`, add `snooze: 1h`).

**Verification:** `go test -run TestMaxParkDurationAcceptsRealReasons ./lib/control/config/... -count=1 && go test ./lib/control/config/... -count=1`

---

## Pass 13: Publisher resync at control-api startup (F8)

**Goal:** On control-api startup, publisher subscriptions for live instances are reconciled — dropped ones re-issued, orphans stopped.
**Scope:** Tasks 27–28
**End state:** working
**Verification:** `go test -run TestPublisherResyncOnStartup ./lib/services/test/... -count=1` (Docker + built images required) OR `./lib/control/config/...` for the wiring-level test

Background (grounded): `lib/runtime/publishers.go::ResyncPublisherSubscriptions` has zero call sites; its doc says "supervisor startup" but the publisher registry lives in the **control-api** (`lib/control/config/controlapi.go:231` dials `publisherReg`; `StartSupervisor` never holds a publisher registry). So resync belongs in control-api startup and the doc is wrong.

### Task 27 (RED): startup-resync reconciliation test

**Files:** `lib/services/test/scenarios/publisher_resync/` (new) using `lib/services/test/harness.BringUpRimsky` with a fake publisher peer, OR a `lib/control/config` wiring test asserting `StartControlAPI` invokes resync against a fake registry

**Steps:**
1. Seed an active `rimsky_publisher_subscriptions` row; bring up control-api with a publisher fixture whose live subscription set has drifted (drop one rimsky-expected sub; add one orphan for an instance rimsky no longer tracks). After startup assert: the dropped sub is re-issued (`Subscribe` observed) and the orphan is torn down (`Unsubscribe` observed).
2. Confirm it FAILS today (no resync fires).

**Verification:** `! go test -run TestPublisherResyncOnStartup ./lib/services/test/... -count=1` (or the chosen package)

### Task 28 (GREEN): Call resync in StartControlAPI + fix the doc

**Files:** `lib/control/config/controlapi.go`, `lib/runtime/publishers.go`

**Steps:**
1. In `StartControlAPI`, after `publisherReg` is dialed (~line 231), call `runtime.ResyncPublisherSubscriptions(ctx, runtime.PublisherLifecycleDeps{Persist: persistStore, Publishers: publisherReg, Clock: cfg.Clock, Logger: cfg.Logger})` best-effort (log-and-continue on error, matching sweep discipline). Optionally run in a goroutine if a slow publisher would delay startup.
2. Fix the stale "invoked at supervisor startup" wording in `ResyncPublisherSubscriptions`' doc and the package doc to say "control-api startup."

**Verification:** `go test -run TestPublisherResyncOnStartup ./lib/services/test/... -count=1 && go build ./...`

---

## Pass 14: Object-store sensor rejects unserviceable backends (J3)

**Goal:** The bundled `sensor-object-store` rejects `backend: s3|gcs|azure` (only `memory` is registered) and advertises only registered backends; memory-only + the SetBackend extension is documented.
**Scope:** Tasks 29–30
**End state:** working
**Verification:** `cd lib/services && go test -run TestObjectStoreRejectsUnregisteredBackend ./sensors/sensor-object-store/...`

Background (grounded): `main.go:59` registers only `memory`; `sensor.go::Subscribe` accepts `s3|gcs|azure|memory`; `Capabilities` advertises all four; `pollOne` silently `no_backend`-WARNs and emits nothing.

### Task 29 (RED): reject + truthful-capabilities test

**Files:** `lib/services/sensors/sensor-object-store/sensor_test.go` (extend, in-process pattern)

**Steps:**
1. With the default wiring (memory-only), assert `Subscribe{resolved_config:{backend:"s3", bucket:"b"}}` returns a clear rejection error naming the unserviceable backend; assert `Capabilities()` advertises a `backend` enum of exactly `["memory"]`; assert `Subscribe{backend:"memory"}` still succeeds.
2. Confirm it FAILS today (s3 is accepted; Capabilities advertises all four).

**Verification:** `cd lib/services && ! go test -run TestObjectStoreRejectsUnregisteredBackend ./sensors/sensor-object-store/...`

### Task 30 (GREEN): Validate against registered listers + truthful Capabilities + docs

**Files:** `lib/services/sensors/sensor-object-store/sensor.go`, `lib/services/sensors/sensor-object-store/main.go`

**Steps:**
1. `Subscribe`: replace the hardcoded `s3|gcs|azure|memory` switch with a check against `s.listers` (reject any backend not registered, with an error naming the registered set via a `registeredBackends()` helper). This auto-accepts whatever a production build registers via `SetBackend`.
2. `Capabilities`: build the `backend` enum dynamically from `s.listers` keys so it advertises only registered backends.
3. Document memory-only + the `SetBackend` extension at `SetBackend`'s doc and `main.go` (the default image services only `memory`; production builds register cloud listers before `Run`).

**Verification:** `cd lib/services && go test -run TestObjectStoreRejectsUnregisteredBackend ./sensors/sensor-object-store/... && go build ./...`

---

## Pass 15: claude-agent internal-MCP connection stability (#11)

**Goal:** The per-dispatch internal-MCP server survives long SSE streams and CLI resume so `report_complete` lands instead of ECONNRESET → `agent/subprocess_exit/before_complete`.
**Scope:** Tasks 31–32
**End state:** working
**Verification:** `cd lib/services/executors/claude-agent && npx vitest run src/internal-mcp-server.test.ts`

Background (grounded): the per-dispatch `http.createServer` (`internal-mcp-server.ts`) sets no `requestTimeout`, so Node's 5-min default destroys long-lived MCP SSE GET streams (ECONNRESET); on resume the killed prior CLI's abrupt RST is not isolated. The server is correctly per-dispatch and stays up across resume.

### Task 31 (RED): timeout-discipline / resume test fails today

**Files:** `lib/services/executors/claude-agent/src/internal-mcp-server.test.ts` (extend the real-HTTP-server `describe` block); optionally `agent-run.test.ts` for the resume path

**Steps:**
1. PRIMARY (behavioral, not a config-shape check): open a real `StreamableHTTPClientTransport` SSE GET stream against the real per-dispatch `http.Server` (the existing `internal-mcp-server.test.ts` real-HTTP `describe` block already opens real clients), hold it open past a test-lowered request-timeout window, and assert NO `error`/ECONNRESET fires on the stream. This drives the actual server-side fault (Node's per-request timeout destroying long-lived MCP SSE streams) and asserts an observable outcome — the stream stays alive — not the value of `requestTimeout`.
2. (Resume path, also behavioral) extend the resume test so the fake CLI dials a real MCP client at the per-dispatch server URL, exits 0 without reporting, then on resume dials a second real client and calls `report_complete`; assert outcome `complete` (not `agent/subprocess_exit/before_complete`).
3. Confirm it FAILS today (the held SSE stream RSTs under Node's 5-min default; the resumed `report_complete` is ECONNRESET-prone). Name the test concretely (e.g. `survives long SSE stream`).

**Verification:** `cd lib/services/executors/claude-agent && ! npx vitest run src/internal-mcp-server.test.ts -t "survives long SSE stream"`

### Task 32 (GREEN): Apply HTTP timeout discipline + harden abrupt disconnects

**Files:** `lib/services/executors/claude-agent/src/internal-mcp-server.ts`

**Steps:**
1. After `http.createServer(...)`, set `httpServer.requestTimeout = 0` (disables the per-request cap — appropriate for indefinitely-long MCP SSE streams) and raise `keepAliveTimeout`/`headersTimeout` to comfortably exceed dispatch duration.
2. Harden the `clientError` handler so an abrupt RST from a killed prior CLI tolerates an already-destroyed socket (try/catch; never rethrow).
3. Keep the per-dispatch server alive across resume (already correct — `closeDispatchMcp` runs only in the outer `finally`); make no change there.

**Verification:** `cd lib/services/executors/claude-agent && npx vitest run src/internal-mcp-server.test.ts && npm run build`

---

## Pass 16: Documentation drift fixes (#4, #5, #6, #9)

**Goal:** Concept docs and stale in-code comments match v0.4.1+ reality. Behavior-preserving; normal verification.
**Scope:** Tasks 33–36
**End state:** working
**Verification:** `go build ./... && go vet ./...`

### Task 33 (#4): conformance concept doc

**Files:** `.ok-planner/design/concepts/conformance.md`

**Steps:** Rewrite "standalone per-protocol binaries" → the `rimsky conformance <protocol>` subcommand model (matches `cmd/rimsky/conformance.go` and `module-layout.md`). Update What it is / Purpose / Invariants / naming-history Notes. Path-free per self-containment rule.

**Verification:** `! grep -n 'rimsky-.*-conformance' .ok-planner/design/concepts/conformance.md` (no standalone-binary framing remains).

### Task 34 (#5): module-layout commercial-license track

**Files:** `.ok-planner/design/concepts/module-layout.md`

**Steps:** Note that the AGPL surface is dual-licensed AGPL-or-commercial (AGPL default; commercial lifts copyleft/§13), per `COPYRIGHT`/`COPYING.md`/`licensing.yml`. Keep the per-directory Apache/AGPL map.

**Verification:** `grep -n 'commercial' .ok-planner/design/concepts/module-layout.md` returns the new note.

### Task 35 (#6): concepts.md glossary `sdk` entry

**Files:** `.ok-planner/design/concepts.md`

**Steps:** Grounding correction (the spec's premise is partly stale): `.ok-planner/design/concepts/_retired/` **exists and is populated**, so the `See concepts/_retired/...` pointer is VALID — do NOT remove it. Read the current `sdk` glossary entry; correct only genuine staleness so it matches `module-layout.md`: the standalone SDK module was **dissolved into the protocols module** (the protocols module is the single public surface), NOT "carved into its own opt-in module"; separately, the Postgres testcontainer helper was demoted to a plain test-support package. Reconcile the wording to that reality; leave it if already accurate.

**Verification:** `grep -n '_retired' .ok-planner/design/concepts.md` still shows the valid pointer (it must NOT be removed), and the `sdk` entry matches module-layout's framing (dissolved-into-protocols; pg helper → test-support).

### Task 36 (#9): stale source comments / doc strings

**Files:** `TRADEMARKS.md`, `lib/foundation/persistence/postgres/claim_holders.go`, `lib/control/controlapi/admin_diagnostics.go`, `lib/runtime/auto_terminal.go`, `CLAUDE.md`

**Steps:** Verify each against current code, then fix the genuinely-stale ones: `TRADEMARKS.md` retired conformance binaries → subcommands; `claim_holders.go` cascade-on-delete comment → promote-not-delete; `admin_diagnostics.go` `ErrInvalidateConflict` text; `auto_terminal.go` stale function citations — BOTH the `insertHeldClaimHoldersAtAcquire` (~line 24) and `insertCoHolderClaimHoldersAtAcquire` (~line 26) citations name `runner_acquire.go`, but both functions live in `runner_acquire_holders.go`; fix both; `CLAUDE.md` async-callback body key (`AsyncCallbackBody` oneof; legacy `{type:…}` rejected with 400). Grounding correction: the `viewer` → `read-only` rename is ALREADY DONE (no `viewer` refs remain in `actions.go`/`mcp_route.go`) — skip it. Each comment must match the code it sits beside.

**Verification:** `go build ./... && ! grep -n 'runner_acquire\.go::' lib/runtime/auto_terminal.go` (no stale `runner_acquire.go::` citation remains — both are corrected; spot-check the other cited sites by reading them).

---

## Pass 17: Endpoint coverage backfill (F5 assets, F6 lineage)

**Goal:** Real-router+Postgres behavioral tests for the untested asset (4 of 6) and lineage (5 of 8, incl. JSONB reverse-lookups) handlers — characterization tests over working code so future regressions surface.
**Scope:** Tasks 37–38
**End state:** working
**Verification:** `go test -run 'TestAssetEndpoints|TestLineageEndpoints' ./lib/control/controlapi/... -count=1` (Docker required)

These handlers work today, so these are passing characterization tests (not red/green) — but they MUST drive the real chi router + real Postgres (the `app_test.go::newHarness` pattern) and assert observable results, not shapes.

### Task 37 (assets): asset endpoint tests

**Files:** `lib/control/controlapi/assets_test.go` (new)

**Steps:** Via the real-router+PG harness, exercise `handleListAssets`, `handleAssetVersions` (dials a stub DataProcessor's `ListVersions`), `handleAssetMaterializationHistory` (lineage join), and the materialize/delete 409-if-in-flight gate; assert real response bodies/status.

**Verification:** `go test -run TestAssetEndpoints ./lib/control/controlapi/... -count=1`

### Task 38 (lineage): lineage endpoint tests

**Files:** `lib/control/controlapi/lineage_test.go` (extend)

**Steps:** Cover `handleLineageRun`, `handleLineageClaim`, `handleLineageClaimAncestors` (sub-claim chain walk), and the JSONB-containment reverse lookups `handleLineageBySource`/`handleLineageByProducer`; seed rows and assert the reverse lookups return the right records (these fail silently if the GIN/JSONB query is subtly wrong).

**Verification:** `go test -run TestLineageEndpoints ./lib/control/controlapi/... -count=1`

---

## Pass 18: CI workflow (public-repo hardening)

**Goal:** A PR-gating CI workflow on the public repo so incoming PRs (bots are already opening them) are built/tested/linted automatically; and the pre-existing broken docs-lint workflow is removed.
**Scope:** Task 39
**End state:** working
**Verification:** `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml'))" && test ! -f .github/workflows/docs-lint.yml`

### Task 39: Add CI; remove the broken docs-lint workflow

**Files:** `.github/workflows/ci.yml` (new), `.github/workflows/docs-lint.yml` (remove)

**Steps:**
1. Remove `.github/workflows/docs-lint.yml` — it runs `make docs-lint`, a target that does NOT exist (the Makefile explicitly states this repo carries no docs gate), so it fails on every PR/push today. Per "Fix Every Bug You Find," delete it as part of standing up real CI.
2. Add a GitHub Actions workflow triggered on `pull_request` and `push` to `main` that runs `go build ./...`, `go test ./...` (the non-Docker subset, or a Docker-enabled runner for the testcontainers tests), and `make lint`. Pin the Go version to the repo's `go.mod`. Include the TS executor's `npm ci && npm test && npm run build` under `lib/services/executors/claude-agent/`.
3. Keep it minimal and correct.

**Verification:** `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml'))" && test ! -f .github/workflows/docs-lint.yml` (valid CI YAML; broken docs-lint workflow gone).

---

## Manual checks after completion

These require GitHub admin access / external state and cannot run inside the automated plan:

- **Branch protection on `main`** — enable required status checks (the Pass 18 CI workflow) and require PR review before merge, via the GitHub repo settings or API (needs admin auth). The public repo currently has no branch protection.
- **GitHub PR #10** — leave it open (not merged); #8 is fixed by Pass 1 with its own test. Close it with thanks at the maintainer's discretion.
- **MCP #7 live reproduction (Task 8)** — the implementer captures the required transport surface against a live Claude Code `type: http` client; if that environment isn't available to the implementer, the user runs the repro and feeds findings into Tasks 9–10.
- **`/execute-plan-via-workflow` run** — this plan is the trial input for the new workflow-backed executor; run it there (fresh session) rather than the prose `/execute-plan`.
