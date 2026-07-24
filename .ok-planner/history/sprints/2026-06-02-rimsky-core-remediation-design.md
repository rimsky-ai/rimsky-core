# Rimsky Core Remediation — Design

## Status

- Spec, 2026-06-02.
- Not a feature design — a **remediation spec**. It captures a batch of confirmed defects (filed GitHub issues plus defects surfaced by a code+test audit) and the resolution decided for each, as the input to `write-plan`.
- Pre-v1; per `.claude/rules/rules.md`, no backwards-compat constraints on protocols, schema, or config shape. Where a clean fix means deleting a legacy path, delete it.

## Problem statement

Issues #1–#9 and #11 on `rimsky-ai/rimsky-core`, plus an audit of the 2026-05-15 data-platform-extensions plan against the current `dev` tree, surfaced a cluster of defects that share one shape: **a feature wired through some layers but not the layer that makes it work, sitting behind a test too shallow to catch it.**

Root cause: the 2026-05-15 build (17 dispatches + 9 fixer cycles) landed its "scenario" tests at the shape level — asserting proto/struct shapes or pure-function results, not driving the running system — while the per-pass gate was only `go test` exiting 0. A green build therefore reported half-wired features as done. The build notes themselves confess the end-to-end harness was deferred "to a follow-up dispatch" that never ran.

Impact: every defect sits on a **primary, documented path** — the paths consumers hit first — so the failures present all at once and every downstream project is blocked.

## Guiding principles

