# All-in-one local orchestrator — inline execution working notes

Working notes for the inline (non-/execute-plan) implementation of
`specs/2026-07-02-all-in-one-local-orchestrator-design.md`. Temporary planning
input; remove after the work completes.

## Process

Plan passes for one step at a time. Implement each pass via the todo list,
verify, mark done. When a step's passes are all done and verified: commit, then
plan the next step's passes. Design-doc mutations ride with the step that
changes them, not a final pass.

## Step sequence (settled)

1. **Claude-agent Go port** — long pole; gates in-proc registration and TS retirement.
2. **In-proc claim-producer registry** (`lib/runtime/claimproducer/`) — runtime-layer, independent of the port.
3. **Bundled registration entrypoint** (`lib/services/bundled/RegisterAll`) + per-service `LoadOptsFromEnv()`.
4. **`rimsky run <template>` self-host** — endpoint discriminator, `--self-host`, late-bound `--service` direct spawn, `RIMSKY_PROCESS_ROLE` error-text update.
5. **Cross-mode proofs** — portability + zero-config scenario tests (need both modes live).

## Step-1 scope decision (settled)

Fold the per-node config redesign INTO the port — no parity port of the
`{ref}` catalog machinery (`mcp-catalog.ts` catalog resolution,
`RIMSKY_EXECUTOR_MCP_CATALOG` / `RIMSKY_EXECUTOR_MCP_ALLOW_INLINE` parsing)
only to delete it in a later pass. Everything else ports to parity; the two
redesigned knobs (`cli.mcp_servers` inline-only three-transport shape,
`cli.expose_env` + operator allowlists under `RIMSKY_CLAUDE_AGENT_*`) are
built straight to their final shape.

## Step-1 passes

- **Pass A — self-contained domain pieces.** Go package at
  `lib/services/executors/claude-agent/` (alongside TS `src/` until Pass F):
  embedded JSON Schema in final shape, error classification, rate-limit,
  token registry, ed25519 sign-off validation. Unit tests incl. signature
  wire-compat vectors.
- **Pass B — CLI runner.** Subprocess spawn, env composition (operator
  allowlist ∩ per-node `cli.expose_env`), stream-JSON parser. Unit tests
  against a fake binary.
- **Pass C — internal MCP server.** Stdlib net/http JSON-RPC endpoint + the
  attribute tools. Unit tests.
- **Pass D — dispatch core.** `agent-run` port wiring A–C; allowlist rejection
  errors name the disallowed entry, template, and node. Unit tests with fake
  claude.
- **Pass E — transport surfaces.** gRPC server from generated `lib/protocols`
  stubs, HTTP-JSON bridge to parity, observability handshake, package-scope
  `SchemaBytes()` / `DeclaredTags()` / `DeclaredErrorClasses()`, Go `main`.
  Verified via conformance runner against the built binary.
- **Pass F — retirement.** Delete TS tree + npm machinery; distroless
  Dockerfile; Go fake-cli; harness + scenario tests rewritten onto
  `RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST` / `RIMSKY_CLAUDE_AGENT_EXPOSE_ENV_ALLOWLIST`;
  `stories/claude-agent.md` mutation + related decision creates. Verified via
  `make service-images`, services scenarios, full build/lint.

## Step-2 passes

- **Pass A — handler contract + registry.** `lib/runtime/claimproducer/`:
  `InProcessRegistry` binding name → `Registration{Handler, Capabilities,
  Validation, DataProcessing}`, shaped like the executor registry (duplicate
  registration errors, RWMutex). Registration validates: non-empty
  write-semantics envelope, known values, mix-in advertisement ⇔ mix-in
  client consistency. Unit tests.
- **Pass B — in-proc dispatch path.** `Client` satisfying
  `locks.ClaimProducer`; mix-in views satisfying
  `clientiface.ValidationRegistry` / `clientiface.DataProcessingRegistry`.
  Envelope enforcement to peer parity (Open realized-write-semantics checks,
  SplitScope/ScopesConflict capability gates with byte-equal fallback).
  Handler errors wrap as `*peer.ProducerCallError` with error-class
  extraction. Unit tests with fake handlers.
