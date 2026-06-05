# Claude-agent sign-off gate — Implementation Plan

**Spec:** `.ok-planner/specs/2026-06-04-claude-agent-signoff-gate-design.md`
**Goal:** Add a host-configured, Ed25519 sign-off gate to the claude-agent executor so `report_complete` cannot resolve to terminal success unless the agent presents valid per-path signatures from host-designated validators — plus the `cli.mcp_servers` wiring the gate depends on — all inside `lib/services/executors/claude-agent/`, with no rimsky-core changes.
**Architecture:** A new pure verification module (`signoff.ts`) does Ed25519 verification over a domain-separated, dispatch-bound, RFC-8785-canonical message. New `attributes.cli` fields (`mcp_servers`, `required_signoffs`, `max_signoff_attempts`) ride the existing opaque attribute channel; the executor parses them, wires host MCP servers into the spawned CLI, and verifies signatures in the `report_complete` (`onComplete`) path after the existing schema check, looping the agent for correction and terminal-erroring with `agent/signoff_unobtained` on exhaustion.
**Tech Stack:** TypeScript (Node 20, ESM), `@modelcontextprotocol/sdk`, `node:crypto` (Ed25519 — built in, no new crypto dep), `canonicalize` (new dep, RFC 8785), `ajv`, `zod`, `vitest`.

All paths below are relative to `lib/services/executors/claude-agent/` unless absolute. Run all commands from that directory.

---

## Context the implementer needs (current shapes)

You are joining cold. These are the real current shapes you will edit; read each file before editing it.

- **`src/agent-run.ts`** — `runAgent(opts: AgentRunOptions)` is the executor core.
  - `AgentRunOptions` (around line 90+) has `attributesSchema`, `attributes`, and an optional `cliConfig` object whose current fields are `bare`, `permissionMode`, `allowedTools`, `disallowedTools`, `addDirs`, `maxBudgetUsd`, `handleRateLimits`, `maxSchemaCorrections`. It does **not** carry the raw `dispatch_id`.
  - The user prompt gets a fixed `---`-delimited metadata footer appended around lines 347–354 (`renderedUser = userPrompt + "\n\n---\n" + "callback_token: ..." + ...`). The system prompt is left clean for cache stability.
  - The CLI is spawned at three sites, each currently passing `tools: [{ kind: "mcp-http", name: "rimsky-callback", url: effectiveCallback.url }]`: the post-park resume (~line 666), the initial spawn (~line 680), and the exit-recovery resume (~line 892).
  - `rejectWithCorrection` (~lines 494–533) is the existing in-session schema-correction loop: it increments `schemaCorrectionFailures`, returns `{status:"rejected", errors}` to the agent until `maxSchemaCorrections` (default 3, ~lines 488–491) is exceeded, then `scheduleTeardown` resolves an `errored` outcome with `errorClass: "agent/schema_violation"` and returns `{status:"accepted"}`.
  - The `onComplete` registry callback (~lines 554–606) validates `{...attributes, ...attributesDelta}` against the compiled `validateAttributes` (~line 582), then on success resets the counter and `scheduleTeardown` → resolves `{kind:"complete", attributesDelta, changed, changeSummary}`.
