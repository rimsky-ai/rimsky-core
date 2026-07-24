# Host-defined sign-off gate for the claude-agent executor

**Date:** 2026-06-04
**Source sketch:** `.ok-planner/sketches/2026-06-03-rimsky-feature-host-defined-agent-io.md` (superseded by this spec; archived on approval)
**Scope:** `lib/services/executors/claude-agent/` only. No rimsky-core, proto, config-schema, or design-doc changes.

## Problem

When a claude-agent dispatch finishes, the agent calls the internal terminal tool `mcp__rimsky-callback__report_complete` with a free-form `attributes_delta` (`ReportCompleteInput.attributes_delta` is `z.record(z.unknown()).optional()` in `src/internal-mcp-tools.ts`). The node's declared attribute schema is the only constraint on that payload, and that constraint is validated with plain JSON Schema (the executor compiles the node's `attributesSchema` with AJV; rimsky re-validates with `santhosh-tekuri/jsonschema`). When the node's schema is open (`additionalProperties: true`) and an output field is not `required`, the agent can return any keys at all and pass validation. The shape mismatch then surfaces *downstream* as a `template_resolution_failed` error at the node that reads `{{nodes.X.attribute.<field>}}` — too late for the agent to correct.

This is a contract problem, not a prompt-quality problem: there is no point at which the host can enforce, at the agent's terminal boundary, that the output is not just well-shaped but semantically attested. The motivating example (from the source sketch): a `discover-gis-endpoints` node declared an `endpoints` output, the agent returned five keys of its own choosing (none named `endpoints`), the open schema accepted it, and the downstream consumer blocked.

## Goal

Let a host (the project embedding rimsky) gate a claude-agent dispatch's *successful completion* on cryptographic sign-offs from external validator services the agent consults — **without claude-agent connecting to those services**, and **without any change to rimsky**. The gate enforces, at the agent's terminal boundary, that the output was attested by the host-designated validators, and returns unmet-gate failures to the still-running agent for in-session correction rather than as silent downstream failures.

## Approach

Capability-based sign-off. The agent gathers detached Ed25519 signatures from host-configured validator MCP servers it talks to (as a normal MCP client) and presents them in `report_complete`; claude-agent verifies those signatures locally against host-configured public keys before the dispatch can resolve to terminal success. The executor never opens a connection to any validator — trust travels in the signature, not the channel.

Alternative considered and rejected: the rimsky-native verifier-executor pattern (a co-holding verifier node that validates the output and routes a retry via `error-policy`). Rejected because validation would happen in a *second dispatch* after the agent already "completed," giving no in-session correction and re-running the whole node on failure. The whole value of this design is the in-session, boundary-enforced property the verifier pattern structurally cannot provide.

## Architecture & scope

Entirely within `lib/services/executors/claude-agent/` (Node/TypeScript). No rimsky-core, proto, or config-schema changes — the feature rides the existing `attributes.cli` channel, which rimsky already resolves and ships opaquely in `ExecuteRequest.attributes` (a `google.protobuf.Struct`, field 5 of `executor.proto::ExecuteRequest`).

Two coupled capabilities:

1. **Host-configured MCP servers on the agent's tool surface** (`cli.mcp_servers`) — lets a template wire validator (or any) MCP servers into the spawned Claude CLI. This also fills the unbuilt consumption path that issue #13 flagged (the removed `attributes.cli.mcpServers: [{ ref }]` e2e fixture gestured at exactly this).
2. **The sign-off gate** (`cli.required_signoffs`) — `report_complete` cannot resolve to terminal success unless it carries a valid Ed25519 signature, per configured key, over the bound output.

