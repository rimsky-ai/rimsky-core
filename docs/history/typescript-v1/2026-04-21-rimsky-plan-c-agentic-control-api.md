# Rimsky Plan C — Agentic Execution + Control API + Reference Binaries

**Goal:** Complete the v1 platform by adding (1) agentic cell execution via Claude CLI subprocesses with a multi-tenant callback MCP server, `report_complete` / `report_blocked` / `report_error` protocol, silence detection, and real+fake CLI runners; (2) the HTTP control API for template CRUD, instance management, cell state reads, operator overrides, event reads, resource reads, and health; and (3) env-var reference binaries that wire the library entry points for docker-compose / k8s deployment.

**Architecture:** The supervisor gains a second execution path (`runAgenticCell`) alongside the deterministic path from Plan B. A per-supervisor callback MCP server runs for the lifetime of the supervisor process and routes incoming `report_*` calls to the correct in-flight cell via a token. The control API is a separate library entry point (`startControlApi`) backed by an HTTP framework (Fastify). Three thin env-var-reading binaries (`rimsky-scheduler`, `rimsky-supervisor`, `rimsky-control-api`) compose `start*` functions into deployable processes.

**Tech stack:** All of Plan A + Plan B, plus: `fastify` for the control API, `@modelcontextprotocol/sdk` (or a minimal HTTP-JSON-RPC shim if the SDK is overkill), `zod` for HTTP request validation (already a dep), `execa` or node's built-in `child_process` for subprocess spawning.

**Prerequisites:**
- Plan A + Plan B complete. `npm test` green. Stored supervisor rows, heartbeats, kill polling, deterministic commit/error flow all working.

**Reference documents:**
- Spec: `docs/specs/2026-04-21-rimsky-v1-design.md` — particularly §9 (agentic execution), §10 (control API), §11 (entry points + binaries).
- Design: `docs/cell-graph-design.md`.

---

## New/changed files this plan produces

```
rimsky/src/
├── supervisor/
│   ├── agentic-runner.ts             # the agentic execution path
│   ├── agentic-runner_test.ts
│   ├── cli-runner.ts                 # CliRunner interface + ClaudeCliRunner + FakeCliRunner
│   ├── cli-runner_test.ts
│   └── supervisor.ts                 # extended to dispatch by cell kind (no rewrite of Plan B paths)
├── callback-mcp/
│   ├── server.ts                     # multi-tenant MCP HTTP server
│   ├── server_test.ts
│   ├── tools.ts                      # report_complete / report_blocked / report_error tool definitions
│   ├── tools_test.ts
│   └── token-registry.ts             # in-memory per-supervisor token map
├── control-api/
│   ├── app.ts                        # Fastify app factory
│   ├── routes/
│   │   ├── templates.ts
│   │   ├── instances.ts
│   │   ├── cells.ts
│   │   ├── events.ts
│   │   ├── resources.ts
│   │   └── health.ts
│   ├── routes/*_test.ts              # one per route
│   ├── schemas.ts                    # zod request/response schemas
│   ├── errors.ts                     # HTTP error-mapping helper
│   └── control-api_test.ts           # integration test across routes
├── config/
│   └── control-api.ts                # startControlApi entry point
├── entrypoints/
│   ├── scheduler.ts                  # rimsky-scheduler binary
│   ├── supervisor.ts                 # rimsky-supervisor binary
│   └── control-api.ts                # rimsky-control-api binary
└── index.ts                          # add startControlApi to public exports

rimsky/test/
├── fakes/
│   ├── fake-cli-runner.ts            # scripted CLI simulator for agentic scenarios
│   └── mcp-client.ts                 # lightweight client for hitting supervisor callback MCP in tests
├── fixtures/templates/
│   ├── agentic-happy-path.yaml
│   ├── agentic-invalid-result-retry.yaml
│   ├── agentic-blocked.yaml
│   ├── agentic-silence-timeout.yaml
│   └── agentic-error-class.yaml
└── scenarios/
    ├── agentic-happy-path_test.ts
    ├── agentic-invalid-result-retry_test.ts
    ├── agentic-blocked_test.ts
    ├── agentic-silence-timeout_test.ts
    ├── agentic-error-class_test.ts
    ├── control-api-templates_test.ts
    ├── control-api-instances_test.ts
    ├── control-api-operator-overrides_test.ts
    └── end-to-end_test.ts            # full flow: deploy, instance, run, complete via HTTP

rimsky/package.json                   # add bin entries for the three binaries
```

---

## Phase 0: Amendments to Plan B

### Task 0.1: Update supervisor `validateConfig` for agentic acceptance

**Files:** `rimsky/src/supervisor/supervisor.ts`

**Steps:**

1. Locate `validateConfig` (Plan B Task 5.1 step 2). Currently throws if `accepts` contains `"agentic"`.
2. Replace that throw with the following logic (still in `validateConfig`):
   - If `accepts` contains `"agentic"` and `cfg.cliRunner` is missing → throw `"Agentic acceptance requires cliRunner"`.
   - If `accepts` contains `"agentic"` and `cfg.callback` is missing → throw `"Agentic acceptance requires callback configuration"`.
3. All agentic-vs-deterministic configuration checks live in `validateConfig`. Runtime code (`startSupervisor` lifecycle) assumes the config is valid after this gate.

**Verification:** Plan B's deterministic supervisor tests still pass. New negative test: `startSupervisor({accepts: ["agentic"]})` with no `cliRunner` throws a clear message at construction.

---

### Task 0.3: Add `kill_requested` clearing to Plan B's state transitions

**Files:** `rimsky/src/supervisor/commit.ts`, `rimsky/src/supervisor/on-error.ts`

**Rationale:** `kill_requested` is set on a running cell via the control API; if it's not cleared when the cell leaves `running`, a subsequent run could be killed spuriously.

**Steps:**

1. In every code path that transitions a cell out of `running` (commit flow in `commit.ts`, error flow in `on-error.ts`), clear `kill_requested` in the same update that changes `state` / `assigned_supervisor_id`. **Implementation: update `CellStore.updateState` (Plan A) to unconditionally set `kill_requested = false` as part of its UPDATE when the new state is anything other than `running`.** No signature change; no optional flag. `kill_requested` is meaningful only while `running`, so this is always correct.

2. Add a test in `supervisor_test.ts`: set `kill_requested = true` via storage, have the supervisor terminate the run (infra error), then verify the column is `false` after the transition.

**Verification:** Plan B's tests still pass. New assertion added.

---

### Task 0.4: Add `work_rejected` event kind and amend the spec enumeration

