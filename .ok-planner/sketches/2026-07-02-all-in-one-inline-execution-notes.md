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
- [ ] Step 3
- [ ] Step 4
- [ ] Step 5

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
   "A node emits a message" becomes visibly wrong on its face — the
   vocabulary marks the layer. Sweep in one change per the uniformity
   rule: `concept:message-emitter-node` rename, "message-emit endpoint" /
   "cascade-emitted / operator-emitted / publisher-emitted" phrasings in
   `concept:message` + `concept:frame`, "universal message-emit surface"
   in `story:empty-message-wakes-roots`, code symbol
   `EmitCascadeMessage` (~15 non-test sites). Signal-side emit
   (`EmitSignal`, diff-gated emission) already correct, untouched.
