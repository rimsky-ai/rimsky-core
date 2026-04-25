# Rimsky Go Port — Plan B — Reference Executors

**Goal:** Implement the two v1 reference executors that speak the cell-executor protocol: `http-node` (Go) and `claude-agent` (TypeScript). Each is a peer service the rimsky supervisor dispatches to; each is deployable as its own Docker image; `claude-agent` additionally publishes to npm. `claude-agent` absorbs the TS project's agentic subsystem (Claude CLI spawning, internal MCP callback, token registry, subprocess lifecycle) entirely — that code moves out of the orchestrator into the executor, proving the three-collection separation.

**End state after this plan:** a developer can run `go build ./executors/http-node` and get a working Go binary; `cd executors/claude-agent && npm install && npm run build` produces a Node package; both executors pass the stub-mode conformance probe (full conformance suite lands in Plan C); the Plan A scenario suite gains two end-to-end scenarios that use the real reference executors (not just the stub).

**Architecture:** both executors are peer services; no Go-module coupling to `core/`. Both speak gRPC canonical + HTTP+JSON bridge. `http-node` imports the Go protobuf bindings directly. `claude-agent` uses `@grpc/grpc-js` + `@grpc/proto-loader` (no generated TypeScript committed; proto files are loaded at runtime). Both advertise stub mode via env var `RIMSKY_EXECUTOR_STUB_MODE=1` per spec §14.4.

**Tech stack (Go executor):** Go 1.22+, `google.golang.org/grpc`, `go-chi/chi` (for HTTP bridge), `testify`, stdlib `log/slog`.
**Tech stack (TypeScript executor):** Node 20+, TypeScript (ES2022, NodeNext), `@grpc/grpc-js`, `@grpc/proto-loader`, `fastify` (for HTTP bridge + optional callback endpoints needed by its internal MCP), `pino`, `vitest`.

**Reference documents:**
- Spec: `docs/specs/2026-04-23-rimsky-go-port-design.md`
- Plan A (complete): `docs/plans/2026-04-23-rimsky-go-port-plan-a-foundation.md`
- TS v1 agentic machinery being rehomed (reference, not imported): `rimsky/src/callback-mcp/`, `rimsky/src/supervisor/agentic-runner.ts`, `rimsky/src/supervisor/cli-runner.ts`

---

## Phase 0 — Amendments to Plan A (none required)

**Outcome:** The Plan A `supervisor/runner.go` is designed to dispatch to any executor speaking the protocol — no Plan A changes needed. The stub executor from Plan A Phase 12 is retained for scenario tests; the reference executors implemented here are in addition.

Verify before starting Plan B:
1. `cd rimsky-go && go test ./... -count=1` passes.
2. `proto/v1/gen/` has committed generated code.
3. `executors/stub/` is present (Plan A Task 12.1).

---

## Phase 1 — `http-node` executor (Go)

### Task 1.1 — Package scaffold

**Files:**
- `rimsky-go/executors/http-node/README.md`
- `rimsky-go/executors/http-node/main.go` (empty stub for now)
- `rimsky-go/executors/http-node/config.go`

**Steps:**