- **`src/token-registry.ts`** — `TokenEntry.onComplete` is typed `(attributesDelta, changed, changeSummary, scheduleTeardown) => Promise<{status:"accepted"} | {status:"rejected"; errors}>` (lines 45–53).
- **`src/internal-mcp-server.ts`** — the **live** `report_complete` MCP handler (lines 334–360) declares an **inline** zod schema `{ token, attributes_delta: z.record(z.unknown()).optional(), changed, change_summary }` and forwards exactly those to `entry.onComplete(...)`. It does **not** import `ReportCompleteInput`.
- **`src/internal-mcp-tools.ts`** — `ReportCompleteInput` (zod, lines 33–43) and `TOOL_DEFINITIONS[0]` (`report_complete`, `inputSchema`, lines 89+) are the tool-*definition* surface, consumed by tests and the allowed-tool-name derivation (`cli-runner.ts` reads `TOOL_DEFINITIONS.map(t => t.name)` only — never the input fields).
- **`src/expected-attributes-schema.ts`** — `expectedAttributesSchema.properties.cli.properties` (lines 61–80) mirrors the parser's cli fields, with a lock-step comment. `declaredErrorClasses` (lines 123–135) is the `agent/*` vocabulary advertised via `Capabilities.declared_error_classes`; operator `error_types:` keys range-check against it at rimsky-side registration (`lib/graph/node/template_validator.go`).
- **`src/server.ts`** (gRPC) and **`src/http-bridge.ts`** (HTTP) — each computes `runId = dispatch_id && length>0 ? dispatch_id : randomUUID()` (server.ts ~330; http-bridge.ts ~134) and passes `runId` (not the raw `dispatch_id`) into `runAgent({...})` inside their `runAndCallback`. Both build the `runAgent` options identically (server.ts ~402; http-bridge.ts ~184), including `cliConfig: parseCliConfig(attributes.cli)`. `parseCliConfig` lives in `server.ts` (~695) and is mirrored in `http-bridge.ts` (~454, annotated `@source: src/server.ts`).
- **`src/cli-runner.ts`** — `CliToolConfig = { kind: "mcp-http"; name; url; headers? }` (59–64). `mcpConfigJson(tools)` (301) serializes any `mcp-http` entry into the `--mcp-config` `mcpServers` map. `buildAllowedTools(templateAllowed?)` (42) returns `[...REQUIRED_CALLBACK_TOOLS, ...(templateAllowed ?? [])]` deduped. `REQUIRED_CALLBACK_TOOLS` (31) is `TOOL_DEFINITIONS.map(t => \`mcp__rimsky-callback__${t.name}\`)`. `buildClaudeCliArgs` (206) emits `--mcp-config` (244) and `--allowedTools` (234). Note: `CliSpawnRequest.tools` is the per-spawn server list; `CliSpawnRequest.allowedTools` is the per-template allow list.
- **Test patterns:**
  - Unit MCP calls: `src/internal-mcp-server.test.ts` connects an MCP `Client` (`@modelcontextprotocol/sdk/client`) and `client.callTool({ name, arguments })`.
  - Real HTTP-bridge drive + callback capture: `src/http-bridge.test.ts` starts the bridge with a `fakeCli` and `postCallback: (url, body) => posts.push({url, body})`, `fetch(\`${bridge.address}/execute\`, {... body: { dispatch_id, attributes, callback_url }})`, then `waitFor(() => posts.length > 0)` and asserts `posts[0].body` (the `AsyncCallbackBody`).
  - Fake CLI handle: `src/lifecycle.e2e.test.ts::makeFakeHandle` builds a scripted `CliHandle` with a `beforeExit` hook; the fake `CliRunner.spawn(req)` receives `req.tools[0].url` (the per-dispatch MCP URL) and `req.env.RIMSKY_CALLBACK_TOKEN`.

## Load-bearing properties (must hold; each has a verification below)

- **The gate reads `required_signoffs` and the binding id from dispatch-time config, never from agent-submitted data.** Read `required` from `cliConfig.requiredSignoffs` (spawn-time) and the binding id from the raw `dispatch_id` plumbed through options — **not** from `attributesDelta` or the merged bag. Verified by the anti-tamper onComplete unit test (Task 10 step 4, case d): an `attributes_delta` carrying its own `cli.required_signoffs` override does not weaken the gate.
- **Anti-replay: signatures bind to the raw `dispatch_id`.** A signature minted for one dispatch must not verify for another. Verified by the cross-dispatch test in `signoff.test.ts` (Task 1, case e). Do **not** bind to `runId` (it falls back to a random UUID and would mask an empty `dispatch_id`); a configured gate with an empty `dispatch_id` is a usage error, not a silently-ungated run.
- **The gate guards success only.** `report_error` / `report_blocked` / `report_park` are untouched; only the `report_complete` success path is gated. Verified by the gate-configured `report_error` unit test (Task 10 step 4, case e): with `required_signoffs` set, a `report_error` call still produces a terminal `errored` outcome with its own class and is not gated.
- **Byte-exact canonicalization (RFC 8785).** The executor and any host validator must produce identical canonical bytes or honest signatures fail. Use the `canonicalize` dependency on the executor side; both the verification module and the test signer go through it. Verified by the canonicalization-equivalence assertion in `signoff.test.ts`.