**Files:** `docs/specs/2026-04-21-rimsky-v1-design.md` (§3.2 event kinds list), `rimsky/src/shared/types.ts` (if event kinds are enumerated there; otherwise the kind lives as a string in the event payload).

**Steps:**

1. Spec amendment: add to the event-kind enumeration in §3.2: `work_rejected` — payload `{reason: string, errors?: object}`. Emitted when an agentic cell's `report_complete` result fails schema validation.
2. No schema migration required (event `kind` is a string column).

**Verification:** Spec updated; referenced consistently in Task 5.2.

---

### Task 0.2: Dispatch by cell kind in the supervisor

**Files:** `rimsky/src/supervisor/supervisor.ts`

**Steps:**

1. In `runInBackground(row)` (Plan B Task 5.1 step 1), branch on `row.cell_kind`:
   ```ts
   if (row.cell_kind === "deterministic") {
     await runDeterministicCell({ /* as in Plan B */ });
   } else if (row.cell_kind === "agentic") {
     await runAgenticCell({ /* see Plan C Task 3.1 */ });
   } else {
     // Shouldn't happen: queue claim filters by accepts; timer kind is never queued.
     throw new Error(`Unexpected cell kind in dispatch: ${row.cell_kind}`);
   }
   ```

2. `runAgenticCell` is added in a later task; this step just prepares the dispatch.

**Verification:** `npx tsc --noEmit` passes with a stubbed `runAgenticCell` (can be `throw new Error("not implemented")` temporarily); Plan B's deterministic tests still pass.

---

## Phase 1: CLI runner abstraction

### Task 1.1: `src/supervisor/cli-runner.ts` — interface

**Files:** `rimsky/src/supervisor/cli-runner.ts`

**Steps:**

1. Define:
   ```ts
   export interface CliSpawnRequest {
     model: string;
     systemPrompt: string;
     userPrompt: string;
     tools: CliToolConfig[];                  // includes the callback MCP plus any cell-declared tools
     env: Record<string, string>;             // supervisor injects RIMSKY_CALLBACK_URL, RIMSKY_CALLBACK_TOKEN
     cwd?: string;
   }

   export interface CliHandle {
     onStdout(cb: (chunk: string) => void): void;
     onStderr(cb: (chunk: string) => void): void;
     onExit(cb: (exitCode: number | null, signal: NodeJS.Signals | null) => void): void;
     sendSigterm(): void;
     sendSigkill(): void;
     /** waits until exit fires */
     waitExit(): Promise<{ exitCode: number | null; signal: NodeJS.Signals | null }>;
   }

   export interface CliRunner {
     spawn(req: CliSpawnRequest): Promise<CliHandle>;
   }

   export interface CliToolConfig {
     kind: "mcp-http";
     name: string;
     url: string;
     headers?: Record<string, string>;
   }
   ```

2. `CliToolConfig.kind` is open for future expansion; v1 supports MCP-over-HTTP only.

**Verification:** `npx tsc --noEmit` passes.

---

### Task 1.2: `ClaudeCliRunner` — real implementation

**Files:** `rimsky/src/supervisor/cli-runner.ts` (same file — implementation alongside interface)

**Steps:**

1. Use `child_process.spawn("claude", [...args], { env, cwd })` to launch the CLI.
2. CLI arguments:
   - `--model <model>`
   - `--system-prompt-file <tmpfile with system prompt>`
   - MCP tool config passed via a JSON config file the CLI reads; path passed as `--mcp-config <tmpfile>`. The content is a standard MCP client config listing the callback server URL under the `rimsky-callback` name plus any cell-declared tool MCP servers.
   - User prompt written to stdin (`process.stdin.write(userPrompt); process.stdin.end()`) so long prompts don't hit arg-length limits.
3. Return a `CliHandle` wrapping the child process: attach listeners to `.stdout`, `.stderr`, `.on("exit", ...)`; implement `sendSigterm` via `kill("SIGTERM")` and `sendSigkill` via `kill("SIGKILL")`; `waitExit` resolves via a promise that listens to `exit`.
4. Clean up tmpfiles in `waitExit` (or in a `finally` in the supervisor).
5. Export `createClaudeCliRunner(opts?: { binaryPath?: string }): CliRunner`.

**Verification:** No unit test — this is I/O. Exercised indirectly via scenario tests using `FakeCliRunner`.

---

### Task 1.3: `FakeCliRunner` — scripted simulator for tests

**Files:** `rimsky/test/fakes/fake-cli-runner.ts`

**Steps:**

1. Write:
   ```ts
   import { CliRunner, CliSpawnRequest, CliHandle } from "../../src/supervisor/cli-runner.js";

   export type FakeScript = (handle: FakeCliHandle, env: Record<string, string>) => Promise<void>;

   export class FakeCliHandle implements CliHandle {
     private stdoutCbs: ((c: string) => void)[] = [];
     private stderrCbs: ((c: string) => void)[] = [];
     private exitCbs: ((code: number | null, signal: NodeJS.Signals | null) => void)[] = [];
     private exited = false;
     private exitResult: { exitCode: number | null; signal: NodeJS.Signals | null } | null = null;
     private exitWaiters: Array<(r: typeof this.exitResult) => void> = [];

     onStdout(cb: (c: string) => void) { this.stdoutCbs.push(cb); }
     onStderr(cb: (c: string) => void) { this.stderrCbs.push(cb); }
     onExit(cb: (code: number | null, signal: NodeJS.Signals | null) => void) { this.exitCbs.push(cb); }
     sendSigterm() { this.exit(0, "SIGTERM"); }
     sendSigkill() { this.exit(null, "SIGKILL"); }

     /** Simulated API for test scripts */
     emitStdout(s: string) { this.stdoutCbs.forEach((cb) => cb(s)); }
     emitStderr(s: string) { this.stderrCbs.forEach((cb) => cb(s)); }
     exit(code: number | null, signal: NodeJS.Signals | null = null) {
       if (this.exited) return;
       this.exited = true;
       this.exitResult = { exitCode: code, signal };
       this.exitCbs.forEach((cb) => cb(code, signal));
       this.exitWaiters.forEach((w) => w(this.exitResult));
     }
     waitExit(): Promise<{ exitCode: number | null; signal: NodeJS.Signals | null }> {
       if (this.exited) return Promise.resolve(this.exitResult!);
       return new Promise((resolve) => { this.exitWaiters.push(resolve); });
     }
   }

   export class FakeCliRunner implements CliRunner {
     private script: FakeScript;
     private handles: FakeCliHandle[] = [];

     constructor(script: FakeScript) { this.script = script; }

     async spawn(req: CliSpawnRequest): Promise<CliHandle> {
       const h = new FakeCliHandle();
       this.handles.push(h);
       // Run the script asynchronously; it drives the handle.
       this.script(h, req.env).catch((e) => h.emitStderr(`fake script failed: ${e}`));
       return h;
     }

     handleFor(index: number): FakeCliHandle | undefined {
       return this.handles[index];
     }
   }
   ```

