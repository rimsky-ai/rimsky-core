# Crimefinder Plan Divergences

A record of where the implementation departs from what the plan literally
prescribed. Each entry: what the plan said, what landed, and the best read
of the reason. This is a record, not a critique — correctness review is
`/review-work`'s job.

Spec: `.ok-planner/specs/2026-05-19-crimefinder-design.md`
Plan: `.ok-planner/plans/2026-05-19-crimefinder.md`
Working tree: `apps/crimefinder/` (subtree introduced by this plan execution).

---

## 1. Scenario harness is in-process, not testcontainers-driven

- **What the plan said:** T48 prescribes a `harness.ts` that "spins up: a
  rimsky stack via testcontainers: postgres + rimsky-supervisor +
  rimsky-control-api + crimefinder-producer (built locally via the
  Dockerfile from T34). A crimefinder-executor process on a chosen host
  port…" Helpers include `registerTemplate`, `createInstance`,
  `waitForInstanceTerminal`. `test/package.json` declares `testcontainers`
  as a real dependency. T49–T57 all describe scenarios driven through
  rimsky instances (`createInstance`, stub-executor receives dispatches
  with `userdata.stub_outcome`).
- **What was implemented:** `apps/crimefinder/test/scenarios/harness.ts`
  starts only the producer's gRPC server in-process and exposes
  `setupHarness`, `gitCommit`, `encodePassStateAddress` plus
  `JsonlStore`/`SessionTokenRegistry`/`PartitionCache` handles. There is
  no rimsky-supervisor, no control-api, no instance lifecycle, no
  template registration, no `waitForInstanceTerminal`. Each scenario test
  imports producer gate handlers (`handleOpen`, `handleAppendFinding`,
  `handleCommitFix`, `handleAppendCoverage`, `handleSkipZone`, etc.)
  directly and calls them. `testcontainers` is declared in
  `apps/crimefinder/test/package.json` but is not imported anywhere in
  `test/scenarios/`. Only `test/e2e/smoke.test.ts` mentions
  testcontainers, and only in a comment describing what a full
  implementation "would" do — the test body is an `expect(GATED).toBe(true)`
  placeholder.
- **Inferred reason:** A pragmatic short-cut. Bringing up the rimsky
  stack via testcontainers needs a built producer image and a working
  Docker socket; in-process gate calls cover the producer's business
  invariants (JSONL durability, atomicity, class-5b routing, dedup,
  recovery scan, iteration counter, skip-zone) without that
  infrastructure cost. The cost is loss of coverage for the wire path
  (rimsky orchestration, claim lifecycle, supervisor callback handoff,
  executor → MCP → CLI subprocess flow). `apps/crimefinder/CLAUDE.md`
  documents this choice: "test/scenarios/ — vitest scenarios driving the
  producer surface via direct gate calls; runs in-process for speed."

---

## 2. `review_complete` does not enforce `coverage_below_threshold`

- **What the plan said:** T57 (Scenario test: coverage threshold) says:
  "Stub a review-zone session that reports only 1 of N files in coverage,
  with N large enough to fall below `cfg:coverage.threshold_pct`. Call
  `review_complete` WITHOUT `review_skip_zone`. Assert: gate returns
  `coverage_below_threshold` error." The spec (line 434) lists
  `review_complete`'s preconditions as "no findings still `status:fixing`
  for this session; coverage at threshold OR `review_skip_zone` invoked"
  and the spec's worked-test section (line 1275-1278) repeats: "Stub
  executor calls `review_complete` without `review_skip_zone` and with
  coverage below threshold; verify the gate returns
  `coverage_below_threshold` and the session remains open."
- **What was implemented:** `executor/src/gates/review-complete.ts:31-36`
  computes `coverage_pct: 0` as a literal and never checks against any
  threshold. The only precondition enforced is `unresolved_findings_in_flight`.
  The `coverage_below_threshold` error code is wired up in
  `shared/src/error-classes.ts` and is *raised* in
  `producer/src/state/skip-zone.ts:16-22`, but only when `skip_zone` is
  called outside a review-zone session (no `zoneId` bound on the token)
  — a different semantic from "coverage too low".
- **What the test does instead:**
  `test/scenarios/coverage-threshold.test.ts` exercises only the skip-zone
  path under a below-threshold config; it asserts the skip row lands and
  `coveragePercent < 80`. It never invokes `review_complete` and never
  asserts the gate returns `coverage_below_threshold` for the
  insufficient-coverage scenario.