---

## Pass 1: Sign-off verification module + test signer

**Goal:** A pure, fully-tested Ed25519 verification module (`signoff.ts`) and a real-crypto test signer, with no wiring into the executor yet.
**Scope:** Tasks 1–2
**End state:** working
**Verification:** `npx vitest run src/signoff.test.ts`

### Task 1 (red): add the dependency, the test signer, a stub module, and the failing spec

**Files:** `package.json`, `src/signoff.ts` (new, stub), `src/signoff-test-signer.ts` (new), `src/signoff.test.ts` (new)

**Steps:**
1. In `package.json`, add `"canonicalize": "^2.0.0"` to `dependencies` (RFC 8785 JSON canonicalization by the RFC author; tiny, zero-dependency — chosen over hand-rolling because ECMAScript number canonicalization is error-prone and the host validators in other languages will key off RFC 8785). Run `npm install`.
2. Create `src/signoff.ts` with the **real** pure helpers `SIGNOFF_DOMAIN`, `buildSignoffMessage`, `valueAtPath`, the `RequiredSignoff` / `SignoffResult` types, and a **stub** `verifyRequiredSignoffs` that compiles but is intentionally wrong (so the tree builds and the test fails on an assertion, not an import error):
   ```ts
   import { createPublicKey, verify as edVerify } from "node:crypto";
   import canonicalize from "canonicalize";

   export const SIGNOFF_DOMAIN = "rimsky/claude-agent/signoff/v1";

   export interface RequiredSignoff { publicKey: string; path?: string }
   export interface SignoffResult {
     ok: boolean;
     unmet: { path: string; reason: "missing" | "invalid" }[];
   }

   /** The exact bytes a validator signs and the executor re-derives. */
   export function buildSignoffMessage(dispatchId: string, value: unknown): Buffer {
     const canonical = canonicalize(value) ?? "null";
     return Buffer.from(`${SIGNOFF_DOMAIN}\n${dispatchId}\n${canonical}`, "utf8");
   }

   /** Dotted path into an object; undefined/empty path ⇒ the whole object. */
   export function valueAtPath(obj: unknown, path?: string): unknown {
     if (!path) return obj;
     let cur: unknown = obj;
     for (const seg of path.split(".")) {
       if (cur == null || typeof cur !== "object") return undefined;
       cur = (cur as Record<string, unknown>)[seg];
     }
     return cur;
   }

   // STUB — Task 2 replaces the body. Intentionally always-unmet so the
   // Task 1 spec fails (red) while the tree still builds.
   export function verifyRequiredSignoffs(
     required: RequiredSignoff[],
     _attributesDelta: Record<string, unknown> | null,
     _dispatchId: string,
     _signatures: string[],
   ): SignoffResult {
     return { ok: false, unmet: required.map((r) => ({ path: r.path ?? "$", reason: "missing" as const })) };
   }
   ```
3. Create `src/signoff-test-signer.ts` (real crypto; test-only helper, reused by `signoff.test.ts` and the acceptance test). It is named without a `.test.ts` suffix because it is imported by multiple test files, so it will be compiled into `dist/` — harmless (no runtime code imports it). Optionally add `"src/signoff-test-signer.ts"` to `tsconfig.json`'s `exclude` to keep it out of the build output.
   ```ts
   import { generateKeyPairSync, sign as edSign } from "node:crypto";
   import { buildSignoffMessage } from "./signoff.js";

   export interface TestSigner {
     publicKeyPem: string;
     sign(dispatchId: string, value: unknown): string; // base64
   }

   export function makeTestSigner(): TestSigner {
     const { publicKey, privateKey } = generateKeyPairSync("ed25519");
     const publicKeyPem = publicKey.export({ type: "spki", format: "pem" }).toString();
     return {
       publicKeyPem,
       sign: (dispatchId, value) =>
         edSign(null, buildSignoffMessage(dispatchId, value), privateKey).toString("base64"),
     };
   }
   ```