2. Write `cli-runner_test.ts` verifying `FakeCliRunner` emits stdout callbacks, fires exit, and `waitExit` resolves.

**Verification:** Unit tests pass.

---

## Phase 2: Callback MCP server

### Task 2.1: `src/callback-mcp/token-registry.ts`

**Steps:**

1. Write:
   ```ts
   import { UUID } from "../shared/types.js";
   import { CellTemplateSpec } from "../cell/template.js";

   export interface TokenEntry {
     cell_id: UUID;
     result_schema: unknown;              // JSON schema for report_complete validation
     onComplete: (result: unknown, changed: boolean, change_summary: string | null) => Promise<{ status: "accepted" } | { status: "rejected"; errors: Record<string, string[]> }>;
     onBlocked: (reason: string, context: unknown) => Promise<void>;
     onError: (error_class: string, payload: unknown) => Promise<void>;
   }

   export class TokenRegistry {
     private readonly map = new Map<string, TokenEntry>();
     register(token: string, entry: TokenEntry): void { this.map.set(token, entry); }
     lookup(token: string): TokenEntry | undefined { return this.map.get(token); }
     release(token: string): void { this.map.delete(token); }
   }
   ```

2. No test file (trivial); exercised by `server_test.ts`.

**Verification:** `npx tsc --noEmit` passes.

---

### Task 2.2: `src/callback-mcp/tools.ts`

**Goal:** Tool schemas and handler glue. Uses a lightweight HTTP+JSON-RPC convention rather than the full MCP SDK (to keep the dependency footprint small). A real MCP client (Claude CLI) speaks MCP over HTTP with a specific request/response shape; this implementation follows that shape.

**Steps:**

1. Define:
   ```ts
   import { z } from "zod";

   export const ReportCompleteInput = z.object({
     token: z.string(),
     result: z.unknown(),
     changed: z.boolean(),
     change_summary: z.string().nullable().optional(),
   });

   export const ReportBlockedInput = z.object({
     token: z.string(),
     reason: z.string(),
     context: z.unknown().optional(),
   });

   export const ReportErrorInput = z.object({
     token: z.string(),
     error_class: z.string(),
     payload: z.unknown().optional(),
   });

   export const TOOL_DEFINITIONS = [
     {
       name: "report_complete",
       description: "Report successful completion with a structured result matching the cell's result_schema.",
       inputSchema: ReportCompleteInput,
     },
     {
       name: "report_blocked",
       description: "Report that work cannot continue. The supervisor treats this as cell error 'agent_blocked'.",
       inputSchema: ReportBlockedInput,
     },
     {
       name: "report_error",
       description: "Report a specific structured failure matching one of the cell's declared error classes.",
       inputSchema: ReportErrorInput,
     },
   ];
   ```

2. Export helpers to validate bodies and to build MCP JSON-RPC responses (`{jsonrpc: "2.0", id, result}` or `{jsonrpc: "2.0", id, error: {code, message}}`).

**Verification:** Unit test on `tools_test.ts`:
- `ReportCompleteInput.parse({...valid...})` passes.
- Invalid input raises zod error.

---

### Task 2.3: `src/callback-mcp/server.ts` — the multi-tenant server

**Important transport choice.** Use the `@modelcontextprotocol/sdk` Node package as the server. The real Claude CLI is an MCP client that speaks Streamable-HTTP (JSON-RPC + SSE), and rolling our own HTTP shim risks silent incompatibility. Before starting Task 2.3, add `@modelcontextprotocol/sdk` to `rimsky/package.json` dependencies and verify the existing zonebase MCP server (see `/backend/src/entrypoints/mcp-server.ts` in this repo for reference) uses the same package — we align with their pattern.

**Pre-flight verification command** (run before implementing):
```bash
cd rimsky
npm install @modelcontextprotocol/sdk
npx tsx -e 'import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js"; console.log(typeof McpServer);'
```
If this prints `function`, the SDK is usable. If the import path has changed, consult the SDK README and update Task 2.3 accordingly.

**Steps:**