1. **Work as documented.** The fix target is the behavior the spec / proto / concept docs / CLAUDE.md describe. Where code and docs disagree, make the code match the documented intent (or, for deliberately-cut scope, make the code stop advertising what it doesn't do).
2. **Every runtime fix carries the end-to-end test that would have caught it.** Non-negotiable. The test must drive the real system (testcontainers Postgres + supervisor/runtime dispatch, or the real executor process), not assert a shape. A green build lied about all of these once; the e2e test is what stops it lying again.
3. **Pre-v1: collapse, don't preserve.** No compat shims for legacy paths being replaced.

## Resolved decisions

- **#2 (`holds:`/`inherits:`):** finish `holds:` in the held-subgraph layer, then **delete `inherits:` entirely** (no legacy alias).
- **#3 (entrypoint role command):** make `rimsky-entrypoint` honor a role argument (single-role when given one, all-three when not); fix the false docs. Not a "document the footgun" fix.
- **#7 (MCP):** implement the **connect-and-control** transport so the default `type: http` client works; **live push / subscriptions is OUT** (V2, undocumented, deliberately scoped out).
- **E4 (`candidate_handle`):** **wire it** — the spec/proto document the leaf receiving it.
- **J3 (object-store backends):** **reject loudly + document memory-only** — implementing s3/gcs/azure is new scope, deliberately cut.
- **#8 / PR #10:** implement #8 ourselves with a test; **do not merge** bot PR #10 (anonymous first-time contributor, no CI gating; left open, not merged).

## Fixes

Each entry: **symptom → root cause (evidence) → fix → verification (the e2e test)**. Line refs are against `dev`; the implementer pins exact sites.

### Filed issues

**#1 — `rimsky watch` cursor encoding mismatch**
- Symptom: `rimsky watch` 500s on first cursor advance — "events.list: bad cursor: illegal base64 data at input byte 0". CLI sends `cursor=346` (numeric seq); server expects an opaque base64 token.
- Root cause: cursor-encoding contract split between CLI and server. Server cursor handling in `lib/control/controlapi/events.go` (reads `cursor` query at ~:77, returns `next_cursor` at ~:106); the CLI watch loop passes the raw numeric seq instead of the returned token.
- Fix: single authority for cursor encoding — the CLI passes back the opaque `next_cursor` token the server returned (or `events.list` accepts both numeric seq + base64 token; prefer the former).
- Verification: e2e — an instance emitting >1 event batch; assert `rimsky watch` streams to terminal with no 500.

**#2 — `holds:` co-holdership half-wired; delete `inherits:`**
- Symptom: a `holds:`-only template never engages the held auto-terminal path; the documented co-held Commit/Abandon never fires.
- Root cause: the held-subgraph **detection** layer is `inherits:`-only — `lib/graph/node/inheritance.go::HoldingSubgraphsForTemplate` / `IsHeld` walk `n.Inherits` only; the acquirer's own row is gated on `IsHeld()` in `lib/runtime/runner_acquire_holders.go::insertHeldClaimHoldersAtAcquire`; the premature-firing guard `lib/runtime/auto_terminal.go::expectedInheritorsMissing` consults the inherits-only subgraph. The co-holder-row insertion and claim-resolution paths already understand `holds:`; the detection layer never migrated.
- Fix:
  1. Teach the held-subgraph layer to build members from `holds:` — `HoldingSubgraphsForTemplate`, `IsHeld`, `expectedInheritorsMissing`, and the acquirer acquire-time row.
  2. **Delete `inherits:`**: remove the inherits walk in `ValidateInheritance`, the inherits branches in `runner_acquire_holders.go` and `runner_locks.go::collectInheritedClaims`, the `Inherits` field on the node spec, and update the concept docs (`claim-co-holdership.md` aliases, `claim.md`, `claim-handle.md`, `atomic-staging.md`, `fan-out.md`).
- Verification: e2e against real Postgres — register a `holds:`-only template, dispatch acquirer + co-holder(s), assert the claim handle becomes held and auto-terminal fires Commit on all-success / Abandon on any-failure.

**#3 — entrypoint ignores role command**
- Symptom: `command: ["rimsky-scheduler"]` on the `rimsky` / `rimsky-all-in-one` images is silently ignored; the container runs all three role processes; multi-container deploys get supervisors advertising `127.0.0.1` and cross-container callbacks fail (~1-in-N dispatch failures).
- Root cause: `cmd/rimsky-entrypoint/main.go::main` hard-codes the three-role spawn list and never reads `os.Args`; both images set `ENTRYPOINT rimsky-entrypoint`. `CLAUDE.md` and `dockerfiles/Dockerfile.rimsky`'s comment falsely claim "role by container command."
- Fix: `rimsky-entrypoint` honors a role argument — run only the named role when given one, all three when not. Settle the migrate story for single-role containers (one-shot init, or one designated role runs migrate). Correct `CLAUDE.md` + the Dockerfile comment.
- Verification: integration test — run the image with `command: [rimsky-scheduler]`, assert only the scheduler role runs (no supervisor registered, no callback listener); assert the no-arg path still runs all three.

**#4 / #5 / #6 / #9 — documentation drift** (concept docs are durable source-of-truth, so these are in scope)
- #4: `.ok-planner/design/concepts/conformance.md` still describes standalone per-protocol binaries → update to the `rimsky conformance <protocol>` subcommand model (matches `cmd/rimsky/conformance.go` and `module-layout.md`).
- #5: `.ok-planner/design/concepts/module-layout.md` omits the AGPL-or-commercial dual-license track → add it (the per-directory Apache/AGPL map stays).
- #6: `.ok-planner/design/concepts.md` glossary — stale `sdk` entry (it was demoted to a test-support package, not a standalone module) + dangling `concepts/_retired/` pointer → fix both.
- #9: stale in-code comments / doc strings — verify each against current code and fix: `TRADEMARKS.md` (retired conformance binaries), `lib/foundation/persistence/postgres/claim_holders.go` (cascade-on-delete comment vs promote-not-delete), `lib/control/controlapi/admin_diagnostics.go` (`ErrInvalidateConflict` text), `lib/control/controlapi/actions.go` + `mcp_route.go` (`viewer` → `read-only`), `lib/runtime/auto_terminal.go` (stale function citation), `CLAUDE.md` (async-callback body key — `AsyncCallbackBody` oneof, legacy `{type:…}` rejected with 400).
- Verification: each claim re-checked against the cited code; doc updated to match.

**#7 — MCP connect-and-control** (live push OUT)
- Symptom: Claude Code `type: http` gets `405`; tool calls fail. `GET /mcp` 405s; the transport handshake is incomplete.
- Root cause: `lib/control/controlapi/mcp_route.go` registers only `POST /mcp`; `lib/control/controlapi/mcp/server.go::Server.ServeHTTP` is JSON-RPC-over-POST only — no GET stream, no session id, always `application/json`; `notifications/initialized` is unhandled and falls through to a `method not found` **reply to a notification** (a JSON-RPC violation).
- Fix: implement enough MCP Streamable HTTP transport for the default `type: http` client to connect — handle `GET` (benign/idle stream or correct response, not 405), `Mcp-Session-Id` session lifecycle, and `notifications/initialized` (consume, no response). **First task: a live reproduction** against a Claude Code `type: http` client to pin exactly what's required and size the transport work.
- Out of scope: server-push subscriptions / live event streaming (V2).
- Verification: a transport test driving the full handshake (`initialize` → `notifications/initialized` → `tools/list` → `tools/call`) over HTTP asserting success; ideally a live `type: http` connection check.

**#8 — `AggregationPolicy` yaml tags**
- Symptom: YAML template keys `cancel_siblings:` / `max_failures:` silently dropped.
- Root cause: `lib/foundation/spec/aggregation_policy.go::AggregationPolicy` declares `json:` tags only; `yaml.v3` (via `cmd/rimsky/cli/templates.go:85::readSpecFile`) falls back to the lowercased field name, so only `cancelsiblings:` / `maxfailures:` bind.
- Fix: add `yaml:` tags to all three fields (matching every sibling struct in the package). Implement ourselves; do **not** merge PR #10.
- Verification: a test unmarshalling a YAML template with `cancel_siblings: true` / `max_failures: N` (under a node's `fan_out.error_policy`) and asserting the fields bind.

**#11 — claude-agent internal-mcp ECONNRESET**
- Symptom: the claude-agent executor's per-run internal-MCP server drops the connection (ECONNRESET) in two paths — (1) post-completion, when the executor resumes the CLI to collect a missing `report_complete`; (2) mid-session, on long dispatches. Either way the node terminal-errors `agent/subprocess_exit/before_complete` despite the agent completing its work. Two failure modes, one root cause.
- Root cause: internal-MCP server lifetime relative to the CLI process in `executors/claude-agent/` — torn down at first `cli.exited` and/or unstable across long-running dispatches.
- Fix: stabilize the internal-MCP connection lifecycle — hold the connection across CLI resume (or bring up a fresh internal-MCP instance for the resumed CLI and route the resumed tool_use through it), and keep it alive across long dispatches.
- Verification: TS integration tests on both paths — resume path (CLI exits without `report_complete` → resume → `report_complete` succeeds) and long-dispatch path; `cd executors/claude-agent && npm test && npm run build`.

### Audit-surfaced runtime bugs (unfiled)

**D5 — `lifetime: durable` dropped at acquire**
- Root cause: the acquire path never threads `Lifetime` into `ClaimHandleInsertInput` — `lib/runtime/runner_acquire_claims.go::acquireClaim` (~:92–104) and the `ClaimSpec` build in `runner_locks.go` (~:133–140) carry no lifetime; sub-claims share the defect (`runner_acquire_helpers.go::AcquireSubClaimsInput` ~:108–129). The row always defaults to `subgraph`. The "e2e" durable test inserts a durable row directly, bypassing `acquireClaim`.
- Fix: thread `lifetime:` from the template store-ref through the acquire path (ClaimSpec → producer Open → `ClaimHandleInsertInput.Lifetime`) for both top-level and sub-claims.
- Verification: e2e — register a `lifetime: durable` template, acquire via the real path, assert the persisted handle is durable/`held_durable` and survives holding-subgraph terminal.

**E4 — `candidate_handle` never reaches the leaf** (wire it)
- Root cause: `lib/runtime/runner_dispatch.go::makeClaimHandle` / `makeHeldClaimHandle` never set `StoreHandle.CandidateHandle`, though it's persisted (`runner_subclaim.go`) and proto-defined.
- Fix: populate `ExecuteRequest` `StoreHandle.candidate_handle` from the persisted candidate in the dispatch builders.
- Verification: e2e/conformance — a DataProcessing fan-out dispatch asserts the leaf's `ExecuteRequest` carries the candidate_handle.

**E10 — retention sweeps are dead code**
- Root cause: `lib/runtime/retention_sweeps.go::SweepRunTreeRetention` / `SweepLineageRetention` are never invoked; the scheduler tick (`lib/graph/scheduler/scheduler.go`) wires every other sweep but not these two.
- Fix: wire both into the scheduler tick alongside the other retention sweeps, honoring the retention config.
- Verification: e2e — with retention config set, run the scheduler tick and assert old `rimsky_node_runs` / `rimsky_lineage` rows are reaped.

**E11 — per-reason park cap can never fire**
- Root cause: the config validator (`lib/control/config/stores.go:480`) accepts reason keys (`time_wait` / `callback_wait` / …) that don't match the stored ParkReason values (`await_callback` / `snooze`); `sweep_parked.go::sweepParkedByReason` → `ListParkedDiagnostic` does exact equality, so a configured cap matches zero rows.
- Fix: align the config reason vocabulary to the actual stored ParkReason values (the collapsed 2-value enum) so per-reason caps match.
- Verification: e2e — park a row with a reason, configure a per-reason cap, sweep, assert the cap trips.

**E14 — `{{child.partition_key}}` never bound**
- Root cause: the resolver `lib/graph/attribute/substitution.go::resolveChildValue` reads `ResolveContext.ChildPartitionKey`, but the dispatch context builder `runner_dispatch.go::buildResolveContextForDispatch` never sets it; the partition key is resolved separately and not threaded into substitution.
- Fix: set `ChildPartitionKey` in the fan-out leaf dispatch resolve context so `{{child.partition_key}}` resolves.
- Verification: e2e — a fan-out leaf node whose attributes use `{{child.partition_key}}` resolves to the actual partition key.

**F8 — publisher resync is dead code**
- Root cause: `lib/runtime/publishers.go::ResyncPublisherSubscriptions` is documented as invoked at supervisor startup but has zero call sites; the supervisor boot path (`lib/control/config/supervisor.go`) never calls it.
- Fix: invoke `ResyncPublisherSubscriptions` at supervisor startup so publisher subscriptions reconcile after restart (re-issue dropped subs, stop orphans).
- Verification: e2e — restart the supervisor; assert subscriptions for live instances are re-issued and orphan subscriptions stopped.

**J3 — object-store sensor advertises backends it can't service** (reject loudly)
- Root cause: `lib/services/sensors/sensor-object-store/` registers only the `memory` backend, but `Subscribe`/`Capabilities` accept and advertise `s3`/`gcs`/`azure`; `pollOne` logs `no_backend` at WARN and no-ops.
- Fix: stop advertising/accepting backends the binary can't service — reject `backend: s3|gcs|azure` at `Subscribe` with a clear error, drop them from `Capabilities`, and document memory-only + the `SetBackend` extension path.
- Verification: a test asserting `Subscribe{backend: s3}` is rejected with a clear error and `Capabilities` advertises only `memory`.

## Hardening / regression prevention

- **End-to-end test with every runtime fix** (above) — the deferred N-section coverage, made real for these paths.
- **Backfill untested-but-wired endpoints**: asset handlers (F5 — 4 of 6 untested) and lineage handlers (F6 — 5 of 8 untested, including the JSONB reverse-lookups that fail silently if subtly wrong).
- **Final whole-system feature-trace pass**: for each documented feature, trace it across layers and confirm a test drives it end to end — the audit lens, made a standing check rather than a one-off.
- **CI + branch protection** on the public repo: a PR gate running `go build ./... && go test ./... && make lint` and branch protection on `main`. The repo is public and bots are already opening PRs; nothing currently vets incoming code.

## Out of scope

- MCP server-push / subscriptions / live event streaming (V2 feature; #7 fixes connect-and-control only).
- Implementing s3/gcs/azure object-store backends (deliberately cut; operators register their own via `SetBackend`).
- Merging bot PR #10 (we implement #8 ourselves).
- Churning the redundant-but-harmless shallow tests whose behavior is genuinely covered elsewhere — leave them unless they sit on a fix path.

## Verification gate (overall)

- `go build ./... && go test ./... && make lint`
- Scenario/storage (Docker): `go test ./test/scenarios/... ./lib/foundation/persistence/... -count=1`
- Race-sensitive paths: `-race -count=3` on queue / supervisor / scheduler / persistence packages touched
- TS executor: `cd executors/claude-agent && npm install && npm test && npm run build`
- Conformance where a protocol or executor surface was touched: `go run ./cmd/rimsky conformance <protocol> --endpoint <…>`