4. Create `src/signoff.test.ts` asserting `verifyRequiredSignoffs` behavior against the real signer. Cover, at minimum: (a) a valid signature for `path: "endpoints"` ⇒ `ok:true`; (b) no signatures ⇒ `ok:false` with `reason:"missing"`; (c) a signature over a different value ⇒ `ok:false` `reason:"invalid"`; (d) a signature from a *different* signer's key ⇒ unmet; (e) a signature bound to a different `dispatchId` (anti-replay) ⇒ unmet; (f) two required keys at two paths, both signed ⇒ `ok:true`, only one signed ⇒ unmet; (g) per-path isolation: a signature for `endpoints` does not satisfy a required entry at `summary`; (h) canonicalization equivalence: a signature produced over `{a:1,b:2}` still verifies when the submitted value is the key-reordered `{b:2,a:1}`. Example for (a):
   ```ts
   import { describe, it, expect } from "vitest";
   import { verifyRequiredSignoffs } from "./signoff.js";
   import { makeTestSigner } from "./signoff-test-signer.js";

   describe("verifyRequiredSignoffs", () => {
     it("accepts a valid per-path signature", () => {
       const signer = makeTestSigner();
       const dispatchId = "disp-1";
       const delta = { endpoints: [{ url: "x" }] };
       const sig = signer.sign(dispatchId, delta.endpoints);
       const res = verifyRequiredSignoffs(
         [{ publicKey: signer.publicKeyPem, path: "endpoints" }],
         delta, dispatchId, [sig],
       );
       expect(res.ok).toBe(true);
     });
     // ... cases (b)–(h)
   });
   ```
5. Run the spec and confirm it FAILS against the stub.

**Verification:** `! npx vitest run src/signoff.test.ts`

### Task 2 (green): implement the verification logic

**Files:** `src/signoff.ts`

