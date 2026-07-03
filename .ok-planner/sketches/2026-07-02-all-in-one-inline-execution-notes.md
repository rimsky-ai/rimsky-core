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

## Status

- [x] Step 1 (passes A–F) — Go port landed, TS retired, conformance 9/9,
      cross-stack + session-resume container scenarios green, design docs
      mutated (story rewrite, 11 decision creates, 1 decision mutate,
      module-layout licensing edit)
- [ ] Step 2
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