1. Create `rimsky-go/executors/http-node/` directory.
2. The Go module for `http-node` is the **same** single `rimsky-go/core` module — `executors/http-node/` is a `package main` inside the module. Import path: `github.com/fallguy/rimsky/core/executors/http-node`. Justification: spec §4 keeps the single Go module for v1; splitting into its own module is post-v1 work.

   **Important:** the executor MUST NOT import `core/` internals — only `core/proto/v1/gen/` (the generated protobuf bindings) and stdlib/external libs. Add a comment in `main.go`:
   ```go
   // Package main is the http-node reference executor. Per spec §3.2 it has
   // ZERO imports from `core/` except `core/proto/v1/gen/`. Adding any
   // `core/*` import here (other than proto bindings) violates the three-
   // collection separation and must be rejected at review.
   ```
3. Write `README.md` describing what http-node does in 5–10 lines with a usage example.
4. `config.go`: env-var reading:
   - `RIMSKY_EXECUTOR_HTTP_NODE_HOST` (default `0.0.0.0`)
   - `RIMSKY_EXECUTOR_HTTP_NODE_PORT` (default `9091`)
   - `RIMSKY_EXECUTOR_HTTP_NODE_TIMEOUT_MS` (default `60000`)
   - `RIMSKY_EXECUTOR_HTTP_NODE_MAX_BODY_BYTES` (default `10485760` = 10 MiB)
   - `RIMSKY_EXECUTOR_STUB_MODE` (default `0`)

**Verification:** `go build ./executors/http-node/` exits 0.

---

### Task 1.2 — Implement `Execute` gRPC server

**Files:**
- `rimsky-go/executors/http-node/server.go`
- `rimsky-go/executors/http-node/server_test.go`

**Steps:**

1. Implement `genv1.NodeExecutorServer`:
   ```go
   type Server struct {
       genv1.UnimplementedNodeExecutorServer
       cfg      Config
       client   *http.Client
       stubMode bool
   }

   func (s *Server) Execute(req *genv1.ExecuteRequest, stream genv1.NodeExecutor_ExecuteServer) error {
       if s.stubMode {
           return s.executeStub(req, stream)
       }
       ud := req.Userdata.AsMap()   // google.protobuf.Struct → map
       url, _ := ud["url"].(string)
       method, _ := ud["method"].(string)
       if method == "" { method = "GET" }
       // headers, body, etc. from ud
       httpReq, err := http.NewRequestWithContext(stream.Context(), method, url, bodyReader)
       // ... set headers ...
       resp, err := s.client.Do(httpReq)
       if err != nil {
           return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Errored{
               Errored: &genv1.Errored{ErrorClass: "http_request_failed", Payload: structpb.NewStringValue(err.Error())},
           }})
       }
       body, _ := io.ReadAll(resp.Body)
       // Parse JSON response if content-type is application/json; otherwise fall back to base64.
       var resultStruct *structpb.Struct
       // ... marshal body into resultStruct ...
       // Compare to userdata["previous_result"] (supervisor passes prior version in reads_data? TBD).
       // For v1, http-node always returns changed=true (stateless). "No-op" detection is the
       // template author's job (possibly later via a `changed_if` expression in userdata).
       return stream.Send(&genv1.ExecuteEvent{Event: &genv1.ExecuteEvent_Complete{
           Complete: &genv1.Complete{Result: structpb.NewStructValue(resultStruct), Changed: true, ChangeSummary: "http response"},
       }})
   }
   ```
2. Userdata schema: `{url: string, method?: string, headers?: map<string,string>, body?: string|object, expect_status?: [int]}`. Validate in userdata parse.
3. Error classes emitted by `http-node`:
   - `http_request_failed` (network error, DNS failure).
   - `http_unexpected_status` (response status not in `expect_status`; default expectation is 200–299).
   - `http_response_parse_failed` (body claimed to be JSON but wasn't).
   - `invalid_userdata` (userdata missing required fields).
4. Tests: unit tests with `httptest.NewServer` as the upstream. Cover happy path, 500 response → `http_unexpected_status`, network failure → `http_request_failed`, malformed userdata → `invalid_userdata`.

**Verification:** `go test ./executors/http-node/...` passes.

---

### Task 1.3 — Stub mode

**Files:** extends `server.go` with `executeStub` method.

**Steps:**

1. Per spec §14.4, `http-node` in stub mode doesn't make any real HTTP request. It returns a deterministic canned response.
2. Behavior: `executeStub(req, stream)` checks `userdata.stub_response` if present, returns that as the result; otherwise returns a fixed `{"stub": true}` with `Changed: true`.
3. Test: with `RIMSKY_EXECUTOR_STUB_MODE=1`, Execute returns the stub response without making real network calls.

**Verification:** `go test ./executors/http-node/... -run TestStubMode` passes.

---

### Task 1.4 — HTTP+JSON bridge

**Files:** `rimsky-go/executors/http-node/bridge.go`

**Steps:**

1. Expose the protocol over HTTP on the same port as gRPC (shared listener via a mux) OR on a separate port.
   - Go v1: same port via `cmux` — one listener, demux by protocol (HTTP/2 cleartext vs HTTP/1.1). Too complex for v1.
   - Simpler: separate ports. `RIMSKY_EXECUTOR_HTTP_NODE_PORT` for gRPC; `RIMSKY_EXECUTOR_HTTP_NODE_HTTP_PORT` for HTTP+JSON (default = gRPC port + 1).
2. HTTP bridge route: `POST /v1/Execute` — body `application/json` matching the `ExecuteRequest` JSON form; response `application/x-ndjson` with one `ExecuteEvent` per line.
3. Internally, the HTTP bridge calls the same server logic as the gRPC `Execute` method by constructing an in-process server stream.
4. Test: HTTP bridge test mirrors the gRPC tests; same assertions.

**Verification:** `go test ./executors/http-node/... -run TestHTTPBridge` passes.

---

### Task 1.5 — `main.go`

**Files:** `rimsky-go/executors/http-node/main.go`

**Steps:**

1. Read config; set up logger.
2. Start gRPC listener; start HTTP bridge listener.
3. Register `NodeExecutorServer`.
4. Handle SIGTERM/SIGINT → graceful stop.

**Verification:** `go build ./executors/http-node/` → binary; `./http-node` with no env starts on 0.0.0.0:9091/9092 and responds to both `POST /v1/Execute` (HTTP) and a raw gRPC `Execute` call.

---

### Task 1.6 — End-to-end scenario test using `http-node` + rimsky

**Files:** `rimsky-go/test/scenarios/http_node_end_to_end_test.go`

**Steps:**

1. Spin up the scenario harness (Plan A Task 13.1).
2. Spin up an `http-node` server in-process (via `server.New(cfg)` + `grpc.NewServer()`).
3. Spin up a fake upstream (`httptest.NewServer`) returning a JSON payload.
4. Deploy a template with a single node: `executor: http-node`, `userdata: {url: "<upstream URL>"}`, committing to an `inline-jsonb` resource.
5. Create an instance; wait for the node to transition `stale → running → fresh`; assert the resource's current version matches the fake upstream's payload.

**Verification:** test passes.

---

## Phase 2 — `claude-agent` executor (TypeScript)

### Task 2.1 — Node package scaffold

**Files:**
- `rimsky-go/executors/claude-agent/package.json`
- `rimsky-go/executors/claude-agent/tsconfig.json`
- `rimsky-go/executors/claude-agent/vitest.config.ts`
- `rimsky-go/executors/claude-agent/.eslintrc.cjs`
- `rimsky-go/executors/claude-agent/README.md`
- `rimsky-go/executors/claude-agent/src/` (empty, populated in subsequent tasks)

**Steps:**

1. `package.json`:
   ```json
   {
     "name": "@rimsky/executor-claude-agent",
     "version": "0.1.0",
     "private": true,
     "type": "module",
     "main": "./dist/index.js",
     "types": "./dist/index.d.ts",
     "bin": { "rimsky-executor-claude-agent": "./dist/main.js" },
     "scripts": {
       "build": "tsc",
       "dev": "tsc --watch",
       "test": "vitest run",
       "lint": "eslint src"
     },
     "dependencies": {
       "@grpc/grpc-js": "^1.10.0",
       "@grpc/proto-loader": "^0.7.10",
       "fastify": "^4.29.1",
       "pino": "^8.17.0",
       "zod": "^3.22.4",
       "yaml": "^2.3.4"
     },
     "devDependencies": {
       "typescript": "^5.3.2",
       "vitest": "^1.0.4",
       "@types/node": "^20.10.0"
     }
   }
   ```
2. `tsconfig.json`: ES2022 + NodeNext + strict, rootDir `src`, outDir `dist`.
3. `vitest.config.ts`: matches TS v1 pattern.
4. `README.md`: describes claude-agent's role as a rimsky reference executor, scope, config.
5. `npm install` to generate `package-lock.json`.

**Verification:** `cd rimsky-go/executors/claude-agent && npm install && npx tsc --noEmit` exits 0 (empty src compiles).

---

### Task 2.2 — gRPC server skeleton

**Files:**
- `rimsky-go/executors/claude-agent/src/server.ts`
- `rimsky-go/executors/claude-agent/src/proto-loader.ts`

**Steps:**

1. `proto-loader.ts`: load the `.proto` files from `../../proto/v1/` at startup using `@grpc/proto-loader`. Return a typed descriptor object.
2. `server.ts`: skeleton gRPC server that registers a `NodeExecutor` implementation with only the `Execute` method (unary-to-stream).
3. The server implementation stub emits `Heartbeat` then `Complete` with a placeholder result — will be replaced in Task 2.4 with real logic.

**Verification:** `npm run build` produces `dist/` with no errors.

---

### Task 2.3 — Absorb TS v1 agentic subsystem

**Files (source — ported, not imported):**
- `rimsky-go/executors/claude-agent/src/cli-runner.ts` ← port of `rimsky/src/supervisor/cli-runner.ts`
- `rimsky-go/executors/claude-agent/src/internal-mcp-server.ts` ← port of `rimsky/src/callback-mcp/server.ts`
- `rimsky-go/executors/claude-agent/src/internal-mcp-tools.ts` ← port of `rimsky/src/callback-mcp/tools.ts`
- `rimsky-go/executors/claude-agent/src/token-registry.ts` ← port of `rimsky/src/callback-mcp/token-registry.ts`
- `rimsky-go/executors/claude-agent/src/agent-run.ts` ← new: orchestrates cli-runner + internal MCP + silence detection; the equivalent of TS v1's `supervisor/agentic-runner.ts` but *inside* the executor

**Steps:**

1. Copy each TS v1 source file to its new home (do not import; the TS project stays unchanged per execution-chain guardrail #6). Adapt:
   - Remove any imports from `rimsky/src/storage/`, `rimsky/src/queue/`, `rimsky/src/cell/`, etc. The agentic subsystem knows nothing about dispatch rows or state machines now — it just runs an agent and reports the outcome.
   - Remove the `applyTerminalOutcome` call at the end. The new agent-run returns a typed outcome object; server.ts maps it to protocol events.
   - Remove the `verifyAgenticClaimOwnership` call. That was a supervisor-side concern; now it's the rimsky supervisor's concern (Plan A Task 10.3).
   - Drop all event-log writes (no `storage.events.append`). The rimsky supervisor logs `work_started`, `work_completed`, etc.; the executor emits protocol events.
2. `agent-run.ts` signature:
   ```typescript
   export interface AgentRunArgs {
     model: string;
     systemPrompt: string;
     userPrompt: string;
     tools: ToolConfig[];
     resultSchema: JsonSchema;
     silenceTimeoutMs: number;
   }
   export type AgentRunOutcome =
     | { kind: "complete"; result: unknown; changed: boolean; changeSummary: string | null }
     | { kind: "blocked"; reason: string; context: unknown }
     | { kind: "error"; errorClass: string; payload: unknown }
     | { kind: "infra_error"; errorClass: string; payload?: unknown };

   export async function runAgent(args: AgentRunArgs, abortSignal: AbortSignal): Promise<AgentRunOutcome> { ... }
   ```
3. Internal MCP server runs on an OS-assigned localhost port per invocation; token is generated per invocation; subprocess spawned with `RIMSKY_CALLBACK_URL` / `RIMSKY_CALLBACK_TOKEN` pointing at this executor's internal MCP. The rimsky orchestrator has no knowledge of this subsystem.
4. Silence detection preserved (spec §14.4 notes silence-timeout becomes an Errored event with error_class `silence_timeout`; the executor maps to `infra_error` → server.ts maps to `Errored`).
5. JSON-schema validation of agent-reported `result` happens inside `agent-run.ts` using a vendored schema validator (e.g. `ajv`). On validation failure: the internal MCP returns `{status: "rejected", errors}` to the subprocess (preserving the in-conversation retry pattern from TS v1); the subprocess can retry in-session.
6. Unserializable-result guard preserved (BigInt/circular detection).

**Verification:** `npm run build` exits 0; unit tests for each ported module pass (port the TS tests too — at least the core correctness ones for cli-runner, token-registry, internal MCP).

---

### Task 2.4 — Wire `server.ts` Execute → runAgent

**Files:** `rimsky-go/executors/claude-agent/src/server.ts`

**Steps:**

1. Replace the Task 2.2 stub Execute with:
   ```typescript
   async function handleExecute(req, stream) {
     const userdata = structToObject(req.userdata);
     const { model, system_prompt, user_prompt_template, tools, result_schema } = userdata;
     // Render user_prompt_template with instance_params, deps_data, reads_data.
     const userPrompt = renderTemplate(user_prompt_template, {
       source: structToObject(req.instance_params),
       deps: Object.fromEntries(Object.entries(req.deps_data).map(([k,v]) => [k, valueToJs(v)])),
       reads: Object.fromEntries(Object.entries(req.reads_data).map(([k,v]) => [k, valueToJs(v)])),
     });
     // Decide: sync (stream stays open) or async handoff (return AsyncAccepted + callback later).
     // For v1: always async handoff because agent runtimes are long. Immediately emit one
     // Heartbeat and then AsyncAccepted with an ack id.
     const ackId = randomUUID();
     stream.write({ event: { kind: "heartbeat", heartbeat: { timestampMs: Date.now(), note: "agent spawned" } } });
     stream.write({ event: { kind: "asyncAccepted", asyncAccepted: { asyncAckId: ackId, expectedCompletionMs: 0 } } });
     stream.end();
     // Background: run the agent; post outcome to req.callback_url.
     runAgent({ model, systemPrompt: system_prompt, userPrompt, tools, resultSchema: result_schema, silenceTimeoutMs: cfg.silenceTimeoutMs }, abortSignal)
       .then(outcome => postCallback(req.callback_url, ackId, outcome))
       .catch(err => postCallback(req.callback_url, ackId, { kind: "infra_error", errorClass: "claude_agent_internal", payload: { message: String(err) } }));
   }
   ```
2. `postCallback(url, ackId, outcome)` makes an HTTP POST to `<url>/v1/callback/<ackId>` with the JSON form of the outcome mapped to Complete/Blocked/Errored protocol messages.
3. Stub mode: `RIMSKY_EXECUTOR_STUB_MODE=1` → `runAgent` is replaced with a stub that returns a canned `{ kind: "complete", result: { stub: true }, changed: true, changeSummary: "stub" }` after a short delay.

**Verification:** unit tests with a fake callback server verify end-to-end flow in stub mode; vitest passes.

---

### Task 2.5 — HTTP+JSON bridge

**Files:** `rimsky-go/executors/claude-agent/src/http-bridge.ts`

**Steps:**

1. Use Fastify for the HTTP+JSON bridge. Routes:
   - `POST /v1/Execute` → same logic as the gRPC Execute handler; response is `application/x-ndjson` with one event per line.
2. Share the handleExecute function between gRPC and HTTP — extract into `handleExecuteCore(req, emit)` where `emit` is transport-agnostic.

**Verification:** HTTP-bridge unit test passes.

---

### Task 2.6 — `main.ts` entry point

**Files:** `rimsky-go/executors/claude-agent/src/main.ts`

**Steps:**

1. Read env vars:
   - `RIMSKY_EXECUTOR_CLAUDE_AGENT_HOST` (default `0.0.0.0`)
   - `RIMSKY_EXECUTOR_CLAUDE_AGENT_GRPC_PORT` (default `9090`)
   - `RIMSKY_EXECUTOR_CLAUDE_AGENT_HTTP_PORT` (default `9090` + 1 → need to pick: use `9190` or gRPC+1 convention; go with `9190` for clarity)
   - `RIMSKY_EXECUTOR_CLAUDE_AGENT_SILENCE_TIMEOUT_MS` (default `180000`)
   - `ANTHROPIC_API_KEY` (required unless stub mode)
   - `RIMSKY_EXECUTOR_STUB_MODE`
2. Start gRPC server + HTTP bridge; handle SIGTERM/SIGINT.

**Verification:** `npm run build && node dist/main.js` starts the server (in stub mode to avoid API-key requirement in CI).

---

### Task 2.7 — End-to-end scenario using `claude-agent` stub mode

**Files:** `rimsky-go/test/scenarios/claude_agent_end_to_end_test.go`

**Steps:**

1. The Go scenario harness spawns the TypeScript executor as a subprocess (`node executors/claude-agent/dist/main.js`) with `RIMSKY_EXECUTOR_STUB_MODE=1` and records its endpoint.
2. Deploy a template with one node: `executor: claude-agent`, `userdata: { model: "stub", system_prompt: "...", user_prompt_template: "...", tools: [], result_schema: {} }`.
3. Create an instance; wait for async handoff + callback; assert the node reaches `fresh` with the stub result committed.

**Verification:** test passes.

---

## Phase 3 — Stub-mode conformance probe

### Task 3.1 — Minimal conformance probe binary (foreshadowing Plan C)

**Files:** `rimsky-go/core/cmd/rimsky-conformance-probe/main.go`

**Steps:**

1. A tiny CLI (`rimsky-conformance-probe`) that takes `--endpoint <url>` `--transport grpc|http` and runs a single ExecuteRequest with a stub-mode probe userdata.
2. If the response is a Complete with `result.stub == true`, exits 0. Otherwise exits 1 with a descriptive error.
3. This is a minimal precursor to Plan C's full conformance suite; lives here because it's useful NOW for Plan B's verification gate.

**Verification:**
- Against `http-node` in stub mode: `rimsky-conformance-probe --endpoint grpc://localhost:9091 --transport grpc` exits 0.
- Against `claude-agent` in stub mode: `rimsky-conformance-probe --endpoint grpc://localhost:9090 --transport grpc` exits 0.

---

## Phase 4 — Definition of Done

### Task 4.1 — Gate verification

**Steps:**

1. `cd rimsky-go && go build ./...` → exit 0.
2. `go test ./... -count=1` → all tests passing (including the two new end-to-end scenarios).
3. `go vet ./...` → exit 0.
4. `golangci-lint run` → exit 0.
5. `cd rimsky-go/executors/claude-agent && npm install && npm run build && npm run test && npm run lint` → all exit 0.
6. Both reference executors pass the `rimsky-conformance-probe` in stub mode.
7. The three-collection separation holds: `grep -r "rimsky/core/cell\|rimsky/core/scheduler\|rimsky/core/supervisor" rimsky-go/executors/http-node/` returns no matches. `grep -r "rimsky/src/" rimsky-go/executors/claude-agent/src/` returns no matches.
8. Append a Plan B entry to `rimsky-go/CHANGELOG.md`.
9. Append a `plan_completed` entry to the execution log.

**Verification:** all gates green.

---

## Appendix — Subagent dispatch notes

**Parallelizable groups:**
- Phase 1 (http-node) and Phase 2 (claude-agent) can run in parallel — they share no files.
- Within Phase 2, Tasks 2.2 (skeleton) and 2.3 (absorb TS agentic subsystem) can be parallelized since they touch different files; Task 2.4 depends on both.

**Critical-path tasks:**
- Task 2.3 — the TS v1 agentic subsystem port has the most nuance. Reviewer should verify the BigInt/circular detection, the internal MCP's multi-tenant token handling, and the silence-detection loop all survive the port.
- Task 2.4 — the async-handoff flow involves getting the gRPC stream semantics right (emit heartbeat + AsyncAccepted + end; background callback). Reviewer should verify the stream closes cleanly before the background callback fires (otherwise the supervisor sees a stream close and interprets as infra error).