- **Pass C — verification + design docs.** Full build/lint/test chain,
  `-race` on `lib/runtime/...`. Decision create
  `parallel-inproc-claim-producer-registry`; concept mutate
  `claim-producer.md` (in-proc shape + protocol-equivalence invariant).

## Step-3 passes

- **Pass A — bundled contract + handler restructures + LoadOptsFromEnv.**
  New `lib/services/bundled/` (protocols-only): `ExecutorHandler` interface
  mirroring the gRPC Execute shape (`Execute(ctx, *genv1.ExecuteRequest)
  (*genv1.Outcome, error)` — async-ack + HTTP callback survives in-proc
  unchanged; the in-proc client already passes outcomes through the same
  `Client` interface as the wire), sink interfaces for exec registry /
  cp registry / aliases / discovery in protocols-only types, and
  `RegisterAll(execReg, cpReg, aliases, discovery, opts)` per spec.
  Server-backed claim-producer adapter in `lib/protocols/serverkit`
  (wraps `genv1.ClaimProducerServer` as `claimproducer.ClaimProducer`;
  pure conversion, errors pass through — envelope policy stays in the
  step-2 runtime client, matching wire parity where error classes ride
  in-band, not on status errors). Restructure http-node / verifier-http /
  verifier-shape-checks from flat `package main` into importable handler
  packages + thin `cmd/main.go` (claude-agent's step-1 layout is the
  template; flat `package main` is not importable, so the spec's
  "importable handler package" invariant forces this). `LoadOptsFromEnv()`
  for the five services missing it. Unconfigured claim producer (required
  env absent) → skipped with a log line; present-but-invalid → boot abort
  naming the handler (zero-config boot stays possible; postgres can never
  have an inventable default DSN). Unit tests with fakes.