1. Implement using the MCP SDK:
   ```ts
   import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
   import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
   import http from "node:http";
   import { TokenRegistry } from "./token-registry.js";
   import { ReportCompleteInput, ReportBlockedInput, ReportErrorInput } from "./tools.js";

   export interface CallbackServerHandle {
     readonly host: string;
     readonly port: number;
     readonly url: string;              // http://{host}:{port}/mcp
     readonly registry: TokenRegistry;
     /**
      * Schedule teardown of the subprocess associated with `token`,
      * but only AFTER the current MCP response has been fully flushed.
      * Callers from inside a tool handler should use this instead of
      * killing the subprocess directly — killing during `await` inside
      * the handler risks truncating the HTTP/SSE response before the
      * client reads "accepted".
      */
     scheduleTeardownAfterResponse(token: string, teardown: () => Promise<void>): void;
     close(): Promise<void>;
   }

   export async function startCallbackServer(opts: {
     host?: string;
     port?: number;
     logger: Logger;
   }): Promise<CallbackServerHandle> {
     const registry = new TokenRegistry();
     const pendingTeardowns = new Map<string, () => Promise<void>>();

     const server = new McpServer({ name: "rimsky-callback", version: "1.0.0" });

     // Register the three tools. Handler bodies validate the token, look up
     // the entry, invoke the appropriate callback, and return a result.
     // Tool handlers are async functions that return the `result` field of
     // the MCP response. The SDK takes care of response serialization and
     // (for Streamable-HTTP) closing the response stream after the result
     // is sent.
     server.tool(
       "report_complete",
       "Report successful completion with a structured result.",
       ReportCompleteInput.shape,
       async ({ token, result, changed, change_summary }) => {
         const entry = registry.lookup(token);
         if (!entry) return { content: [{ type: "text", text: "unknown_token" }], isError: true };
         const outcome = await entry.onComplete(result, changed, change_summary ?? null);
         if (outcome.status === "accepted") {
           // Register a teardown to fire AFTER this response is flushed.
           // The SDK's transport closes the HTTP/SSE stream once the tool
           // returns; we enqueue teardown here, and the HTTP server hook
           // below runs it on response-finished.
           const td = pendingTeardowns.get(token);
           if (td) {
             // already scheduled; no-op
           }
         }
         return { content: [{ type: "text", text: JSON.stringify(outcome) }], structuredContent: outcome };
       }
     );
     // Analogous registrations for report_blocked and report_error.

     // Streamable-HTTP transport + raw HTTP server. We attach an 'onResponseFinished'
     // hook that runs pending teardowns AFTER the client has received the response.
     const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
     await server.connect(transport);

     const httpServer = http.createServer(async (req, res) => {
       if (!req.url?.startsWith("/mcp")) {
         res.statusCode = 404;
         res.end("not found");
         return;
       }
       // Hook response-finished to run any teardowns queued during this request.
       const tokensToTeardown: string[] = [];
       res.on("finish", () => {
         void Promise.allSettled(
           tokensToTeardown.map((t) => {
             const td = pendingTeardowns.get(t);
             pendingTeardowns.delete(t);
             return td ? td() : Promise.resolve();
           })
         );
       });
       // Attach the token-collector to the request context so the tool handler
       // can add tokens. For SDK v1.x this is done via a request-scoped context;
       // alternatively, the handler calls scheduleTeardownAfterResponse which
       // stages into pendingTeardowns and tokensToTeardown via a closure.
       await transport.handleRequest(req, res, undefined);
     });

     const host = opts.host ?? "127.0.0.1";
     const port = opts.port ?? 0;
     await new Promise<void>((r, j) => httpServer.listen(port, host, () => r()).on("error", j));
     const actualPort = (httpServer.address() as any).port;

     return {
       host, port: actualPort, url: `http://${host}:${actualPort}/mcp`, registry,
       scheduleTeardownAfterResponse: (token, teardown) => {
         pendingTeardowns.set(token, teardown);
       },
       close: async () => {
         await new Promise<void>((r) => httpServer.close(() => r()));
       },
     };
   }
   ```

2. The critical invariant: **teardown runs after the HTTP response has finished**. The implementation uses `pendingTeardowns` keyed by token, collected during tool handler execution, and fired in the `res.on("finish", ...)` event. This guarantees the agent's CLI reads `"accepted"` in the MCP response before the subprocess is killed.

3. Tool handler callbacks (`onComplete`, `onBlocked`, `onError`) receive a `scheduleTeardown(() => Promise<void>)` helper as part of the `TokenEntry`. They call `scheduleTeardown(async () => { handle.sendSigterm(); await grace; handle.sendSigkill(); await handle.waitExit(); })` to register teardown; the server fires it post-response.

4. `TokenEntry` from Task 2.1 updated to:
   ```ts
   export interface TokenEntry {
     cell_id: UUID;
     result_schema: unknown;
     onComplete: (result: unknown, changed: boolean, change_summary: string | null, scheduleTeardown: (td: () => Promise<void>) => void) => Promise<{ status: "accepted" } | { status: "rejected"; errors: Record<string, string[]> }>;
     onBlocked: (reason: string, context: unknown, scheduleTeardown: (td: () => Promise<void>) => void) => Promise<void>;
     onError: (error_class: string, payload: unknown, scheduleTeardown: (td: () => Promise<void>) => void) => Promise<void>;
   }
   ```

5. Write `server_test.ts` using an MCP client (from `@modelcontextprotocol/sdk/client/...`) or a minimal HTTP test helper that follows the SDK's Streamable-HTTP contract:
   - Start server on port 0.
   - List tools → three returned.
   - Call `report_complete` with a registered token → `onComplete` fires; response matches; `scheduleTeardown` callback fires *after* response complete (use a flag + delay in the test to verify ordering).
   - Call with unknown token → isError response; no teardown fired.
   - Shut down cleanly.

**Verification:** Server tests pass. `npx tsc --noEmit` passes.

---

## Phase 3: Agentic runner

### Task 3.1: `src/supervisor/agentic-runner.ts`

**Goal:** End-to-end flow for agentic cells per spec §9.4.

**Steps:**

1. Interface:
   ```ts
   export async function runAgenticCell(opts: {
     storage: StorageBackend;
     queue: DispatchQueue;
     clock: Clock;
     logger: Logger;
     cell_id: UUID;
     supervisor_id: string;
     cliRunner: CliRunner;
     callback: CallbackServerHandle;
     silenceTimeoutMs: number;
   }): Promise<void>;
   ```

2. Behavior:
   1. Load cell + instance + template spec. Find execution config (agentic): model, system_prompt, user_prompt_template, tools, result_schema.
   2. Resolve deps_data (same as deterministic).
   3. Render system prompt + user prompt against `{source: instance_params, params: cell_params, deps, resources}`.
   4. Generate a fresh `callback_token` (UUID).
   5. Build a promise that resolves when the agent terminates cleanly (`report_complete` accepted → reap subprocess) or rejects on an error condition. Create callback entry on the token registry. Each callback receives a `scheduleTeardown(() => Promise<void>)` helper from the callback server, used to register a post-response teardown:
      - `onComplete(result, changed, change_summary, scheduleTeardown)`:
        - Validate `result` against `result_schema` (use `ajv` with `ajv-formats` — add `ajv` dependency). If invalid, log a `work_rejected` event with `{reason: "schema_validation_failed", errors}` and return `{ status: "rejected", errors: <field errors> }`. **Do not call scheduleTeardown.** Subprocess continues and can retry.
        - If valid, call `scheduleTeardown(async () => { handle.sendSigterm(); await cfg.clock.sleep(5000); handle.sendSigkill(); await handle.waitExit(); })`. Return `{ status: "accepted" }`. The callback server fires the teardown after the HTTP response has flushed. The outer promise resolves with `{kind: "complete", result, changed, change_summary}` inside an `onExit`-driven listener (step 9).
      - `onBlocked(reason, context, scheduleTeardown)`: call `scheduleTeardown` as above; the outer-promise resolution to `{kind: "blocked", reason, context}` happens in the `onExit` listener.
      - `onError(error_class, payload, scheduleTeardown)`: analogous for `{kind: "error", error_class, payload}`.
   6. Register callback under the token. Spawn CLI:
      ```ts
      const handle = await cliRunner.spawn({
        model, systemPrompt, userPrompt,
        tools: [{ kind: "mcp-http", name: "rimsky-callback", url: callback.url }, ...toolsFromTemplate],
        env: {
          RIMSKY_CALLBACK_URL: callback.url,
          RIMSKY_CALLBACK_TOKEN: callbackToken,
        },
      });
      ```
   7. Track `last_stdout_chunk_at = clock.now()`. Attach `handle.onStdout((chunk) => { last_stdout_chunk_at = clock.now(); /* optionally log */; })`.
   8. Set up silence detection: an interval timer that checks `clock.now() - last_stdout_chunk_at > silenceTimeoutMs`. If triggered AND the terminal-outcome promise hasn't resolved: first append a `silence_detected` event (payload `{supervisor_id, silence_duration_ms}` per spec §3.2), then call `handle.sendSigterm()`, wait grace, `sendSigkill()`, reject outer promise with `{kind: "silence_timeout"}`. Cancel the interval on exit.
   9. Also watch for `handle.onExit(...)` BEFORE a terminal callback was received → reject outer promise with `{kind: "subprocess_exit_before_complete", exitCode, signal}`.
   10. Transition cell to `running`, set `assigned_supervisor_id`, log `work_started`.
   11. Await the outer promise. On resolution, delegate to a shared helper `applyTerminalOutcome` so the same logic as `runDeterministicCell`'s post-handler branch is not duplicated here. Extract this helper now: create `rimsky/src/supervisor/terminal-outcome.ts` with:
       ```ts
       export async function applyTerminalOutcome(opts: {
         storage: StorageBackend;
         queue: DispatchQueue;
         clock: Clock;
         logger: Logger;
         cell_id: UUID;
         outcome:
           | { kind: "run_succeeded"; result: unknown; changed: boolean; change_summary: string | null }
           | { kind: "app_error"; error_class: string; payload?: unknown }
           | { kind: "infra_error"; error_class: string; payload?: unknown }
         ;
       }): Promise<void>;
       ```
       `run_succeeded` runs `commitCellOutcome` + recalculate cascade + transition to `fresh` (or triggers `handleCellError` on `quality_failed`). `app_error` calls `handleCellError`. `infra_error` runs the infra-reenqueue path from Plan B Task 3.1 step 3. Both `runDeterministicCell` (amend Plan B to call this helper) and `runAgenticCell` use it.
   12. Per the outer-promise resolution:
       - `complete` → `applyTerminalOutcome({kind: "run_succeeded", result, changed, change_summary})`
       - `blocked` → `applyTerminalOutcome({kind: "app_error", error_class: "agent_blocked", payload: {reason, context}})`
       - `error` → `applyTerminalOutcome({kind: "app_error", error_class, payload})`
       - `silence_timeout` → `applyTerminalOutcome({kind: "infra_error", error_class: "silence_timeout"})`
       - `subprocess_exit_before_complete` → `applyTerminalOutcome({kind: "infra_error", error_class: "subprocess_exit_before_complete", payload: {exitCode, signal}})`
   13. Always release the token from the registry in a `finally`.

   **Note on Plan B amendment:** extracting `applyTerminalOutcome` is a Plan C addition, but `runDeterministicCell` (Plan B Task 4.1) should be updated to call it, replacing the inline commit/error branching. Plan B's tests should continue to pass since the logic is identical — only the call site changes.

3. Write `agentic-runner_test.ts` with the fake CLI runner, in-process callback server, real storage:
   - Happy path: fake script emits stdout, then POSTs `report_complete` to the callback URL with valid args → commit; cell `fresh`.
   - Invalid result: fake script POSTs `report_complete` with malformed result → MCP returns `rejected` with field errors; fake script continues, then POSTs valid result; commit succeeds.
   - Silence: fake script never emits stdout for > `silenceTimeoutMs` → supervisor kills; cell re-enqueued as infra error.
   - Exit before complete: fake script exits with code 1 → infra error.
   - Agent blocked: fake script POSTs `report_blocked` → cell hits policy chain with `agent_blocked` class.
   - Agent error: fake script POSTs `report_error` with declared class → policy chain invoked.

**Verification:** Tests pass.

---

## Phase 4: Supervisor config extension

### Task 4.1: Extend `SupervisorConfig` for agentic

**Files:** `rimsky/src/supervisor/types.ts`

**Steps:**

1. Add:
   ```ts
   export interface CallbackConfig {
     host?: string;                     // default "127.0.0.1"
     port?: number;                     // default 0 (OS-assigned)
   }

   export interface SupervisorConfig {
     // ... existing fields
     cliRunner?: CliRunner;             // required if accepts includes "agentic"
     callback?: CallbackConfig;         // required if accepts includes "agentic"
     silenceTimeoutMs?: number;         // default 180000 (3 min)
   }
   ```

2. Update `startSupervisor` lifecycle (Plan B Task 5.1):
   - If `accepts` includes `"agentic"`:
     - Require `cfg.cliRunner` and `cfg.callback`. Throw if missing.
     - Start the callback server before the main loop: `const callback = await startCallbackServer({ host: cfg.callback.host, port: cfg.callback.port, logger: log });`
     - Register supervisor in `rimsky_supervisors` with `callback_host`, `callback_port` from the handle.
     - Pass `callback` to `runAgenticCell` calls.
     - On shutdown, close the callback server.
   - If only `"deterministic"` in accepts: no callback server (current behavior).

3. Update supervisor `register()` to include callback host/port when agentic.

**Verification:** `npx tsc --noEmit` passes. Deterministic-only supervisor tests still pass (no behavior change for the deterministic path).

---

## Phase 5: Agentic scenarios

### Task 5.1: `agentic-happy-path.yaml` + test

**Fixture:** One agentic cell with a trivial system prompt, declaring `result_schema: { type: "object", properties: { greeting: { type: "string" } }, required: ["greeting"] }`. No error classes (happy path only).

**Test:** Use FakeCliRunner scripted to: emit a stdout chunk, then POST `report_complete({token, result: {greeting: "hello"}, changed: true, change_summary: "first run"})` to the callback URL. Assert: cell reaches `fresh`; one `commit` event logged with `change_summary: "first run"`; resource version data matches the handler's return.

**Verification:** Test passes.

---

### Task 5.2: `agentic-invalid-result-retry.yaml` + test

**Fixture:** Same schema as 5.1, but plus an error type that captures `unknown_error_class` giving up immediately (so if something odd happens we fail fast in the test).

**Test:** FakeCliRunner script: POSTs `report_complete` with an object MISSING `greeting` (violates schema) → receives `{status: "rejected", errors: {...}}` in the response. Script then POSTs again with valid output → receives `{status: "accepted"}` → exits. Assert: cell reaches `fresh`; event log contains one `work_rejected` event (payload `{reason: "schema_validation_failed", errors: {...}}`) for the first attempt and one `commit` for the second. `work_rejected` is the event kind added to the spec enumeration in Task 0.4 and emitted by the supervisor's `onComplete` callback when schema validation fails (see Task 3.1 step 5).

**Verification:** Test passes.

---

### Task 5.3: `agentic-blocked.yaml` + test

**Fixture:** Cell with `error_types.agent_blocked: policy: [{action: give_up, reason_template: "agent blocked: {reason}"}]`.

**Test:** FakeCliRunner POSTs `report_blocked({token, reason: "ran out of ideas"})`. Assert: cell state = `failed`; `error` event with `action_taken: give_up` and reason matching.

**Verification:** Test passes.

---

### Task 5.4: `agentic-silence-timeout.yaml` + test

**Fixture:** Cell with no `silence_timeout` in error_types (infra errors bypass the cell's error_types).

**Test:** FakeCliRunner emits no stdout; scenario uses `silenceTimeoutMs: 200`. Advance clock past 200ms. Assert: subprocess killed; cell state back to `stale`; dispatch row re-enqueued without backoff; `silence_detected` event logged; retry counters untouched.

**Verification:** Test passes.

---

### Task 5.5: `agentic-error-class.yaml` + test

**Fixture:** Cell with `error_types.fetch_failure: policy: [{action: retry, count: 2, backoff: linear, base_delay_ms: 50}, {action: give_up}]`.

**Test:** FakeCliRunner script on first two spawns POSTs `report_error({token, error_class: "fetch_failure", payload: {url: "..."}})`. On third spawn POSTs valid `report_complete`. Assert: cell reaches `fresh`; event log shows two `error` events with `action_taken: retry`, one `commit`.

**Verification:** Test passes.

---

## Phase 6: Control API

### Task 6.1: `src/control-api/schemas.ts`

**Steps:**

1. Define zod schemas for every route request/response body per spec §10. Examples:
   ```ts
   export const DeployTemplateRequest = z.object({
     yaml: z.string(),                  // OR a parsed object; accept both
   }).or(z.object({ spec: z.unknown() }));

   export const CreateInstanceRequest = z.object({
     template_id: z.string().uuid(),
     consumer_key: z.string().min(1).max(200),
     params: z.record(z.unknown()).optional(),
   });

   export const OperatorInvalidateRequest = z.object({
     reason: z.string(),
     restore_version: z.union([z.string().uuid(), z.literal("previous")]).optional(),
   });
   ```

2. Cover all endpoints from spec §10 (templates, instances, cells, events, resources, operator overrides, health).

**Verification:** `npx tsc --noEmit` passes.

---

### Task 6.2: `src/control-api/errors.ts`

**Steps:**

1. Map rimsky errors to HTTP status codes:
   - `TemplateValidationError` → 400 (include structured issue list).
   - `TemplateNotFoundError` / `InstanceNotFoundError` / `CellNotFoundError` → 404.
   - `ConsumerKeyConflictError` / `TemplateInUseError` / `CellRunningError` → 409.
   - Anything else → 500 (log details, return a generic message).

2. Export `toHttpError(err: unknown): { status: number; body: {error: string; details?: unknown} }`.

**Verification:** Compiles.

---

### Task 6.3: `src/control-api/routes/templates.ts`

**Steps:**

1. Implement handlers for:
   - `POST /templates` → parse body (YAML or JSON); call `validateTemplate`; on success `storage.templates.deploy(spec)`; return 201 with `{template_id, name, version}`.
   - `GET /templates` → `storage.templates.list()` → 200.
   - `GET /templates/:id` → `storage.templates.get(id)` → 200 or 404.
   - `DELETE /templates/:id` → `storage.templates.delete(id)` → 204 on success, 409 with structured message if `TemplateInUseError`.

2. Write `templates_test.ts` using a real harness (testcontainers + in-process Fastify app):
   - Deploy valid YAML → 201.
   - Deploy invalid YAML (missing `name`) → 400 with issue list.
   - List → includes the deployed template.
   - Delete with no instances → 204; subsequent GET → 404.
   - Delete with an active instance → 409.

**Verification:** Tests pass.

---

### Task 6.4: `src/control-api/routes/instances.ts`

**Steps:**

1. Implement:
   - `POST /instances` → validate body; call `createInstance(storage, queue, template_id, consumer_key, params)` helper (same as Plan A test harness); return 201 with `{instance_id, consumer_key, cell_count}`. 409 on consumer_key conflict.
   - `GET /instances` → `storage.instances.list({template_id?, consumer_key?})`.
   - `GET /instances/:id_or_key` → resolve (UUID → by id; else by consumer_key with optional `template_id` query). 404 if not found.
   - `DELETE /instances/:id_or_key` → 409 if any cell is `running`; else cascading delete.

2. Write `instances_test.ts` with full round-trip: deploy template → POST instance → GET by id and by key → DELETE.

**Verification:** Tests pass.

---

### Task 6.5: `src/control-api/routes/cells.ts`

**Steps:**

1. Implement:
   - `GET /cells/:cell_id` → cell state + dependencies + error tracking + assigned supervisor (join with supervisor row if needed).
   - `GET /instances/:id_or_key/cells` → list all cells for instance.
   - `POST /cells/:cell_id/invalidate` → body `{reason, restore_version?}`; call `invalidateCell` from `src/scheduler/invalidate.ts`; log `operator_override` event; return 202.
   - `POST /cells/:cell_id/reset` → only valid if state is `failed`; clear error tracking; transition to `stale`; log `operator_override`; return 202. 409 if state is not `failed`.
   - `POST /cells/:cell_id/kill` → set `kill_requested = true` on the cell row. Return 202. No-op if cell is not `running` (idempotent).

2. Write `operator-overrides_test.ts` combining all four:
   - Invalidate a fresh cell → stale; next supervisor tick picks it up.
   - Reset a failed cell → stale + dispatch row; next supervisor tick picks it up.
   - Kill a running cell → supervisor detects `kill_requested` on next heartbeat, terminates subprocess (fake CLI), cell transitions per infra-error path.

**Verification:** Tests pass.

---

### Task 6.6: `src/control-api/routes/events.ts`

**Steps:**

1. Implement `GET /events` with query params `instance`, `cell`, `kind`, `since`, `until`, `cursor`, `limit` (default 100, max 1000). Call `storage.events.tail(cursor, limit)` or `list(filter)` depending on params. Return `{events, next_cursor}`.

2. Write tests:
   - Append N events (20) via harness; `GET /events?limit=5` returns 5 + cursor.
   - Subsequent call with cursor returns next 5.
   - Filter by cell_id → only matching events.

**Verification:** Tests pass.

---

### Task 6.7: `src/control-api/routes/resources.ts`

**Steps:**

1. Implement:
   - `GET /resources/:id/current` → returns current version data (via `resourceData.read` or inline payload).
   - `GET /resources/:id/versions` → list of versions with metadata (no data bodies).
   - `GET /resources/:id/versions/:version_id` → specific version data.

2. Write tests creating a resource with two versions; all three endpoints return correct data.

**Verification:** Tests pass.

---

### Task 6.8: `src/control-api/routes/health.ts`

**Steps:**

1. Implement `GET /health`:
   - Returns `{status: "ok" | "degraded", scheduler: {last_tick_at, running_cells}, supervisors: [{id, accepts, concurrency, callback_host, callback_port, active_cell_count, last_heartbeat_at}]}`.
   - `status: "degraded"` if any supervisor's last heartbeat is older than a threshold (e.g., 30s).
   - Scheduler metadata: for v1, just report whether any scheduler process has registered itself (we don't have a scheduler registry in Plan A's schema — so v1 health just reports supervisors + a cell-state histogram). Keep the scheduler field optional or return `{registered: false}` if no data.

2. Write tests:
   - No supervisors registered → `status: "ok", supervisors: []`.
   - One supervisor active → appears in the list.
   - Supervisor heartbeat stale → `status: "degraded"`.

**Verification:** Tests pass.

---

### Task 6.9: `src/control-api/app.ts` + `src/config/control-api.ts`

**Steps:**

1. `app.ts` — Fastify app factory:
   ```ts
   export function createControlApiApp(deps: { storage: StorageBackend; queue: DispatchQueue; clock: Clock; logger: Logger; authenticate?: (req: any) => Promise<unknown>; }): FastifyInstance {
     const app = fastify({ logger: false });
     if (deps.authenticate) {
       app.addHook("preHandler", async (req, reply) => { /* run auth hook */ });
     }
     app.register(templatesRoutes, deps);
     app.register(instancesRoutes, deps);
     app.register(cellsRoutes, deps);
     app.register(eventsRoutes, deps);
     app.register(resourcesRoutes, deps);
     app.register(healthRoutes, deps);
     app.setErrorHandler((err, req, reply) => {
       const { status, body } = toHttpError(err);
       return reply.status(status).send(body);
     });
     return app;
   }
   ```

2. `src/config/control-api.ts`:
   ```ts
   import { FastifyRequest } from "fastify";

   export interface AuthContext {
     subject: string;
     claims?: Record<string, unknown>;
   }

   export interface ControlApiConfig {
     storage: StorageBackend;
     queue: DispatchQueue;
     clock: Clock;
     logger: Logger;
     host: string;                      // default "127.0.0.1"
     port: number;
     authenticate?: (req: FastifyRequest) => Promise<AuthContext | null>;
   }

   export interface ControlApiHandle {
     readonly host: string;
     readonly port: number;
     shutdown(): Promise<void>;
   }

   export async function startControlApi(cfg: ControlApiConfig): Promise<ControlApiHandle> {
     const app = createControlApiApp(cfg);
     const host = cfg.host ?? "127.0.0.1";
     await app.listen({ host, port: cfg.port });
     const actualPort = (app.server.address() as any).port;
     return {
       host, port: actualPort,
       shutdown: async () => { await app.close(); },
     };
   }
   ```

3. Add `startControlApi` to `src/index.ts` exports.

**Verification:** `npx tsc --noEmit` passes. Tests in 6.3–6.8 pass.

---

### Task 6.10: End-to-end integration test

**Files:** `rimsky/test/scenarios/end-to-end_test.ts`

**Steps:**

1. Spin up harness + scheduler + supervisor (deterministic) + control API.

2. Via HTTP:
   - POST `/templates` with a minimal deterministic template.
   - POST `/instances` with a consumer_key.
   - GET `/instances/:id_or_key` — returns the instance with its cells.
   - Register the handler via the harness (supervisor is already running).
   - Poll `GET /cells/:cell_id` until `state: "fresh"`.
   - GET `/events?instance=...` — verify the event log shows the full lifecycle.
   - GET `/resources/:id/current` — verify the committed data.
   - DELETE `/instances/:id_or_key` → 204.

**Verification:** Test passes.

---

## Phase 7: Reference binaries

### Task 7.1: `src/entrypoints/scheduler.ts`

**Steps:**

1. Write:
   ```ts
   import pg from "pg";
   import { createPostgresStorage } from "../storage/postgres/backend.js";
   import { PostgresDispatchQueue } from "../queue/postgres-queue.js";
   import { SystemClock } from "../shared/clock.js";
   import { createPinoLogger } from "../shared/logger.js";
   import { startScheduler } from "../config/scheduler.js";

   function readEnv(name: string, required: boolean = true, def?: string): string {
     const v = process.env[name] ?? def;
     if (!v && required) {
       console.error(`Missing required env var: ${name}`);
       process.exit(1);
     }
     return v ?? "";
   }

   async function main() {
     const dbUrl = readEnv("RIMSKY_DB_URL");
     const tickIntervalMs = Number(readEnv("RIMSKY_SCHEDULER_TICK_MS", false, "1500"));
     const heartbeatTimeoutMs = Number(readEnv("RIMSKY_SCHEDULER_HEARTBEAT_TIMEOUT_MS", false, "15000"));
     const concurrencyLimitsJson = readEnv("RIMSKY_CONCURRENCY_LIMITS", false, "{}");
     const logLevel = readEnv("RIMSKY_LOG_LEVEL", false, "info");

     const pool = new pg.Pool({ connectionString: dbUrl });
     const clock = SystemClock;
     const logger = createPinoLogger({ level: logLevel });
     const storage = createPostgresStorage({ pool, clock, logger });
     const queue = new PostgresDispatchQueue(pool);

     const handle = startScheduler({
       storage, queue, clock, logger,
       tickIntervalMs, heartbeatTimeoutMs,
       concurrencyLimits: JSON.parse(concurrencyLimitsJson),
     });

     const shutdown = async () => {
       await handle.shutdown();
       await pool.end();
       process.exit(0);
     };
     process.on("SIGTERM", shutdown);
     process.on("SIGINT", shutdown);
   }

   main().catch((e) => { console.error(e); process.exit(1); });
   ```

**Verification:** With a real Postgres + applied migrations, `RIMSKY_DB_URL=... npx tsx src/entrypoints/scheduler.ts` starts without error and logs scheduler ticks.

---

### Task 7.2: `src/entrypoints/supervisor.ts`

**Steps:**

1. Analogous to 7.1, reading:
   - `RIMSKY_DB_URL`
   - `RIMSKY_SUPERVISOR_ID` (default: `os.hostname() + "-" + process.pid`)
   - `RIMSKY_SUPERVISOR_ACCEPTS` (csv; default `"deterministic"`)
   - `RIMSKY_SUPERVISOR_CONCURRENCY` (default `1`)
   - `RIMSKY_SUPERVISOR_HEARTBEAT_MS` (default `5000`)
   - `RIMSKY_SUPERVISOR_CLAIM_POLL_MS` (default `1000`)
   - `RIMSKY_SUPERVISOR_SILENCE_TIMEOUT_MS` (default `180000`)
   - `RIMSKY_SUPERVISOR_CALLBACK_HOST` (default `127.0.0.1`)
   - `RIMSKY_SUPERVISOR_CALLBACK_PORT` (default `0`)
   - `RIMSKY_CONCURRENCY_LIMITS` (same as scheduler)
   - `RIMSKY_LOG_LEVEL` (default `"info"`)

2. Construct `SupervisorConfig`. If `accepts` includes `"agentic"`, instantiate `createClaudeCliRunner()`. For deterministic acceptance, the reference binary loads handlers from a user-supplied module specified by the `RIMSKY_HANDLERS_MODULE` env var:

   - **Module path resolution.** Treated as a standard Node ESM import specifier, resolved from the current working directory. Can be:
     - A relative path (`./my-handlers.js`), resolved relative to `process.cwd()`.
     - A package name (`@my-org/rimsky-handlers`), resolved via the usual node_modules lookup from `process.cwd()`.
     - An absolute path (`/app/handlers/index.js`).
   - **Module contract.** The module must default-export or named-export a function `register(registry: HandlerRegistry): void | Promise<void>`. The binary calls it once after constructing the registry. Example consumer module:
     ```ts
     // my-handlers.js
     export async function register(registry) {
       registry.register("generate-config", async (ctx) => { /* ... */ });
       registry.register("test-adapter", async (ctx) => { /* ... */ });
     }
     ```
   - **Docker image guidance.** The module file (and its dependencies) must be present in the image at the path or package name specified. Consumers typically build an image that layers their handler module on top of the `rimsky` base image, then set `RIMSKY_HANDLERS_MODULE=./handlers.js`.
   - **Missing module.** If `accepts` includes `"deterministic"` and `RIMSKY_HANDLERS_MODULE` is unset, the binary logs a clear warning at startup ("no handlers registered; deterministic cells will give_up with unknown_handler") and proceeds. This is a legitimate configuration for a supervisor that only accepts agentic work.
   - **Errors during load.** If the module exists but import/`register()` throws, the binary exits with a clear error before entering the supervisor loop.

3. The env-var binaries are reference examples for simple deployments. Production deployments usually import rimsky as a library and build `SupervisorConfig` programmatically (passing a pre-populated `HandlerRegistry` directly), per the library-first design decision in the brainstorm.

**Verification:** Binary starts with valid env vars; throws clear error with missing ones.

---

### Task 7.3: `src/entrypoints/control-api.ts`

**Steps:**

1. Analogous to 7.1. Env vars:
   - `RIMSKY_DB_URL`
   - `RIMSKY_CONTROL_API_HOST` (default `127.0.0.1`)
   - `RIMSKY_CONTROL_API_PORT` (default `3100`)
   - `RIMSKY_LOG_LEVEL`

2. No authentication in the reference binary (per spec §11.2).

**Verification:** Binary starts and responds to `GET /health`.

---

### Task 7.4: `package.json` bin entries

**Steps:**

1. Update `rimsky/package.json`:
   ```json
   "bin": {
     "rimsky-scheduler": "./dist/entrypoints/scheduler.js",
     "rimsky-supervisor": "./dist/entrypoints/supervisor.js",
     "rimsky-control-api": "./dist/entrypoints/control-api.js"
   }
   ```

2. `npm run build` produces these files.

**Verification:** After `npm run build`, `node dist/entrypoints/scheduler.js` is runnable.

---

## Phase 8: Definition of done

### Task 8.1: Final verification

**Steps:**

1. `cd rimsky && npm run build` → 0.

2. `npm test` → all tests pass (Plan A + Plan B + Plan C scenarios all green).

3. `npm run lint` → no errors.

4. `src/index.ts` exports: Plan A + Plan B + `startControlApi`, `startCallbackServer`, `createClaudeCliRunner`, relevant types.

5. Plan C's three env-var binaries build and run against a live Postgres:
   - `rimsky-scheduler` starts, ticks, shuts down on SIGTERM.
   - `rimsky-supervisor` with `RIMSKY_SUPERVISOR_ACCEPTS=deterministic` claims and runs cells.
   - `rimsky-control-api` answers `GET /health`.

6. Update `CHANGELOG.md` (repo root):
   ```
   - Added rimsky agentic execution (Claude CLI subprocess with callback MCP), HTTP control API (template CRUD, instance management, cell reads, operator overrides, events, resources, health), and env-var reference binaries (rimsky-scheduler, rimsky-supervisor, rimsky-control-api). See /rimsky/ and docs/plans/2026-04-21-rimsky-plan-c-agentic-control-api.md.
   ```

7. Update `feature-index.md` (or create if missing) with an entry pointing at the rimsky module and noting its boundary (no imports from /backend/).

**Deliverables:**
- v1 rimsky platform is deployable and feature-complete per the spec.
- Zonebase ingestion can begin building against rimsky as a library consumer.
- Open-source extraction is a matter of copying `/rimsky/` out, renaming the package, and publishing.

---

## Notes for the implementer

- **Agentic cells' `cell_params` and `instance_params`** are rendered into prompt templates; the rendering engine is a simple handlebars-style `{{source.x}}`, `{{params.y}}`, `{{deps.z}}` substitution. Don't bring in a heavyweight template engine — a regex-based substitution over a flat namespace is enough for v1.
- **The callback MCP server binds to localhost by default**. Do not expose it externally; the token model is intra-supervisor only.
- **FakeCliRunner** scripts receive the env map which includes `RIMSKY_CALLBACK_URL` and `RIMSKY_CALLBACK_TOKEN`. Tests use these to POST to the callback server via the test's `mcp-client.ts` helper.
- **The control API is read-mostly-write-rarely**. All mutation endpoints go through the storage interface; no direct SQL in the route handlers.
- **Zero auth in v1**. Production consumers must wrap the library's `startControlApi` with their own auth hook (see spec §10 and §11.1).
- **Never commit in a plan step.** Commits are the user's call.
- **Multi-resource cells are still Plan B's single-resource simplification.** Plan C does not revisit this — the template validator continues to accept multi-resource templates for future compatibility, but the runner throws a clear error if a cell owns more than one resource. Deferred to a v1.x follow-on.