**Steps:**
1. Replace the `verifyRequiredSignoffs` stub body with the real implementation: for each required entry, derive `valueAtPath(attributesDelta, req.path)`, build the message with `buildSignoffMessage(dispatchId, value)`, import the key via `createPublicKey(req.publicKey)`, and search the base64-decoded signature bag for one that `edVerify(null, message, keyObj, sigBuf)` accepts (wrap each `edVerify` in try/catch so a malformed signature for one key doesn't abort the scan). Record `{path: req.path ?? "$", reason}` for any unmet entry — `reason:"missing"` when the signature bag is empty, else `"invalid"`. Return `{ ok: unmet.length === 0, unmet }`.
2. Run the spec and confirm it PASSES.

**Verification:** `npx vitest run src/signoff.test.ts`

---

## Pass 2: Config surface — parse, schema, advertised error class, dispatch_id plumbing

**Goal:** Teach the executor to parse the new `cli` fields, advertise the new error class, type the new `cliConfig`/options fields, and thread the raw `dispatch_id` into `runAgent` — without yet enforcing anything.
**Scope:** Tasks 3–4
**End state:** working
**Verification:** `npx vitest run src/config-signoff.test.ts && npm run build`

### Task 3 (red): failing spec for the new parsed/advertised surface

**Files:** `src/server.ts`, `src/http-bridge.ts`, `src/config-signoff.test.ts` (new)

**Steps:**
1. `parseCliConfig` is currently module-private in **both** `src/server.ts` (~695) and `src/http-bridge.ts` (~454). Add `export` to **both** declarations (a non-behavioral change, needed so the test can import them and so the tree still builds with the failing assertions).
2. Create `src/config-signoff.test.ts` that imports `parseCliConfig` from `./server.js` **and** from `./http-bridge.js` and, for each, asserts that `parseCliConfig({ mcp_servers: [{ name: "v", url: "https://v/mcp" }], required_signoffs: [{ public_key: "PEM", path: "endpoints" }], max_signoff_attempts: 2 })` returns an object whose `mcpServers`, `requiredSignoffs`, and `maxSignoffAttempts` are populated with those values. Also assert `declaredErrorClasses` (from `./expected-attributes-schema.js`) includes `"agent/signoff_unobtained"`, and that `expectedAttributesSchema.properties.cli.properties` has `mcp_servers` and `required_signoffs` keys.
3. Run it; confirm it FAILS on the assertions (the parser returns the values as `undefined`; the class/schema entries are absent) — not on an import error.

**Verification:** `! npx vitest run src/config-signoff.test.ts`

### Task 4 (green): parse the fields, extend types, advertise the class, plumb dispatch_id

**Files:** `src/server.ts`, `src/http-bridge.ts`, `src/agent-run.ts`, `src/expected-attributes-schema.ts`

**Steps:**
1. In `src/server.ts::parseCliConfig`, extend the return type and parsing to add:
   - `mcpServers?: { name: string; url: string; headers?: Record<string,string>; allowedTools?: string[] }[]` parsed from `cli.mcp_servers` (validate each entry has string `name` + `url`; pass `headers`/`allowed_tools` through if present).
   - `requiredSignoffs?: { publicKey: string; path?: string }[]` parsed from `cli.required_signoffs` (map `public_key`→`publicKey`, `path`→`path`).
   - `maxSignoffAttempts?: number` from `cli.max_signoff_attempts` (reuse the existing `numberOrUndefined`).
   Keep the type-shape-only validation style of the existing parser (rimsky never inspects values).
2. Apply the **same** edit to the `parseCliConfig` mirror in `src/http-bridge.ts` (it carries `@source: src/server.ts`; the copies must stay identical). Both copies were already made `export` in Task 3.
3. In `src/agent-run.ts`, extend the `AgentRunOptions.cliConfig` inline type with the same three optional fields (`mcpServers`, `requiredSignoffs`, `maxSignoffAttempts`), and add a new top-level optional option `dispatchId?: string` (documented: "raw `ExecuteRequest.dispatch_id`; the sign-off gate binds to this and requires it non-empty — distinct from `runId`, which falls back to a random UUID").
4. In `src/server.ts::runAndCallback` and `src/http-bridge.ts::runAndCallback`, add `dispatchId: req.dispatch_id ?? ""` (server) / `dispatchId: body.dispatch_id ?? ""` (http-bridge) to the `runAgent({...})` options object.
5. In `src/expected-attributes-schema.ts`: (a) add `mcp_servers` and `required_signoffs` (and `max_signoff_attempts`) properties to the `cli` schema block, matching the lock-step comment — `mcp_servers` as an array of objects (`name`, `url` strings; `headers` object; `allowed_tools` string array), `required_signoffs` as an array of objects (`public_key` string; `path` string), `max_signoff_attempts` as an integer; (b) append `"agent/signoff_unobtained"` to `declaredErrorClasses`.
6. Run the spec + build; confirm PASS.

**Verification:** `npx vitest run src/config-signoff.test.ts && npm run build`

---

## Pass 3: Wire host MCP servers into the spawned CLI

**Goal:** Host-declared `cli.mcp_servers` reach the spawned CLI's `--mcp-config` and have all their tools auto-allowed.
**Scope:** Tasks 5–6
**End state:** working
**Verification:** `npx vitest run src/mcp-servers-wiring.test.ts && npm run build`

### Task 5 (red): failing spec that host servers reach the spawn request

**Files:** `src/mcp-servers-wiring.test.ts` (new)

**Steps:**
1. Create `src/mcp-servers-wiring.test.ts` that runs `runAgent` with a `fakeCli` capturing the `CliSpawnRequest` (follow `src/lifecycle.e2e.test.ts::makeFakeHandle`; the fake `spawn` records `req` then returns a handle that exits 0 quickly). Pass `cliConfig: { mcpServers: [{ name: "validator", url: "https://validator/mcp" }] }`. Assert the captured `req.tools` contains an `mcp-http` entry `{ name: "validator", url: "https://validator/mcp" }` **in addition to** `rimsky-callback`, and that the captured `req.allowedTools` contains `mcp__validator` (or the `mcp__validator__*` entries). Start an internal MCP server in `beforeEach` like the other e2e tests.
2. Run it; confirm it FAILS (host servers not yet wired).

**Verification:** `! npx vitest run src/mcp-servers-wiring.test.ts`

### Task 6 (green): assemble host servers into tools + allowlist

**Files:** `src/agent-run.ts`, `src/cli-runner.ts`

**Steps:**
1. In `src/agent-run.ts`, before the spawn block, build `const hostServers = (cliConfig?.mcpServers ?? []).map(s => ({ kind: "mcp-http" as const, name: s.name, url: s.url, headers: s.headers }))` and `const hostAllowed = (cliConfig?.mcpServers ?? []).flatMap(s => s.allowedTools && s.allowedTools.length > 0 ? s.allowedTools.map(t => \`mcp__${s.name}__${t}\`) : [\`mcp__${s.name}\`])` (a bare `mcp__<name>` server-prefix entry allows all of that server's tools by default — the spec's "auto-allow all"; an explicit per-server `allowed_tools` narrows it).
2. At the **initial spawn** site (~line 680), append `...hostServers` to the `tools` array, and change `allowedTools: cliConfig?.allowedTools` to a union: `allowedTools: [...(cliConfig?.allowedTools ?? []), ...hostAllowed]` (pass `undefined` when both are empty to preserve current behavior).
3. Apply the same `tools` append at the **post-park resume** site (~line 666) and the **exit-recovery resume** site (~line 892). (The resume `CliResumeRequest` may not carry `allowedTools`; check `CliResumeRequest` in `cli-runner.ts` — if it lacks an allow-list field, add `allowedTools` to it and emit it in `buildClaudeCliResumeArgs` via `buildAllowedTools`, mirroring the spawn path, so resumed dispatches can still reach host servers.)
4. Confirm `cli-runner.ts::mcpConfigJson` already serializes the appended `mcp-http` entries (it does — it maps any `mcp-http` tool); no change needed there unless step 3 required the resume allow-list addition.
5. Run the spec + build; confirm PASS.

**Verification:** `npx vitest run src/mcp-servers-wiring.test.ts && npm run build`

---

## Pass 4: Thread `signoffs` to `onComplete` and inject the binding id (no enforcement yet)

**Goal:** The `report_complete` tool accepts a `signoffs` array and forwards it to `onComplete`; the binding id (raw `dispatch_id`) is injected into the agent's prompt footer. No outcome changes yet (signoffs are accepted and ignored), so existing behavior is preserved.
**Scope:** Tasks 7–8
**End state:** working
**Verification:** `npm run build && npx vitest run`

### Task 7: add the `signoffs` field across the three definition/runtime surfaces

**Files:** `src/token-registry.ts`, `src/internal-mcp-server.ts`, `src/internal-mcp-tools.ts`

**Steps:**
1. In `src/token-registry.ts`, extend the `TokenEntry.onComplete` type signature with a trailing parameter `signoffs: string[] | null` (before `scheduleTeardown`, or after `changeSummary` and before `scheduleTeardown` — pick one and be consistent; placing it before `scheduleTeardown` keeps `scheduleTeardown` last to match the other callbacks). New signature: `(attributesDelta, changed, changeSummary, signoffs, scheduleTeardown) => Promise<...>`.
2. In `src/internal-mcp-server.ts`, in the live `report_complete` handler (lines 340–355): add `signoffs: z.array(z.string()).optional()` to the inline zod schema, and pass `args.signoffs ?? null` to `entry.onComplete(...)` in the new parameter position.
3. In `src/internal-mcp-tools.ts`: add `signoffs: z.array(z.string()).optional()` to `ReportCompleteInput`, and add a matching `signoffs` entry to `TOOL_DEFINITIONS[0].inputSchema.properties` (type array of strings) with a short description ("base64 Ed25519 sign-off signatures, when the node requires sign-offs"). Keep this consistent with the runtime schema in step 2.
4. Build to confirm the type threads (the existing `onComplete` registration in `agent-run.ts` will fail to typecheck until Task 8 adds the parameter — that's expected; do Task 8 before running the pass verification).

**Verification:** (interim) none — typecheck completes after Task 8.

### Task 8: accept the `signoffs` parameter in `onComplete` and inject `binding_id`

**Files:** `src/agent-run.ts`

**Steps:**
1. Update the `onComplete` callback registered on the token registry (~lines 554–606) to accept the new `signoffs: string[] | null` parameter in the position chosen in Task 7. For now, do not use it (no enforcement) — the existing schema-validation-then-accept logic is unchanged.
2. Inject the binding id into the prompt footer: in the `renderedUser` construction (~lines 348–354), add a line `\`binding_id: ${dispatchId ?? ""}\n\`` to the `---`-delimited footer, where `dispatchId` is the new option. Add a short comment that validators sign `domain ‖ binding_id ‖ canonical(content)` and the agent relays this id.
3. Build and run the full suite; confirm the tree builds and all existing tests still pass (this pass is additive: `signoffs` is accepted and ignored, the footer gains one line).

**Verification:** `npm run build && npx vitest run`

---

## Pass 5: Enforce the gate + end-to-end acceptance

**Goal:** `report_complete` enforces `required_signoffs`: it loops the agent for correction on unmet sign-offs and terminal-errors with `agent/signoff_unobtained` on exhaustion; correctly-signed output completes. Proven end-to-end against the real HTTP-bridge entry point with a real signer.
**Scope:** Tasks 9–10
**End state:** working (this is the acceptance pass; the plan ends green)
**Verification:** `npx vitest run src/signoff-gate.e2e.test.ts && npm run build`

### Task 9 (red): end-to-end acceptance test, failing against the un-gated executor

**Files:** `src/signoff-gate.e2e.test.ts` (new)

**Steps:**
1. Create `src/signoff-gate.e2e.test.ts` modeled on `src/http-bridge.test.ts` (real bridge + capturing `postCallback`) and `src/lifecycle.e2e.test.ts` (fake CLI). It must:
   - `makeTestSigner()` from `./signoff-test-signer.js`; choose a fixed `dispatchId` (e.g. `"acc-disp-1"`).
   - Start the HTTP bridge with a `fakeCli` and `postCallback: (url, body) => posts.push({url, body})`.
   - The `fakeCli.spawn(req)` returns a `makeFakeHandle`-style handle whose `beforeExit` connects a real MCP `Client` over `StreamableHTTPClientTransport(new URL(req.tools.find(t => t.name === "rimsky-callback")!.url))` (per `src/internal-mcp-server.test.ts`) and issues `client.callTool({ name: "report_complete", arguments: {...} })` using the token from `req.env.RIMSKY_CALLBACK_TOKEN`.
   - **Unsigned case:** the fake CLI calls `report_complete` with `{ token, changed: true, attributes_delta: { endpoints: [{url:"x"}] } }` and **no** `signoffs`, repeatedly, until the tool response is not `{status:"rejected"}` (i.e. the budget is exhausted and the run commits) — with `cli.max_signoff_attempts: 1` this is 2 calls. Then let the handle exit. POST `/execute` with body `{ dispatch_id: "acc-disp-1", attributes: { user_prompt: "go", cli: { required_signoffs: [{ public_key: signer.publicKeyPem, path: "endpoints" }], max_signoff_attempts: 1 } }, callback_url }`. Assert the captured `AsyncCallbackBody` is `{ error: { error_class: "agent/signoff_unobtained", ... } }`.
   - **Signed case:** identical, but the fake CLI computes `signer.sign("acc-disp-1", { endpoints: [{url:"x"}] })`... — wait, it must sign the value at the configured path, i.e. `signer.sign("acc-disp-1", deltaValueAt("endpoints"))` = `signer.sign("acc-disp-1", [{url:"x"}])` — and passes `signoffs: [thatSig]`. Assert the captured `AsyncCallbackBody` is `{ success: { ... } }`.
   - Use `waitFor(() => posts.length > 0, 5000)` between the `/execute` POST and the assertion.
2. Run it; confirm it FAILS (with Pass 4's plumbing but no gate, the unsigned `report_complete` is accepted → the run completes → the unsigned-case assertion that the outcome is `error{agent/signoff_unobtained}` fails).

**Verification:** `! npx vitest run src/signoff-gate.e2e.test.ts`

### Task 10 (green): implement the gate in `onComplete` + add onComplete unit coverage

**Files:** `src/agent-run.ts`, `src/agent-run.test.ts`

**Steps:**
1. Add a sign-off correction loop mirroring `rejectWithCorrection`. Near the existing `maxSchemaCorrections`/`schemaCorrectionFailures` setup (~lines 488–533), add:
   ```ts
   const maxSignoffAttempts =
     typeof cliConfig?.maxSignoffAttempts === "number" && cliConfig.maxSignoffAttempts >= 0
       ? cliConfig.maxSignoffAttempts : 3;
   let signoffFailures = 0;
   const rejectSignoff = (
     detail: string,
     scheduleTeardown: (td: () => Promise<void>) => void,
   ): { status: "accepted" } | { status: "rejected"; errors: Record<string, string[]> } => {
     signoffFailures++;
     if (signoffFailures > maxSignoffAttempts) {
       scheduleTeardown(async () => {
         await teardownCli();
         safeResolve({ kind: "errored", errorClass: "agent/signoff_unobtained",
           payload: { attempts: signoffFailures, max: maxSignoffAttempts, last_error: detail } });
       });
       return { status: "accepted" };
     }
     return { status: "rejected", errors: { signoffs: [`${detail} (signoff ${signoffFailures}/${maxSignoffAttempts})`] } };
   };
   ```
2. In `onComplete`, **after** the existing schema validation passes (after ~line 591, before the `schemaCorrectionFailures = 0` / success `scheduleTeardown`), insert the gate, reading required sign-offs from **dispatch-time config** (`cliConfig?.requiredSignoffs`) and binding to the **raw `dispatchId`** option — never from `attributesDelta`:
   ```ts
   const required = cliConfig?.requiredSignoffs ?? [];
   if (required.length > 0) {
     if (!dispatchId || dispatchId.length === 0) {
       // A configured gate with no dispatch_id cannot be bound/verified — usage error.
       scheduleTeardown(async () => {
         await teardownCli();
         safeResolve({ kind: "errored", errorClass: "agent/signoff_unobtained",
           payload: { error: "dispatch_id required for sign-off gate but was empty" } });
       });
       return { status: "accepted" };
     }
     const res = verifyRequiredSignoffs(required, attributesDelta, dispatchId, signoffs ?? []);
     if (!res.ok) {
       const detail = res.unmet.map(u => `${u.path}:${u.reason}`).join(", ");
       return rejectSignoff(`unmet sign-offs: ${detail}`, scheduleTeardown);
     }
     signoffFailures = 0;
   }
   ```
   Import `verifyRequiredSignoffs` from `./signoff.js`. State in a comment that `required` and `dispatchId` come from dispatch-time inputs so the gated agent cannot edit its own gate via `attributes_delta`.
3. Leave `report_error` / `report_blocked` / `report_park` handlers untouched (the gate guards success only).
4. Add focused `onComplete`-layer unit tests in `src/agent-run.test.ts` (drive `runAgent` with a fake CLI that hand-drives the registered callback, the existing pattern in that file): (a) missing signature ⇒ rejected then, on exhausting `max_signoff_attempts`, an `errored` outcome with `agent/signoff_unobtained`; (b) a valid signature (from `makeTestSigner`, bound to the run's `dispatchId`) ⇒ `complete`; (c) `required_signoffs` set but `dispatchId` empty ⇒ `errored` `agent/signoff_unobtained`; (d) **anti-tamper:** an `attributes_delta` that itself contains a `cli.required_signoffs` override is ignored — the gate still uses the dispatch-time `cliConfig.requiredSignoffs`, so an otherwise-unsigned delta is still rejected; (e) **gate guards success only:** with `required_signoffs` configured, a `report_error` call (drive the registered `onError` callback) still resolves a terminal `errored` outcome carrying the agent-supplied `error_class` — it is **not** gated and does **not** become `agent/signoff_unobtained`.
5. Run the acceptance e2e + the unit tests + build; confirm PASS.

**Verification:** `npx vitest run src/signoff-gate.e2e.test.ts src/agent-run.test.ts && npm run build`

---

## Final full-suite check

After Pass 5, run the whole executor suite and lint to confirm nothing regressed:

`npm run build && npx vitest run && npm run lint`

(This is a convenience confirmation, not a substitute for the per-pass gates above.)

## Manual checks after completion

None. Every behavior is exercised by an automated gate; the acceptance pass (Pass 5) drives the real HTTP-bridge entry point with a real Ed25519 signer and asserts the real `AsyncCallbackBody` outcome. The host-facing signing-contract documentation and a shippable reference validator are explicit non-goals of the spec (future work).