The two are coupled because the gate requires the agent to be able to reach the validator(s) that issue the signatures — and (per issue #14/#13 findings) the Claude CLI only speaks MCP to servers it was *configured* with via `--mcp-config`; a URL mentioned in a prompt is unreachable.

## Configuration

All configuration lives under `attributes.cli`, snake_case to match the existing fields that `parseCliConfig` reads (`bare`, `permission_mode`, `allowed_tools`, `disallowed_tools`, `add_dirs`, `max_budget_usd`, `handle_rate_limits`, `max_schema_corrections`).

### `cli.mcp_servers` — connection wiring

```yaml
cli:
  mcp_servers:
    - name: gis-validator
      url: https://gis-validator.internal/mcp
      headers:                 # optional; reachability secrets are a later pass (non-goal)
        X-Tenant: acme
```

Each entry mirrors the executor's existing `CliToolConfig` (`{ kind: "mcp-http", name, url, headers? }` in `src/cli-runner.ts`). Every declared server is added to the CLI's `--mcp-config` and **all** of its tools are auto-allowed into `--allowedTools` (the host wired the server intending the agent to use it).

### `cli.required_signoffs` — the gate

```yaml
cli:
  required_signoffs:
    - public_key: |            # PEM SubjectPublicKeyInfo, Ed25519
        -----BEGIN PUBLIC KEY-----
        MCowBQYDK2VwAyEA...
        -----END PUBLIC KEY-----
      path: endpoints          # dotted path into attributes_delta; omit ⇒ root
```

A list of `{ public_key, path? }`. Each key maps to exactly one path; `path` absent ⇒ root (the whole `attributes_delta`). `required_signoffs` is a **separate list** from `mcp_servers`: a required signer need not be a connected server (a human / out-of-band approver can sign), and a connected server need not be a required signer.

### `report_complete.signoffs` — the carried tokens

`ReportCompleteInput` (`src/internal-mcp-tools.ts`, currently `{ token, attributes_delta?, changed, change_summary? }`) gains one optional field:

```
signoffs?: string[]    // base64-encoded Ed25519 signatures, a flat bag
```

No key-id labeling: the executor knows the required `(public_key, path)` pairs from config and the value at each path from the submitted payload, so it matches each required pair against the bag.

## Gate mechanics

### What is signed

For each required `{ public_key, path }`, the signed message is the byte concatenation:

```
"rimsky/claude-agent/signoff/v1" ‖ "\n" ‖ <binding_id> ‖ "\n" ‖ canonical_json( value_at(attributes_delta, path) )
```

Ed25519 signs this message directly (it hashes internally with SHA-512; there is no separate application-level SHA-256). `value_at` is the subtree at `path`, or the whole `attributes_delta` when `path` is omitted.

### Binding id

`binding_id` is rimsky's `ExecuteRequest.dispatch_id` (field 12 of `executor.proto::ExecuteRequest`) — the authoritative per-run identity. Binding to it scopes a signature to one dispatch, so a signature cannot be replayed into another run (anti-replay). The gate requires a non-empty `dispatch_id`; a dispatch invoked without one (e.g. stub/test probes, where the proto notes `dispatch_id` may be empty) cannot be gated and is treated as a configuration/usage error rather than silently ungated.

The agent must hand `binding_id` to the validator so the validator signs the same bytes the executor will re-derive. The executor therefore injects `binding_id` into the agent's context (alongside the system prompt claude-agent already assembles for the spawn), and the published signing contract instructs validators to sign `domain ‖ binding_id ‖ canonical(content)`. The agent gains nothing by lying about the id: a wrong id yields a signature the executor's check — which uses the real `dispatch_id` — rejects.

**Plumbing caveat.** Both transports currently collapse `dispatch_id` into the executor's `runId` with a `randomUUID()` fallback (`server.ts` and `http-bridge.ts`, in their `runAndCallback` setup) *before* calling `runAgent`, so by the time the gate runs an empty `dispatch_id` is already indistinguishable from a fabricated UUID. The raw `dispatch_id` must therefore be threaded separately into the run options the gate reads — the gate keys its binding off the raw field, not the `runId` fallback — so the non-empty-`dispatch_id` requirement can actually be enforced (and so the same id can be injected into the agent context for relay to the validator).

### Verification (executor, at `report_complete`)

1. base64-decode `signoffs` → candidate signatures.
2. For each required `{ public_key, path }`: take `value_at(attributes_delta, path)`, canonicalize it, build the message above, and search the candidate bag for a signature that Ed25519-verifies under `public_key`.
3. If every required pair is satisfied → fall through to the existing `attributes_delta` schema validation, then terminal success. If any required pair is unsatisfied → reject in-session (see Give-up path).

Verification uses Node's built-in `crypto` (Ed25519 via `crypto.verify(null, message, publicKey, signature)`); no new crypto dependency.

### Canonicalization

JCS / RFC 8785 canonical JSON. This is a **published part of the contract** that validators must implement (sorted keys, normalized numbers/whitespace), so an honest signature verifies byte-for-byte against the executor's re-derivation.

Implementation: claude-agent has no JCS / RFC-8785 dependency today (`package.json` carries grpc, mcp-sdk, ajv, fastify, pino, yaml, zod). The work must add the canonicalizer — either a vetted RFC-8785 dependency or a small in-tree implementation — and both the executor and the host-facing signing contract name RFC 8785 as the normative scheme, so the two sides produce identical bytes. (The "no new crypto dependency" note under Verification covers only `crypto.verify`; canonicalization is a separate dependency decision.)

## Give-up path

The gate guards *success*, not *failure*: a failure cannot smuggle bad output downstream (a terminal error fires `terminal/error/<class>`, never `terminal/success`), so failing honestly stays available and safe. "No escape hatch" means "no faked success," which the gate on `report_complete` delivers.

- **Unmet sign-offs reuse the existing correction loop.** A `report_complete` whose `signoffs` do not satisfy every required `{ key, path }` is rejected back into the agent's session via the same mechanism as a schema-validation failure (`rejectWithCorrection` in `src/agent-run.ts`), with a message naming exactly which path is unmet and why (no signature supplied vs. signature did not verify). Bounded by a new `cli.max_signoff_attempts` field mirroring `max_schema_corrections` (default 3); on exhaustion, the executor resolves the dispatch as a terminal error with error class `agent/signoff_unobtained` (parallel to the existing `agent/schema_violation`).
- **Two sequential layers in `report_complete`:** schema validation first (existing loop, `max_schema_corrections`), then the signature gate (new loop, `max_signoff_attempts`) — get the shape right, then get it signed. Each has its own retry budget.
- **`report_error` is retained** as the honest-failure exit; it is not excluded. It cannot fake success, only declare failure. (Excluding the free-form report tools entirely is a possible future strict-mode toggle — a non-goal here.)
- **Routing is rimsky's existing `error-policy`.** `agent/signoff_unobtained` (and any `report_error` class) routes through the node's `error_types` chain — `pass | give_up | retry | discard_claims_then_retry` — defaulting to `give_up → failed` when the operator has not mapped the class. Nothing new on the rimsky side.
- **Transient vs. terminal is the agent's call.** A validator that is unreachable → the agent may `report_park` (retry later); a validator that rejects the work → `report_error`. The executor does not classify.

## Components & surfaces touched

All claude-agent-internal. The `signoffs` field crosses three files because the runtime `report_complete` handler does not use `ReportCompleteInput` — be careful to wire the runtime path, not just the test-facing schema.

- `src/cli-runner.ts` — `CliToolConfig`, `mcpConfigJson`, and `buildAllowedTools` already serialize `mcp-http` entries into the `--mcp-config` payload and fold tool names into `--allowedTools` (`buildClaudeCliArgs` emits both). Extend so host `mcp_servers` entries are appended to the `tools` list and their tools auto-allowed (full `mcp__<name>__<tool>` names, or a server-prefix entry).
- `src/server.ts::parseCliConfig` **and** its mirror `src/http-bridge.ts::parseCliConfig` (the latter annotated `@source: src/server.ts`) — both learn `mcp_servers` and `required_signoffs`. The `@source` tracked-duplication discipline means both copies change together.
- `src/expected-attributes-schema.ts` — two changes: (1) the `cli` schema block gains `mcp_servers` and `required_signoffs` properties, kept in lock-step with the parser per that file's own comment (a parser field without a schema entry rejects legitimate templates at dispatch validation; a schema entry without a reader silently no-ops); (2) **add `agent/signoff_unobtained` to the `declaredErrorClasses` list** (advertised to rimsky via `Capabilities.declared_error_classes`). Operator `error_types:` keys range-check against this advertised vocabulary at template registration, so without this an operator who tries to route the new class would have their template rejected — defeating the "routes through the node's `error_types` chain" property below.
- `src/internal-mcp-server.ts` — the **live** `report_complete` MCP handler. It inlines its own zod schema (`{ token, attributes_delta, changed, change_summary }`) and forwards exactly those to `onComplete`; it does **not** import `ReportCompleteInput`. Add `signoffs` to this inline schema and forward `args.signoffs` to `onComplete`. This is the runtime-critical wiring — the gate never sees a signature without it.
- `src/token-registry.ts` — extend the `onComplete` callback type signature to carry `signoffs` (it is currently a fixed-arity signature with no signoffs parameter).
- `src/internal-mcp-tools.ts::ReportCompleteInput` and `TOOL_DEFINITIONS` — add the `signoffs` field (Zod schema + the MCP tool's published `inputSchema`) for the tool *definition* surface and its tests. Note this copy is consumed only by tests; the runtime path is `internal-mcp-server.ts` above. Keep the two in agreement.
- `src/agent-run.ts` — assemble host `mcp_servers` into the spawned `tools` at all three hardcoded sites (initial spawn, post-park resume, exit-recovery resume, each currently `[{ kind: "mcp-http", name: "rimsky-callback", url: effectiveCallback.url }]`); inject `binding_id` (the raw `dispatch_id`) into the agent context; add the signature-gate check and its retry loop in the `onComplete` path, after the existing schema check.
- `src/server.ts` / `src/http-bridge.ts` (their `runAndCallback` setup) and `AgentRunOptions` (in `agent-run.ts`) — thread the **raw `dispatch_id`** from `ExecuteRequest` / the HTTP body into `runAgent` as a distinct field, separate from the existing `runId` (which is `dispatch_id || randomUUID()`). The gate binds to and enforces non-emptiness on the raw field; the `runId` fallback would mask an empty `dispatch_id`.

## Data flow

1. Host declares `cli.mcp_servers` and `cli.required_signoffs` in the node template (as static-default attribute values, or source-bound/overridable — rimsky's existing attribute machinery; opaque to rimsky).
2. rimsky resolves attributes at dispatch and ships them in `ExecuteRequest.attributes`; `dispatch_id` is set on the request.
3. claude-agent reads `attributes.cli` via `parseCliConfig`, writes the `--mcp-config` (host servers + the internal `rimsky-callback`), builds `--allowedTools`, injects `binding_id` into the agent context, and spawns the CLI.
4. The agent does its work, consults each validator MCP server (handing it `binding_id` + the content for its path), and collects the returned signatures.
5. The agent calls `report_complete` with `attributes_delta` and `signoffs`.
6. The executor validates the delta against the schema (existing), then verifies every required `(key, path)` signature over the bound message (new). All satisfied → terminal success (`AsyncCallbackBody.success`); unmet beyond budget → terminal error `agent/signoff_unobtained` (`AsyncCallbackBody.error`).

## Testing & acceptance

### Acceptance scenario

A node with `executor: claude-agent`, a `cli.mcp_servers` entry for a validator, and a `cli.required_signoffs` entry carrying a test keypair's public key at `path: endpoints`, is dispatched through claude-agent's **real gRPC `Execute` entry point (or the HTTP bridge's equivalent)** — the path that carries `ExecuteRequest.dispatch_id` and POSTs the terminal `AsyncCallbackBody` to the callback URL. Driving the real entry point (not `runAgent` in isolation) is deliberate: it exercises the `dispatch_id → binding_id` plumbing, which is exactly the surface a `runAgent`-direct test would skip. Two real runs:

- **Unsigned output is blocked.** The fake CLI emits `report_complete` with an `endpoints` value but no valid signoff. The real gate rejects it in-session each attempt; after `max_signoff_attempts` the executor POSTs `AsyncCallbackBody{ error: { error_class: "agent/signoff_unobtained" } }` to the callback URL. The dispatch does **not** complete — the unsigned output never becomes a terminal success.
- **Signed output completes.** The fake CLI obtains a real Ed25519 signature over `domain ‖ dispatch_id ‖ canonical(endpoints)` from the test signer — using the same `dispatch_id` the Execute request carried — and emits `report_complete` carrying it. The real gate verifies it, and the executor POSTs `AsyncCallbackBody{ success: { attributes_delta } }`. The dispatch completes with the signed output.

Real components: the gRPC / HTTP Execute entry point, the executor's signature gate, and the Ed25519 signer (the value-delivering surface). Faked: the CLI subprocess (a `CliRunner` fake emitting `report_complete` — the fake-CLI technique from `src/lifecycle.e2e.test.ts`, but driven *through the real Execute entry point* rather than calling `runAgent` directly) and the LLM behind it. The feature exercises *enforcement*, not the model's reasoning, so a real LLM would add only nondeterminism and API cost without touching the gate. The product-faithful observable is the real `AsyncCallbackBody` on the callback URL — exactly what rimsky's supervisor receives.

### Test strategy beyond acceptance

- **Gate verification units:** valid signature accepts; missing / malformed / wrong-key / wrong-path signature rejects; multi-signer AND (every required key satisfied, partial set rejects); per-path isolation (a signature for path A does not satisfy path B); cross-dispatch replay rejected (a signature bound to a different `binding_id` fails); canonicalization equivalence (key-order / whitespace variants of the same value still verify).
- **Config:** `parseCliConfig` (and the `http-bridge.ts` mirror) parse the two new fields; `expected-attributes-schema.ts` stays in lock-step; `mcpConfigJson` / `buildAllowedTools` add host servers to `--mcp-config` and auto-allow their tools.
- **Give-up loop:** reject → retry → exhaustion → `agent/signoff_unobtained`; `report_error` still terminal-errors.
- **Test signer fixture:** a small helper that generates an Ed25519 keypair, surfaces the public key (PEM SPKI) for config, and signs the bound message — real crypto, reusable across the unit and acceptance tests.

## Design changes

**NONE — and this is a deliberate, recorded decision, not an omission.**

This feature is entirely consumption-side: it lives inside the bundled claude-agent executor, whose internals the rimsky concept catalog does not model (`concept:executor` records that the LLM-agent executor "moved to the consumption side, outside the platform"). It changes no rimsky concept's definition, boundary, or invariant:

- `concept:attribute` — used exactly as designed. The new `cli.mcp_servers` / `cli.required_signoffs` keys are unenumerated extension properties admitted by the concept's existing open-schema carve-out (an executor schema that does not constrain a property has delegated naming authority for it). No invariant changes.
- `concept:executor` — the gRPC wire protocol is untouched; the gate is internal to one executor implementation.
- `concept:error-policy` — routed *through* (the `agent/signoff_unobtained` class flows the existing `error_types` chain), not changed.
- `concept:validation` — a *different* surface (a registration-time service RPC). This feature does not touch it.

**Specifically do not, on the basis of this work, edit any file under `.ok-planner/design/`.** In particular, do **not** amend `concept:attribute`'s Boundaries note that currently states semantic validation is "today the verifier-executor pattern … covers that surface." That note describes rimsky-*native* patterns; the sign-off gate is a consumption-side executor feature, not a rimsky offering, and the concept self-containment rule forbids the attribute concept from citing a consumption-side implementation regardless. The catalog stays as-is. This is claude-agent business.

## Non-goals

Decisions deliberately out of scope for this spec:

- **Generalizing the gate to other agentic executors.** The pattern may be reused for other agent-shaped executors later; this work touches only claude-agent.
- **Secrets / env-refs for validator connection headers.** `mcp_servers[].headers` may carry static values, but resolving a secret from the executor's deployment environment (e.g. `Authorization: "Bearer ${env:…}"`) is a separate later pass. Note that `headers` ride `attributes`, which is persisted and trace-logged — inline secrets are a leak surface, which is precisely why secret handling is deferred rather than done casually here.
- **Incremental `attributes_set` writeback interacting with the gate.** The gate binds the terminal-final `attributes_delta`. A run that produces its output via incremental `attributes_set` calls (no single terminal payload) has nothing at a `path` to bind; reconciling sign-offs with incremental writeback needs its own design.
- **Excluding the free-form `report_error` / `report_complete` tools** ("signed success or nothing" strict mode). Retaining `report_error` is the default; a strict-mode toggle is future work.
- **A shippable reference validator service.** The acceptance signer is a test-only fixture; publishing a reusable example validator MCP server (and the host-facing signing-contract docs) is a separate effort.