- **Inferred reason:** Plan/spec asked for server-side enforcement at
  `review_complete`. The implementer wired up the error class and the
  config (`cfg:coverage.threshold_pct`, `on_below_threshold`) but stopped
  short of the conditional throw. The scenario test was reshaped to
  match what landed instead of what the spec asked for. This is the
  divergence the user flagged: "captured via the skip-zone path rather
  than server-side enforcement."

---

## 3. Internal MCP server uses `McpServer` SDK + raw `http.createServer`, not Fastify + JSON-RPC

- **What the plan said:** T37 step 4: "Write `internal-mcp-server.ts`.
  Fastify-based loopback HTTP+JSON-RPC server (port chosen at startup;
  advertised to Claude CLI via `--mcp-config`)." Internal flow described
  as: parse JSON-RPC envelope; validate `Authorization: Bearer <token>`;
  match tool; validate input via zod; call dispatch; return result or
  `GateErrorEnvelope` verbatim.
- **What was implemented:**
  `executor/src/internal-mcp-server.ts:1-9` uses
  `@modelcontextprotocol/sdk`'s `McpServer` +
  `StreamableHTTPServerTransport` mounted on a plain
  `node:http.createServer`. Tools are registered via `mcp.registerTool`
  (the newer overload, not `mcp.tool`). There is no Fastify, no
  hand-rolled JSON-RPC envelope parsing, and no `Authorization: Bearer`
  header check; token validation is per-tool by reading a `token` field
  from the tool input (`makeHandler` extracts `(parsed.data as
  {token: string}).token`).
- **Inferred reason:** Using the SDK's `McpServer` is the conformant way
  to speak the protocol (the StreamableHTTP transport handles framing,
  initialization, capability negotiation). The "Fastify-based JSON-RPC"
  framing in the plan predates the SDK overload that landed; the
  implementer migrated to the supported surface. The auth-via-input-field
  pattern is a knock-on: header-based bearer auth would require
  per-request middleware on the transport, which the SDK doesn't expose
  cleanly; passing the token through the tool input every call sidesteps
  that. The user flagged the `mcp.tool` → `mcp.registerTool` migration
  specifically.

---

## 4. Single consolidated `gates_test.ts`, not per-gate test files

- **What the plan said:** T38 enumerates per-gate test files:
  `executor/src/gates/review-context_test.ts`,
  `review-finding_test.ts`, `review-coverage_test.ts`, etc., "co-located
  `*_test.ts` for each."
- **What was implemented:** Only one test file in `executor/src/gates/`:
  `gates_test.ts`. It imports every gate (`reviewFinding`,
  `reviewCoverage`, `reviewRunTests`, `reviewCommitFix`, `reviewDefer`,
  `reviewSkipZone`, `reviewRequestHelp`, `reviewContext`,
  `reviewComplete`) into one suite.
- **Inferred reason:** Consolidation. The gates are thin (each ~10–30
  lines, mostly a `stateClient.<rpc>(input)` call + named-event emit),
  so collapsing tests into one file removes boilerplate. No real loss of
  coverage if cases are exhaustive, but cold-read by-feature
  organization is mildly weaker (T38 step 4 said "Tests per gate: mock
  `stateClient`, exercise the gate, assert the right call shape AND the
  right named-event emission").

---

## 5. `partition.ts` annotated `@diverged: true` where plan said `@diverged: false`

- **What the plan said:** T12 step 2: "Port the prototype with these
  changes: Use `@crimefinder/shared`'s `generateZoneId` rather than the
  prototype's local hash function (they're equivalent)." Annotation: "
  `@source: /Users/patrick/.../partition.ts` / `@diverged: false`."
- **What was implemented:** `producer/src/zones/partition.ts:1-6`:
  `@diverged: true` with `@reason: generateZoneId moved to
  @crimefinder/shared/ids; ignore-pattern list extended to be
  config-overridable.`
- **Inferred reason:** The lift is genuinely divergent — the prototype
  inlined a hash function for zone IDs; the port imports `generateZoneId`
  from `@crimefinder/shared` and accepts an external
  `ignorePatterns` override. The plan's `@diverged: false` was the
  optimistic call ("they're equivalent") and the implementer made the
  honest call: imports and signature changed, so mark it diverged. This
  is the same judgment the cold-read style guide wants.

---

## 6. Files / helpers not in the plan's file map

The plan's per-task "Files:" sections enumerated every file expected. The
implementation added several support files the plan didn't list:

- `executor/src/agent-types.ts` — pulled `AgentOutcome` types out of
  `agent-run.ts` into a sibling module. The user flagged this:
  "extra helpers or files the plan didn't mention." Plan T43 step 1
  defined `AgentOutcome` inline in `agent-run.ts`.
- `executor/src/gates/types.ts` — defines `GateContext`,
  `NamedEventEmitter`, and the `event()` helper shared across every
  gate. Plan T38 step 3 implied this exists ("The `NamedEventEmitter` is
  a function the executor's agent-run pipeline passes in") but didn't
  enumerate the file.
- `producer/src/scopes/types.ts` — defines `OpenContext`, `OpenResult`,
  the `PartitionCache` interface + `createPartitionCache` factory, and
  `parseSelectorQuery`. Plan T21 step 1 sketched `OpenContext` /
  `OpenResult` inline as illustrative TypeScript inside the task body
  but didn't enumerate a file for them.
- `producer/src/state/handler-deps.ts` — defines `StateHandlerDeps` (the
  bag of injected dependencies threaded into every CrimefinderState RPC)
  and `UnauthenticatedError`. Plan T28 step 1 referred to the same
  concept ("dependency injection of `JsonlStore`, `SessionTokenRegistry`,
  `Logger`, and (where relevant) `IterationCounter`, `TestCache`,
  `GitOps`") without enumerating the file.
- `producer/src/state/materialize.ts` — defines `materializeFindings`
  (the helper that walks finding + status_update rows and computes
  current status). Plan T28 step 4 described
  "read JSONL, materialize statuses" inline but didn't extract.
- `producer/src/state/test-helpers.ts` — `makeStateDeps(...)` fixture
  builder for tests.

Most of these are uncontroversial extractions a thoughtful reviewer
would have asked for anyway. `agent-types.ts` is the only one the user
flagged specifically.

---

## 7. `OpenContext` is fatter than the plan said

- **What the plan said:** T21 step 1 sketched `OpenContext` as:
  `{selector, claimId, repoRoot, store, tokens, iterCounter,
  stateEndpointUrl, logger}`.
- **What was implemented:** `producer/src/scopes/types.ts:10-22` adds
  `partitionCache: PartitionCache`, `config: CrimefinderConfig`, and
  `git: GitOps` to the type.
- **Inferred reason:** Genuinely needed by downstream scope handlers
  (partition cache for source-tree/fix-partition, config for
  partitioning options and design-doc paths, git for any handler that
  needs `git status`). The plan's sketch was illustrative.

---

## 8. `review_complete` returns `coverage_pct: 0` placeholder

- **What the plan said:** T38 step 2 for `review-complete`: "calls
  `stateClient.updateFindingStatus` is N/A here — review-complete just
  emits `zone_completed` named-event and returns `{findings_recorded,
  coverage_pct}` computed from queryFindings + appendCoverage state."
- **What was implemented:** `executor/src/gates/review-complete.ts:34-36`
  returns `coverage_pct: 0` as a literal, with no derivation. The
  `zone_completed` event payload also carries `coverage_pct: 0`.
- **Inferred reason:** Same root cause as divergence #2 — there is no
  server-side coverage computation in the gate path. The plan's spec
  text implied the gate would need to walk coverage rows for the zone
  and compute the percentage; that work was elided.

---

## 9. `Executor.Capabilities` handler in `server.ts` returns the wrong shape; `capabilities.ts`'s response is unused on the gRPC path

- **What the plan said:** T45 step 2: `capabilities.ts` returns
  `{userdata_schema, declared_events, http_bridge_url}` (matching
  `ExecutorObservability.Capabilities` as the plan understood it).
- **What was implemented:** `executor/src/capabilities.ts:11-17` builds
  exactly that shape — but the registered gRPC handler in
  `executor/src/server.ts:213-222` does NOT call `buildCapabilitiesResponse`.
  Instead it manually constructs `{supports_trace_get,
  supports_trace_stream, retention_after_terminal_seconds,
  http_bridge_url}`. That second shape is what the underlying
  `ExecutorObservability` proto actually defines (the proto's Capabilities
  message is about trace surface, not userdata schema; userdata schema
  lives elsewhere in the protocol).
- **Inferred reason:** Plan T45 misidentified what
  `ExecutorObservability.Capabilities` is for. The implementer kept
  `buildCapabilitiesResponse()` available (probably for an HTTP-bridge
  surface that could expose userdata schema separately) but wired the
  gRPC handler to the actual proto shape. The result: dead helper in
  `capabilities.ts`, but the server contract is correct.

---

## 10. `SkipZoneRow` schema added without plan instruction; `passes.jsonl` is the carrier

- **What the plan said:** T28 step 8 for `skip-zone`: "append to
  passes.jsonl (or a separate skip-record row that ties into pass
  summary). Validate session is in review-zone role." T6 step 1
  enumerated `pass_started`, `pass_finished`; T17 added `iter_marker`.
- **What was implemented:** `shared/src/jsonl-rows.ts:150-159` adds a
  fourth member `SkipZoneRowSchema` to the passes-row discriminated
  union with fields `{kind:"skip_zone", id, ts, pass_id, zone_id,
  session_id, reason}`. `JsonlStore` gets a corresponding
  `appendSkipZone` method.
- **Inferred reason:** Plan T28 step 8's "or" gave the implementer
  latitude; the structured row is the right call (queryable later, no
  ambiguity vs `pass_started`/`pass_finished`).

---

## 11. `skip-zone.ts` overloads `coverage_below_threshold` for "no zone bound"

- **What the plan said:** T28 step 8: "Validate session is in
  review-zone role." On failure, you'd expect an error like
  "wrong_session_role" or similar; the plan never said to reuse
  `coverage_below_threshold` for it.
- **What was implemented:** `producer/src/state/skip-zone.ts:15-22`
  throws `coverage_below_threshold` when `meta.zoneId` is absent. This
  is the only place in the implementation that emits that error class.
- **Inferred reason:** Probably an attempt to give the error class some
  reason to exist, given that the gate-level enforcement (divergence #2)
  was never wired up. Semantically it's a stretch: "no zone bound" is
  not "below coverage threshold." A future cleanup pass would either
  introduce the missing gate-level check or rename this error.

---

## 12. `executor/src/server.ts` does not start an MCP server on its own; `agent-run.ts` does it per dispatch

- **What the plan said:** Implied by T37 / T43: the executor hosts an
  internal MCP server that the spawned Claude CLI dials. The plan's text
  in T43 step 7 says "spawn Claude CLI via cli-runner, with `--mcp-config
  <mcpServer.mcpConfigPath>`."
- **What was implemented:** `executor/src/agent-run.ts:166-167` calls
  `startInternalMcpServer(...)` *per dispatch* (i.e. a fresh MCP server
  per agent run) and tears it down in the `finally`. Each run gets its
  own port, its own token, its own temp `--mcp-config` file. This is
  fine and is in fact the cleanest mapping, but the plan's wording
  ("hosting an internal HTTP+JSON-RPC MCP server on a loopback port",
  T37) read more like a long-lived server.
- **Inferred reason:** Per-run isolation is safer (token scope =
  run scope, no cross-talk). Plan was ambiguous; implementer made the
  call.

---

## 13. `ReviewFindingResult.crimefinder_error_class` overwrites tension-confirmation on auto-route

- **What the plan said:** T38 step 2 for `review-finding`: "If response
  indicates `auto_rerouted:true` → return success but include
  `crimefinder_error_class:"concept_citation_missing"` in a sibling
  field per spec. If response indicates tension-confirmation routing →
  also return success with `tension_already_cataloged`."
- **What was implemented:** `executor/src/gates/review-finding.ts:32-33`:
  ```ts
  if (r.auto_rerouted) out.crimefinder_error_class = "concept_citation_missing";
  if (r.tension_confirmation) out.crimefinder_error_class = "tension_already_cataloged";
  ```
  If both flags happen to be true on the same finding (the producer
  can in principle hit both branches), the second assignment wins and
  the `concept_citation_missing` signal is lost. The field is a single
  scalar, so this is structurally limited; the plan asked for both
  signals.
- **Inferred reason:** The producer's `append-finding.ts` actually
  short-circuits to `tension_confirmation` before even checking
  class-5b, so in practice the two flags are mutually exclusive given
  the current ordering. But the output shape doesn't capture that
  invariant.

---

## 14. Executor `main.ts` skips several env vars + the callback-host knob

- **What the plan said:** T45 step 4 enumerated env vars:
  `CRIMEFINDER_EXECUTOR_HOST`, `CRIMEFINDER_EXECUTOR_PORT_GRPC`,
  `CRIMEFINDER_EXECUTOR_SILENCE_MS`, `CRIMEFINDER_EXECUTOR_CALLBACK_HOST`,
  `CRIMEFINDER_EXECUTOR_STUB_MODE`, `ANTHROPIC_API_KEY`,
  `CLAUDE_CODE_OAUTH_TOKEN`, `LOG_LEVEL`.
- **What was implemented:** `executor/src/main.ts:4-12` reads all
  except `CRIMEFINDER_EXECUTOR_CALLBACK_HOST`. The callback URL comes
  from the supervisor's `ExecuteRequest.callback_url` (see
  `server.ts:182`), so the env var has no use site.
- **Inferred reason:** The supervisor-supplied `callback_url` is the
  source of truth for where the executor posts back; an env-var
  override would only matter if the executor needed to rewrite the
  callback host (e.g. inside a container where the supervisor's
  Docker-network hostname isn't reachable on the host). The plan's
  inclusion was defensive; the implementer pruned it.

---

## 15. Producer `server.ts` does not assemble the `ExecutorObservability` service

- **What the plan said:** T33 step 1 listed handlers to register:
  ClaimProducer (Capabilities, Open, Commit, Abandon, Release, SplitScope,
  ScopesConflict) and CrimefinderState (T28–T30 handlers).
- **What was implemented:** Plan and implementation match for the
  producer.  *(This is a "no divergence" check — included so the reader
  knows producer-side wire surface was audited.)*

---

## 16. `executor/src/cli-runner.ts` exec-spawns by passing prompts as CLI args

- **What the plan said:** T40 step 2: "Spawn `claude` binary with
  `--mcp-config <path>`, `--allowedTools <list>`, working directory,
  system prompt, user prompt."
- **What was implemented:** `executor/src/cli-runner.ts:47-59`:
  ```
  --mcp-config <path> --allowedTools <list> --system-prompt <SYS> -p <USR>
  ```
  Prompts are passed as positional CLI args, which for non-trivial
  prompts can hit OS argv length limits and quoting issues. The
  claude-agent source the file was lifted from (`@source:
  executors/claude-agent/src/cli-runner.ts`, `@diverged: true`) handles
  this differently in the original; this is a simplification.
- **Inferred reason:** Stripped-down lift. The
  `@diverged` reason notes "removed cwd_from_store and
  attribute-writeback paths" but doesn't mention the prompt-passing
  difference.

---

## 17. The `validate.mjs` template validator is present and matches plan; the prototype's prompt source is followed

- **What the plan said:** T47 prescribes
  `apps/crimefinder/templates/validate.mjs` with cross-graph
  type-uniqueness, tags presence, and sub-graph encapsulation checks.
- **What was implemented:** Matches the plan literally.

---

## 18. `apps/crimefinder/CLAUDE.md` adds workspace-layout section the plan didn't prescribe

- **What the plan said:** T59 step 2 sketched a CLAUDE.md template with
  "What this is", "Where to look first", "After Code Changes" sections.
- **What was implemented:** `apps/crimefinder/CLAUDE.md` adds a
  "Workspace layout" subsection (under "Where to look first") that
  enumerates `shared/`, `producer/`, `executor/`, `cli/`, `templates/`,
  `deploy/`, `test/`. This is the file that explicitly documents the
  in-process harness choice from divergence #1.
- **Inferred reason:** Better onboarding doc; surfaces the most
  important fact (in-process scenarios) where the next session will
  look.

---

## 19. Multi-zone-concurrency test asserts `findings.length <= expectedCount` (allowing dedup), not exact count

- **What the plan said:** T50: "Assert: every emitted finding lands in
  JSONL exactly once."
- **What was implemented:**
  `test/scenarios/multi-zone-concurrency.test.ts:107` asserts
  `findings.length <= expectedCount`, with a comment "Some findings may
  dedup if same fingerprint produced; ensure none were corrupted and
  that we have at most the expected count."
- **Inferred reason:** Three zones x 5 findings each, with each zone's
  five findings using `zone.files[0]` (the same file) and descriptions
  `${zone.id}-bug-${i}` — the fingerprints should all differ (different
  description), so in principle the assertion could be `===
  expectedCount`. The implementer wrote a defensive
  ≤ to handle the case where fingerprint-dedup fires unexpectedly. The
  test doesn't fail under the relaxed assertion, but it also wouldn't
  catch a dedup bug that ate findings the spec wanted preserved.

---

## 20. `executor/src/agent-run.ts` skips several plan-specified steps in stub-mode flow

- **What the plan said:** T43 step 2 enumerates: (1) decode addresses,
  (2) construct StateClient, (3) build dispatch, (4) start MCP server,
  (5) resolve prompts, (6) if stubMode call `runStubAgent` and skip
  Claude CLI, (7) else spawn Claude CLI, (8) build AgentOutcome.
- **What was implemented:** Stub-mode path
  (`executor/src/agent-run.ts:154-160`) **does not** start the internal
  MCP server, **does not** resolve prompts, and **does not** build a
  CLI runner. It calls `runStubAgent(...)` with the dispatch function
  directly and inherits a fixed `token: "stub"`. The MCP server,
  prompt-loader, and silence-watch live in the else branch (lines
  166-215).
- **Inferred reason:** Stub mode by definition doesn't dial the MCP
  server (no real CLI subprocess to call out to it), so the server
  wouldn't be exercised. Skipping the prompts is also defensible — the
  stub never reads them. The plan ordered the steps as if every step
  ran every dispatch; the implementer reshaped to only do what each
  branch needs.

---

## 21. Internal MCP server's `transport` is a *singleton* across requests

- **What the plan said:** T37 implied per-request handling.
- **What was implemented:**
  `executor/src/internal-mcp-server.ts:187-204`: the `transport` is
  lazily initialized in the first request's handler and reused for all
  subsequent requests. The `StreamableHTTPServerTransport` is intended
  to be session-aware (Streamable HTTP is bidirectional with session IDs),
  so a single transport handling a single session is correct for the
  "one Claude CLI per executor run" pattern — but this only works
  because the executor tears down the MCP server when the run ends
  (divergence #12).
- **Inferred reason:** Matches the per-run scoping established in
  divergence #12. Plan didn't specify; implementer chose what fits the
  per-run lifecycle.

---

## 22. Test files use compiled `dist/` imports against sibling workspaces

- **What the plan said:** T48 step 2's test/package.json lists
  `@crimefinder/shared` as a dependency but doesn't address whether
  tests import sources or built dist artifacts.
- **What was implemented:** Every scenario test imports
  `@crimefinder/producer/dist/...` and `@crimefinder/executor/dist/...`
  paths (e.g.
  `from "@crimefinder/producer/dist/state/append-finding.js"`). This
  requires producer and executor to be built (`npm run build`) before
  scenarios will pass. The harness pattern of using `dist/` paths
  applies to the consolidated `gates_test.ts` as well.
- **Inferred reason:** Cross-workspace imports for the test workspace
  needed an entry point; `dist/...` is the natural target since the
  source `.ts` files aren't directly importable from another workspace
  without a tsconfig project reference. Building first is the trade-off.

---

## 23. `cross-zone-finding` scenario file landed; mapping matches spec semantics

- **What the plan said:** T53: emit a finding citing a file in a
  different zone; assert `originating_zone_id` matches the dispatcher
  zone.
- **What was implemented:** Both producer
  (`append-finding.ts:151-156`) and test
  (`test/scenarios/cross-zone-finding.test.ts`) implement and exercise
  the `originating_zone_id` derivation. This is in-line with the plan.
  *(Audit-positive entry: no divergence.)*

---

## Summary

The biggest semantic divergence is the **in-process harness** + the
**missing `review_complete` coverage enforcement**, which together mean
the spec's "coverage threshold gate" promise lives only as an unused
error class and an unused config knob. The producer-side surface
(JSONL, atomicity, recovery, class-5b, dedup, iter-counter) was built to
plan; the executor-side gates are present but thin, and the wire surface
between rimsky-supervisor and the producer was not exercised end-to-end
by any landed test.

The remaining divergences are mostly clean factoring (added helper
files, extracted types, factory functions for test fixtures) and one
documented annotation correction (`partition.ts` marked `@diverged: true`
where plan optimistically said `@diverged: false`).

Plan-prescribed but missing or watered-down:
- testcontainers-driven scenario harness (replaced with in-process)
- `coverage_below_threshold` enforcement at `review_complete` (missing)
- Per-gate test files (consolidated to one `gates_test.ts`)
- Bearer-header auth on internal MCP (replaced with per-tool token field)
- `CRIMEFINDER_EXECUTOR_CALLBACK_HOST` env var (pruned)

Added but not in plan:
- `executor/src/agent-types.ts`
- `executor/src/gates/types.ts`
- `producer/src/scopes/types.ts`
- `producer/src/state/handler-deps.ts`
- `producer/src/state/materialize.ts`
- `producer/src/state/test-helpers.ts`
- `SkipZoneRow` schema member
- "Workspace layout" subsection in `apps/crimefinder/CLAUDE.md`