- **Pass B — capability advertisement.** Package-scope `SchemaBytes()` /
  `DeclaredTags()` / `DeclaredErrorClasses()` for the three executors
  missing them (data lifted from each service's observability surface);
  claim-producer capabilities as construction data; discovery population
  through the bundled-defined sink. Unit tests.
- **Pass C — root-side wiring.** All-in-one entrypoint calls
  `builtin.RegisterAll` then `bundled.RegisterAll` with root-side adapters
  onto `executor.InProcessRegistry`, `runtime/claimproducer.InProcessRegistry`,
  the executor alias/endpoint map, and `control/observability.Discovery`;
  root go.mod gains the services dependency (one-way edge); construction
  failure aborts boot naming the handler. Integration smoke: boot
  all-in-one, drive one node through an in-proc bundled handler.
- **Pass D — verification + design docs.** Full chain across modules.
  Decision creates: handler-package-in-service-directory,
  per-service-load-opts-from-env, bundled-registry-entrypoint,
  bundled-executor-inproc-capability-advertisement. Concept mutates:
  service, discovery-cache, module-layout. Story mutate
  single-process-all-in-one; decision mutate single-process-mode.

## Status

- [x] Step 1 (passes A–F) — Go port landed, TS retired, conformance 9/9,
      cross-stack + session-resume container scenarios green, design docs
      mutated (story rewrite, 11 decision creates, 1 decision mutate,
      module-layout licensing edit)
- [x] Step 2 (passes A–C) — in-proc claim-producer registry landed
      (registry + client + mix-in views, race-clean, lint green, decision
      create + concept mutate); riding fixes: latest-run ordering bug,
      nine stale frame fixtures, timeout-guard hardening. Known-red: six
      pre-existing frame-isolation proofs, dispositioned in the repair
      ledger (separate workstream).
- [x] Step 3 (passes A–D) — bundled registration entrypoint landed:
      `lib/services/bundled/RegisterAll` + per-service `LoadOptsFromEnv()`
      (five built, claude-agent's reshaped), three executors restructured
      into importable packages + `cmd/main.go`, server-backed claim-producer
      adapter + shared proto↔Go converters in serverkit (peer refactored
      onto them), capability accessors at package scope feeding both the
      handshake and registration-time advertisement, root→services go.mod
      edge with the collector confined to `cmd/internal/bundledwire`,
      static discovery entries, config-wins precedence, error-class
      status-details fallback in the in-proc CP client. Proofs: bundled
      unit tests, serverkit adapter tests, zero-executor-config in-proc
      dispatch scenario (`TestBundledInProcDispatchZeroExecutorConfig`),
      single-process + compose + claude-agent cross-stack + http-node/
      verifier cross-stack scenarios green on rebuilt images; race-clean;
      lint green. Design docs: 4 decision creates, 3 concept mutates,
      story + decision mutate (single-process pair).
- [x] Step 4 (passes A–D) — `rimsky run <template>` self-hosts: the run
      verb's entry moved to the compose package (compose imports cli, not
      the reverse), lenient endpoint resolution with an
      `ErrNoEndpointConfigured` sentinel (no endpoint → self-host;
      `--self-host` overrides context/env; `--endpoint` + `--self-host`
      is a usage error; self-host rejects `--template` and explicit
      `--keep`), self-host runner reuses the compose one-shot machinery
      (spawnServices, synthetic configs, role stack with bundled
      registration, extracted shared `waitOneShotToTerminal`); unified-
      marker blob error names all three setters; env-passthrough
      regression test. Proof: in-process end-to-end (no Docker) —
      `TestRunTemplateRun_SelfHostDrivesBundledInProcExecutorToTerminal`
      drives a zero-config template through the in-proc bundled http-node
      handler to terminal in ~1s; CLI-path container scenarios green.
      Design docs: decisions rimsky-run-self-hosts-templates,
      rimsky-compose-run-scope, late-bound-services-direct-spawn,
      process-role-unified-message-covers-rimsky-run created; story
      local-orchestrator-zero-config created; concept rimsky + decision
      single-process-mode mutated.
- [x] Step 5 — cross-mode proofs + remaining design-doc sweep. Docs:
      stories portable-template-across-modes,
      claude-agent-mcp-servers-per-node, claude-agent-expose-env-per-node
      created; decision env-var-convention-across-modes created; story
      single-process-all-in-one Proof aligned with spec (process-level
      no-external-service-contacted assertion). Proofs: bundled in-proc
      dispatch scenario extended with the process-level assertion (peer
      is Static, endpoint `inproc://…`);
      `TestClaudeAgentPerNodeDivergence` covers per-node MCP + per-node
      expose-env divergence in one template (http + stdio transports
      end-to-end; module transport unit-covered by existing
      `TestRunAgentMcpServersReachSpawnAcrossTransports`);
      `TestPortableTemplateAcrossModes` drives the same template bytes
      through all-in-one and containerized modes and asserts
      terminal-graph-shape equality. Small production fix in
      observability: `/v1/observability/executors` and
      `/v1/observability/claim-producers` now surface bundled/static
      discovery entries (previously only surfaced configured peer
      specs) — the assertion needs them, and their absence was a real
      observability gap.

## Deviations / discoveries (surface at end of each step)

- **Module transport meaning in Go (Pass D).** TS `module` transport dynamically
  imported a JS module exporting a `createMcpServer()` factory. No JS runtime in
  the Go port, so `module` now resolves in an in-process registry
  (`RegisterMcpModule(specifier, factory)`) and is served over a loopback
  stdlib HTTP MCP server — same semantics (loopback HTTP reached by the CLI
  child, per-node declared, distinct transport), no Node. `http-loopback`
  alias preserved.
- **"Template" in rejection errors = instance (Pass D).** The executor wire
  (`ExecuteRequest`) carries `instance_id`/`node_id`/`node_type` but no
  template name, and the spec forbids adding protocol fields. Allowlist
  rejection errors name the disallowed entry + instance_id + node_id.
- **Genuine port bug found+fixed (Pass D).** TS relied on Node's
  single-threaded event loop to finish writing the report_complete response
  before dispatch-MCP close. In Go the teardown raced the in-flight response
  (CLI child would see EOF on its report_complete ack). Fixed with graceful
  `http.Server.Shutdown` in the MCP server Close path; regression covered by
  the race-detector suite.
- **Expose-env moved from runner-constructor to per-request (Pass B/D).** TS
  had container-wide `exposeEnvNames` at runner construction; the per-node
  redesign passes `ExposeEnvNames` on each spawn/resume request.
- **Park tool description (Pass C).** Dropped the `.ok-planner/specs/...`
  path and "Per 2026-05-14 Piece 2" references from agent-visible tool
  descriptions; semantics unchanged.
- **Two TS description sources unified (Pass C).** `TOOL_DEFINITIONS` vs
  `registerTools` texts diverged in TS; the wire-visible (`tools/list`)
  descriptions won; `toolDefinitions()` is now the single source.
- **Runtime image is wolfi, not distroless (Pass F).** TD said "distroless
  base shaped like http-node's Dockerfile", but the executor's job is
  spawning the `claude` CLI, which needs a real userland (shell for its Bash
  tool, git). Kept the TD's intent — the Node/npm surface is gone — via
  wolfi + the CLI's native binary distribution (pinned 2.1.186) + tini + git.
  Forced shape-change, not an undershoot.
- **Observability store extracted to a shared package (Pass E).** http-node
  had the only Go trace store; a second copy for claude-agent would violate
  strict DRY, so it moved to `lib/services/executors/internal/observability`
  and http-node was rewired in the same change (its store-level tests moved
  with it).
- **License boundary moved (Pass F).** claude-agent left the Apache carve-out
  in `licensing.yml` (and node_modules/dist exemptions dropped); Go sources
  carry the AGPL dual-license header; `make license-lint` green.
- **Pre-existing bug found+fixed (Pass F).** The openlineage subscriber
  container tests polled `node.state` from the observability node endpoint,
  but the node row no longer carries a state field (replaced by
  `run_summary` counts in earlier core work) — the tests waited forever and
  failed at HEAD independent of this step. Fixed the wait helper to
  categorize `run_summary` counts (the same idiom the cross-stack scenario
  uses); suite green.
- **Root-module scenario tests not re-run (Pass F).** No root-module source
  changed in step 1 (Makefile grep-filter removal only); lint compiled all
  modules; services-module suite (unit + containers) run in full.
- **Step 2: `ClassedError` interface added to `lib/protocols/claimproducer`
  (Pass B).** Bundled producers return classed domain errors that the gRPC
  hop converts to `ErrorInfo` and the peer client recovers into
  `ProducerCallError.ErrorClass`. In-proc needs the same channel without the
  hop; the contract lives in protocols (the only module both sides import).
  Unstated in the spec but required for error-class policy parity across
  modes.
- **Step 2: `ProducerCallError.Error()` wording generalized (Pass B).**
  "remote producer %q" → "producer %q" — the type is now constructed by both
  the gRPC peer client and the in-proc client; the old wording would lie for
  in-proc errors. No test asserted the old text.
- **Step 2: in-proc client never consults `handler.Capabilities` (Pass B).**
  Capabilities are construction data at registration (per the spec's
  advertisement model); the client returns the registered set. A test pins
  this by making the fake handler's `Capabilities` method error.
- **Step 2: pre-existing core bug found+fixed (Pass C) —
  `GetLatestRunForNode` ambiguous ordering.** The query ordered terminal
  runs by `sequence DESC`, but sequence is per (node, run_scope); since
  frames own fresh root run-scopes (015 migration), a node run in each of
  two frames ties at sequence 1 and Postgres picks arbitrarily. Consumers:
  message delivery, wake-parked, cascade recalculate, plus the scenario
  wait helper — surfaced as the flaky
  `TestPerRunAttributes_HardDepPullsUpstream` (~1-in-6). Fixed in both
  backends: order by active-first, `enqueued_at DESC, sequence DESC, id
  DESC`. Verified 15/15 (16th run was a Docker boot timeout, not the race).
- **Step 2: pre-existing stale fixtures fixed (Pass C).** Nine scenario
  tests (six in `frame_resolution`, plus frame-timeout-stuck,
  frame-timeout-progressing-loop, retention-sweep in the root scenarios
  package) hand-inserted `rimsky_frames` rows without the now-NOT-NULL
  `root_run_scope_id`; failing at HEAD. Fixed by capturing the instance's
  root scope before frame deletes and threading it through the inserts.
- **Step 2: go-test timeout kills now fail loudly (Pass C).** All Makefile
  go-test invocations run through `tools/gotest-guard.sh`: a package that
  hits the `-timeout` ceiling is killed by the go test runtime and every
  unreported test silently vanishes; the guard detects the timeout panic
  and fails with a distinct banner. Verified on a forced kill (exit 1,
  banner) and a passing run (exit 0).
- **Step 2: root scenarios package outgrew the make timeout (Pass C).**
  `test/scenarios` takes ~132s standalone; the Makefile's `-timeout 120s`
  killed it mid-run (masking every test that hadn't reported yet). Raised
  the root-module test timeouts to 300s. Also raised the compose drain
  test's spawn ReadyTimeout 5s → 30s (its assertion is drain boundedness,
  not spawn speed; 5s flakes under full-suite load).
- **Step 2: six scenario proofs fail at HEAD — frame-isolation fallout
  (Pass C, walked through with the user; see "Repair ledger" below).**
  Initially read as a design contradiction; the story walk-through
  resolved it: the frame-isolation rework (6a62f50f) was correct, but its
  test-reimplementation sweep stopped after the session-resume proof
  (065af196 names message-driven rounds "provably unsatisfiable" and the
  intra-frame cascade self-edge as the replacement shape). Five of six
  proofs passed at 6a62f50f~1; the breakage was masked by the 120s make
  timeout kill. Resolution per story is itemized in the repair ledger.

### Step-3 deviations / discoveries

- **Claude-agent credential gate reshaped.** `LoadOptsFromEnv()` demanded
  credentials at load time, which would abort every credential-less
  all-in-one boot. Moved the gate to `Opts.CredentialsConfigured()` +
  `ErrCredentialsMissing`; the standalone main still fails fast, the
  bundled entrypoint skips claude-agent with a log line (stub mode counts
  as configured). Captured in `decision:per-service-load-opts-from-env`.
- **Collector placement corrected mid-pass.** `CollectBundled` first landed
  in `lib/control/config` — a layered package importing the services
  module, which the module-layout concept forbids. Moved to
  `cmd/internal/bundledwire` (cmd-group-only visibility); the data-only
  `BundledRegistrations` type stays in the config package for role-config
  threading. `StartUnifiedStack` takes the registrations as a parameter;
  the two cmd callers (entrypoint, compose launcher) collect and abort
  boot on error.
- **Static discovery entries.** The discovery refresh loop re-probes every
  cache entry and would have marked in-proc entries unreachable, wiping
  their capabilities. `PeerEntry` gained a `Static` flag the refresh loop
  skips. Captured in
  `decision:bundled-executor-inproc-capability-advertisement`.
- **Config-wins precedence.** An executor or claim-producer name already
  declared in rimsky.yml keeps its configured endpoint; the bundled
  in-proc handler for that name is skipped with a log line (supervisor
  resolver, control-api executor map, and both locks registries).
- **Error-class parity fix in the step-2 client.** gRPC-server-backed
  handlers encode error classes as status ErrorInfo details, and
  `status.Err()` breaks the `errors.As` chain to the protocols
  `ClassedError`; `code:lib/runtime/claimproducer/inproc_client.go::callError`
  now falls back to peer's status-details extraction.
- **Shared claim-producer conversions.** proto↔Go conversions that peer
  duplicated inline now live once in `lib/protocols/serverkit`
  (plus `ServerBackedClaimProducer`, pure conversion, envelope policy
  stays in the runtime clients); `peer.Client`/`Dial` refactored onto
  them with identical error strings.
- **In-proc executor async-ack survives unchanged.** The in-proc client
  returns outcomes through the same `Client` interface as the wire, so
  claude-agent's AwaitAsync + HTTP callback to the in-process supervisor
  listener needs no adaptation; the adapter just drops the unused
  `HandlerContext`.
- **CP registration names** are the binary names (`store-filesystem`,
  `store-postgres`); registered only when the service's config env is set.
- **verifier-shape-checks' validation gRPC surface stays standalone-only**
  (the spec's in-proc categories are executor + claim-producer; only its
  executor protocol registers in-proc).
- **http-node stub gating** needs the `stub_probe` attribute in addition to
  `env:RIMSKY_EXECUTOR_STUB_MODE` — the smoke template carries
  `stub_probe: true` as a schema default.
- **Opts→Config mapping idiom**: postgres agent introduced
  `Opts.ServerConfig()`; mirrored onto the filesystem producer so both
  claim producers share one idiom.

### Step-4 deviations / discoveries

- **Run-verb entry moved to the compose package.** The self-host branch
  needs the compose machinery, and compose imports cli (not the reverse),
  so the router now sends `run` to `compose.RunTemplateRun`, which parses
  once and either self-hosts or delegates to `cli.RunRunRemote`. The old
  `cli.RunRun` was split into `ParseRunArgs` + `RunRunRemote` and deleted;
  tests updated to the new entries (existing remote tests now regression-
  test the dispatch discriminator via the env endpoint).
- **`--service` semantics differ by mode** (as spec'd): remote resolves
  bindings + auto-starts the host agent; self-host direct-spawns on
  loopback ports via the compose spawn path — validation of binding
  strings therefore happens in each branch, not at parse time.
- **Shared one-shot wait extracted.** The compose one-shot's wait/
  escalate/classify select is now `waitOneShotToTerminal`, used by both
  verbs (DRY; only the verb label differs).
- **Proof is in-process, not containerized.** The zero-config story proof
  runs the real verb inside the compose package's tests (fresh HOME, no
  endpoint, stub mode) and completes in ~1s — no Docker required; the
  container scenarios cover the compose/remote paths.
- **Docs pulled forward for citation resolution:** the
  rimsky-run-self-hosts-templates decision and local-orchestrator-
  zero-config story were created when the citing code landed (the lint
  hook checks resolution at write time), not at the end of the step.

### Step-5 deviations / discoveries

- **Observability endpoint was silently missing bundled peers.**
  `/v1/observability/executors` and `/v1/observability/claim-producers`
  only iterated the configured PeerSpec list, so the bundled/static
  entries populated into the discovery cache by `AdvertiseInto` were
  invisible to consumers of the endpoint (including the new
  process-level assertion). Extended both list handlers to merge in
  discovery-cache entries not already covered by a configured peer,
  and their `GET /:name` counterparts to accept static names. Not a
  regression introduced by step 5 — a pre-existing gap that the
  scenario proof exposed and this step fixed forward.
- **Bundled peer endpoint is `inproc://<name>`, not empty.** The
  no-external-service assertion originally checked for an empty
  endpoint; the truthful marker is the `inproc://` scheme (set by
  `bundled.AdvertiseInto`). Test updated to assert the scheme
  prefix — semantically stronger, since it distinguishes "no external
  contact" from "peer never registered".
- **Module transport scenario coverage stays in unit tests.**
  Standing up a module MCP server requires a registered
  `RegisterMcpModule` factory in the executor process; production
  currently registers none, and the fake-cli container is FROM the
  production executor image (no way to inject a test module without
  building an augmented executor binary). The existing unit test
  `TestRunAgentMcpServersReachSpawnAcrossTransports` already covers
  all three transports end-to-end at the executor-package boundary
  (writes the mcp.json, stands up the loopback, sees the tool call).
  The new scenario adds per-node divergence via http + stdio, which
  is what the story's *divergence* arm actually tests.
- **Fake-cli witness carries hashed env values.** The story's security
  invariant (rimsky never sees plaintext env values) is the point of
  the whole redesign; the witness therefore reports SHA-256 digests of
  observed env values, not the raw strings. `assertEnvPresent` hashes
  the expected plaintext locally and compares digests. The bag also
  gets grep'd for the plaintext just in case.
- **Story `single-process-all-in-one` Proof text tweaked to spec.** The
  in-repo story was missing the "process-level assertion" clause the
  spec's Design changes added; corrected in place. Assertion landed in
  the existing `TestBundledInProcDispatchZeroExecutorConfig`.

## Repair ledger (frame-isolation fallout — settled with the user 2026-07-03)

Ruling in effect: only a message runs a frame; every message gets a new
frame; a node's attributes at frame start are the default values; message
payloads are the ONLY cross-frame carrier.

1. **Empty-wake unification (fully spec'd by
   `decision:empty-message-as-root-trigger` — the live code is that
   decision's rejected alternative).** Seed the implicit `""` entry into
   the declared-types set at template registration; create the `""`
   message-receiver node at instance creation like any declared type;
   delete `cascadeEmptyMessageWakeInTx`, the `msg.Type == ""` delivery
   fork, and the endpoint's hard-coded `matched = true`; the
   cross-frame scope override (message_delivery.go:298 — the one hard
   isolation violation found in the code audit) dies with the branch.
   Ride-along: `story:empty-message-wakes-roots` falsifier still says
   "virtual" (pre-receiver-node vocabulary).
2. **Story `most-recent-coalesces-cascades` — rewrite.** Its user need
   (message backlog must not pile up behind a slow instance) lives at the
   message pool, not the node_run queue. Rewrite as a message-pool
   coalesce-mode story ("keep only the latest queued message while a
   frame is running"), in general user-story language; new pool
   capability to build (open sub-question: per-instance or
   per-message-type). `cascade_mode=most-recent` keeps its intra-frame
   node_run-queue role.
3. **Stories `sequenced-preserves-cascade-rounds` +
   `idempotent-mode-dedupes` — stories stand; proofs rewritten.** The
   mechanism (gate_evaluator cascade modes) is correct intra-frame
   node_run machinery; the tests drove rounds via messages. Rewrite each
   proof onto the intra-frame cascade self-edge shape.
4. **Story `cross-frame-coupling` — SPLIT (settled).** The story fuses two
   capabilities from different layers: its Role promises iterative/cyclic
   workflows as first-class graph objects (cascade layer — the self-edge
   subscription bounded by its `when:` predicate over run-local data such
   as `payload.attributes_delta`; the session-resume proof is the working
   precedent), while its Capability's first sentence is a different story
   entirely — a node sends a message to its own instance's queue (message
   layer; delivery only, no convergence promise). Split into two stories,
   each in its own layer's vocabulary; the diff-gate-convergence
   acceptance clause dissolves (the diff-gate's power is "no cascade
   occurs", never "no more frames open"). Old slug retired; `@story:`
   citations updated.
5. **Story `cascade-signal-blind` — story stands AS-IS; proof iteration
   rewritten (settled).** Read at cascade altitude the diff-gate clause
   is already correct ("prior run" in a cascade story can only mean the
   prior run in the same resolution) — adding frame-scoping language
   would itself be the layer conflation. Only the proof's diff-gate
   iteration is wrong: it posts two messages and expects the second
   frame's same-value settle to stay silent; rewrite it intra-frame
   (self-edge re-settle, receiver wakes exactly once).
6. **Story `cascade-defers-during-flight` — walked, confirmed: story
   stands, proof-rewrite only** (same bucket as item 3). The seal is now
   delivered twice over: intra-frame by the walker-queues-new-run rule +
   serialization gate; inter-frame structurally by frame serialization.
   Test currently drives A's re-run via message ("a should be re-invoked
   once for the second message"); rewrite onto the intra-frame self-edge.
7. **Audit result (for the record):** all attribute-value reads are
   scope-qualified (diff baseline, carry-forward, sender-dep
   substitution, cascade-mode dedup, dispatch input bags); message
   payloads frame-qualified. Compliant-but-fragile: `wake_parked` and
   operator `recalculate` key off the cross-frame `GetLatestRunForNode`.
8. **ok-planner user-story guidance clarification (meta).** Stories must
   be general user stories — no implementation specifics (no
   `cascade_mode=...` in a Role sentence). Several existing stories
   violate this; sweep opportunity when they're touched.
9. **Vocabulary: messages are SENT, signals are EMITTED (settled).**
   Send = push to a destination (an instance's message queue,
   idempotency-keyed, one frame each); emit = broadcast into the
   subscription fabric (receivers opt in by type-path + predicate).
   "A node sends a message" becomes visibly wrong on its face — the
   vocabulary marks the layer. Sweep in one change per the uniformity
   rule: `concept:message-sender-node` rename, "message-send endpoint" /
   "cascade-sent / operator-sent / publisher-sent" phrasings in
   `concept:message` + `concept:frame`, "universal message-send surface"
   in `story:empty-message-wakes-roots`, code symbol
   `EmitCascadeMessage` (~15 non-test sites). Signal-side emit
   (`EmitSignal`, diff-gated emission) already correct, untouched.
