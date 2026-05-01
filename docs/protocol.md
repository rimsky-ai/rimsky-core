# Wire Protocols

Rimsky speaks two wire protocols: the **node-executor protocol** (this
document; `proto/v1/node_executor.proto`) for dispatching work to
executors, and the **store-service protocol**
(`proto/v1/store_service.proto`) for talking to standard
store-services running out-of-process per stores-redesign-v3.

This document covers the executor side in detail. The store side is
specified in `docs/specs/2026-04-27-stores-redesign-v3-design.md` §4
and §5, as amended by the 2026-04-30 stores-protocol cleanup; in
summary it ships 4 runtime verbs (`Open` / `Commit` / `Abandon` /
`Release`) plus a startup `Capabilities()` handshake, with the same
gRPC + HTTP+JSON bridge shape as the executor protocol. Each
store-service implements the gRPC server; rimsky's `core/store/remote/`
is the gRPC client. See the store-author guide for authoring details.

Conceptual context (nodes, messages, attributes, stores, locks) lives
in `node-graph-design.md`; implementation shape lives in
`architecture.md`. Vocabulary lives in `glossary.md`.

## Vocabulary

The terms used in this document (claim, named lock, region, selector, address, payload, intent, alias, acquirer, inheritor, holding subgraph, auto-terminal, value-pass, claim-pass, write_semantics, pick policy) are defined in `docs/glossary.md`. When this doc and the glossary disagree, the glossary wins.

---

## 1. Transports

The protocol supports two transports. Both are first-class; conformance-certified executors implement at least one. Supervisor config declares `transport: grpc | http` per executor.

### 1.1 gRPC (canonical)

The generated gRPC service is the authoritative contract. `proto/v1/node_executor.proto` declares:

```protobuf
service NodeExecutor {
  rpc Execute(ExecuteRequest) returns (stream ExecuteEvent);
}
```

Generated Go client stubs live at `github.com/fallguy/rimsky/proto/v1/gen`. Generated TypeScript stubs are bundled with the `@rimsky/executor-claude-agent` npm package.

### 1.2 HTTP+JSON bridge

Each gRPC method has a parallel HTTP route. The bridge uses JSON mapping from `google.api.http` annotations. An executor author writing in a language without comfortable gRPC support — or an internal one-off executor — can implement the HTTP+JSON form exclusively.

The bridge maps:

- `POST /v1/Execute` with a JSON-encoded `ExecuteRequest` body.
- Response is a chunked `application/x-ndjson` stream: one JSON-encoded `ExecuteEvent` per line.
- Stream close without a terminal event is treated identically to gRPC's cancelled-without-terminal as an infrastructure error.

The HTTP body shape mirrors the gRPC message shape. Terminal events on the wire are keyed by `type` (e.g. `{"type":"complete", ...}`) on the async-handoff and incremental-attributes callback paths (see §4 and §5). On the streaming `POST /v1/Execute` response path, events are emitted as the JSON encoding of the proto `ExecuteEvent` oneof (e.g. `{"complete": {...}}`).

### 1.3 Transport selection

An executor may implement one transport or both. An executor that wants to be conformance-certified for gRPC must pass conformance with `--transport=grpc`; likewise for HTTP. The conformance binary takes a `--transport` flag.

In mixed deployments, supervisors declare each executor's transport in config:

```yaml
executors:
  claude-agent:
    transport: grpc
    endpoint: "http://claude-agent:9090"
  http-node:
    transport: http
    endpoint: "http://http-node:9091"
```

---

## 2. Messages

### 2.1 `ExecuteRequest`

The request from supervisor to executor. Carries full dispatch context; only `userdata` is opaque to rimsky.

```protobuf
message ExecuteRequest {
  string node_id = 1;
  string instance_id = 2;
  string node_type = 3;

  // Opaque per-node config from the template. Rimsky never interprets this;
  // only the executor does. NEVER substituted.
  google.protobuf.Struct userdata = 4;

  // Per-run typed attributes. Source-directive fields are pre-populated by
  // rimsky at dispatch; sourceless fields are populated by the executor
  // (terminal-final via attributes_delta on Complete, or incremental via
  // POST {callback_url}/v1/attributes/{node_id}).
  google.protobuf.Struct attributes = 5;

  // The declared JSON Schema for the node's attributes. For executor reference;
  // rimsky validates at dispatch (substitution) and at commit (writeback)
  // regardless.
  google.protobuf.Struct attributes_schema = 6;

  // Handles for each store the node references. Keyed by alias (the per-claim
  // name within the node; defaults to the store name).
  map<string, StoreHandle> stores = 7;

  // HTTP+JSON callback URL the executor may POST to for async handoff and
  // incremental attribute writes. Empty string if the supervisor did not
  // configure a callback endpoint.
  string callback_url = 8;

  // Bearer token the supervisor watches for cancellation requests, also used
  // as the authorization token on the incremental attributes callback and the
  // async terminal callback. Format is opaque to executors.
  string cancel_token = 9;

  // Reserved (formerly `resumed`). Resume is universal — the store
  // detects resumed-vs-fresh internally by lookup against its own state,
  // keyed by the lock-holder identity. There is no protocol-level resume
  // flag; see §11.5 of the v2 design.
  reserved 10;

  // Increments on every retry. Exposed for executor visibility / idempotency.
  int32 run_attempt = 11;
}

message StoreHandle {
  // Store-supplied address bytes returned by Store.Open. Opaque to Rimsky
  // (transported as JSON); the executor decodes per its store-specific
  // knowledge of the store's `kind`.
  google.protobuf.Struct handle = 2;
}
```

Field details:

- `node_id` / `instance_id` — UUIDs. Echoed in async callbacks.
- `node_type` — template-relative type name. Executors can route internally on this.
- `userdata` — opaque JSON. See §6.
- `attributes` — the node's typed per-run attribute object. Source-directive fields (`source: "{{deps.<n>.<f>}}"`, `source: "{{claim.<alias>.address}}"`, `source: "{{claim.<alias>.payload.<f>}}"`, `source: "{{claim.<alias>.region}}"`, or `source: "{{params.<k>}}"`) are pre-populated by the supervisor at dispatch from upstream attributes, claim content, and instance params; the executor should treat that subtree as read-only input. Sourceless fields are slots the executor is expected to fill in (terminal-final via `Complete.attributes_delta`, or incremental via the §5 callback).
- `attributes_schema` — the JSON Schema for `attributes`, copied verbatim from the node template. Provided for executor reference. Rimsky validates `attributes` against this schema both at dispatch (after substitution) and at commit (after writeback) regardless of whether the executor validates.
- `stores` — map of alias → `StoreHandle` for every claim the node acquired or inherited. The supervisor calls `Store.Open` per claim inside the atomic acquisition transaction (spec §7.3 / blessed invariant 15) and packages the returned `Address` bytes opaquely into each `StoreHandle.handle` field. The executor decodes the bytes per its store-specific knowledge of the store's `kind` (declared in operator config; e.g. `filesystem` returns a path-shaped address, `postgres` returns a row-locator-shaped address).
- `callback_url` — base URL for both the async-handoff terminal callback (§4) and the incremental attributes callback (§5). Empty string if the supervisor is not configured for callbacks; in that mode the executor must not emit `AsyncAccepted` and must not attempt incremental writeback.
- `cancel_token` — supervisor-issued bearer token. Executors echo it as `Authorization: Bearer <cancel_token>` on incremental-attributes POSTs and on async terminal callbacks; the supervisor authenticates by comparing the token against the live dispatch row.
- `run_attempt` — 1-indexed retry counter, useful for idempotency keys and progress reporting.

#### 2.1.1 Intent vs. write_semantics

Each claim in a node template carries an `intent` field — `"r"` (read-only) or `"rw"` (read-write) — which is the **graph author's** declaration of how the executor will use the claim. Each store carries a `write_semantics` field — `"direct"`, `"staged_blocking"`, or `"staged_async"` — which is the **store's** declaration of how writes coordinate with readers (operator-configured, bounded above by the store kind's max capability). Together they form the claim's effective mode used for the conflict predicate (sync vs. async × r vs. w; see spec §8.5). Both are invisible at the wire level — the executor sees only the resolved `Address` — but matter for understanding which dispatches are eligible to run concurrently.

### 2.2 `ExecuteEvent`

The streamed response from executor to supervisor.

```protobuf
message ExecuteEvent {
  oneof event {
    Heartbeat heartbeat = 1;
    Complete complete = 2;
    Blocked blocked = 3;
    Errored errored = 4;
    AsyncAccepted async_accepted = 5;
  }
}
```

Exactly **five** event kinds: one non-terminal (`Heartbeat`), four terminal (`Complete`, `Blocked`, `Errored`, `AsyncAccepted`).

### 2.3 Individual event messages

```protobuf
message Heartbeat {
  int64 timestamp_ms = 1;
  string note = 2;
}

message Complete {
  bool changed = 1;
  string change_summary = 2;
  // Optional terminal-final attribute writeback. Empty for the
  // incremental-via-callback pattern.
  google.protobuf.Struct attributes_delta = 3;
}

message Blocked {
  string reason = 1;
  google.protobuf.Struct context = 2;
}

message Errored {
  string error_class = 1;
  google.protobuf.Struct payload = 2;
}

message AsyncAccepted {
  string async_ack_id = 1;
  int64 expected_completion_ms = 2;
}
```

---

## 3. Event semantics

The response stream carries zero or more `Heartbeat` events followed by EXACTLY ONE terminal event. After emitting a terminal event, the executor MUST close the stream immediately.

### 3.1 `Heartbeat` (non-terminal)

An in-progress progress indicator. The executor may emit any number during a long-running execution. Each refreshes the node's `last_heartbeat_at` on the supervisor side; no application-visible effect. `note` is free-form (shown in logs if operators look); `timestamp_ms` is the executor's clock at emit time, purely advisory.

Heartbeats are optional. An executor that completes quickly may emit none.

### 3.2 `Complete` (terminal)

Successful execution. The executor reports:

- `changed` — producer-declared verdict on whether this output differs meaningfully from the previous version. Governs whether `recalculate` fans out to dependents (see `node-graph-design.md` §4.3).
- `change_summary` — optional human-readable note, useful when `changed: true`.
- `attributes_delta` — optional terminal-final attribute writeback, a `Struct` whose top-level keys merge into the node's attribute object. Empty (or absent) when the executor used the §5 incremental callback path, in which case the accumulated incremental writes are the authoritative final state. If both paths are used, the final attribute state is `dispatch-resolved attributes ∪ incremental writes ∪ attributes_delta` (shallow merge in that order).

The supervisor:

1. Merges the delta into the per-node attribute row, validates against `attributes_schema`, and on success emits `attributes_committed`. Validation failure is treated as a commit-time `attributes_schema_failed` and routed through the policy chain.
2. Resolves each of the node's claims per the 4-verb store interface — non-held claims fire `Store.Commit` / `Abandon` immediately at the acquirer's terminal; held claims update their `rimsky_claim_holders` row and let the auto-terminal mechanism (v3 spec §4.10 invariant 13) fire exactly one resolution at holding-subgraph completion. See §7 below.

### 3.3 `Blocked` (terminal)

The executor cannot make progress without external intervention. Translates to a supervisor-side error of class `executor_blocked` unless the node's `error_types` map declares a more specific class the executor used.

- `reason` — human-readable description.
- `context` — structured context for debugging.

The policy chain handles it like any other error class. `Blocked` is distinct from `Errored` in intent: "I can't proceed" rather than "something failed."

### 3.4 `Errored` (terminal)

Application-level error. The executor reports a structured failure matching the node's declared error taxonomy:

- `error_class` — class string; supervisor routes through `on_error(error_class, payload)` to the policy chain.
- `payload` — structured details recorded in the event log.

If the class is not declared in the node's `error_types`, the supervisor treats it as `give_up` with an unknown-class reason.

### 3.5 `AsyncAccepted` (terminal; async handoff)

The executor has accepted the work but will report the final outcome later via HTTP+JSON POST to `ExecuteRequest.callback_url`. See §4.

- `async_ack_id` — identifier the executor will echo in the callback so the supervisor can correlate.
- `expected_completion_ms` — hint for observability; the supervisor uses its own heartbeat-loss cutoff for actual timeout enforcement.

### 3.6 Invariants

- **Exactly one terminal event per stream.** An executor that emits two terminal events, or zero, violates the contract. Supervisors treat the second as protocol-error and log `work_rejected`.
- **Stream close after terminal.** The executor must close the stream immediately after emitting a terminal event. Supervisors treat a hanging-open stream after terminal as an infrastructure error.
- **Stream close without terminal is infrastructure error.** If the stream closes without any terminal event (executor process died, connection dropped), the supervisor routes through `on_error(infra:transport_closed)`.
- **Heartbeats do not count as terminal.** Zero, one, or many heartbeats before the terminal event are all valid.

---

## 4. Async handoff

Executors whose work cannot reasonably complete within a single held `Execute` call (canonically: `claude-agent` spawning a Claude CLI subprocess) use the async-handoff pattern.

### 4.1 Flow

1. Supervisor calls `Execute(request)`.
2. Executor optionally emits zero or more `Heartbeat` events.
3. Executor emits `AsyncAccepted(async_ack_id, expected_completion_ms)` as its terminal event. Closes the stream.
4. Supervisor holds the dispatch-row claim and keeps the node in `running` state. The pre-acquired lock-holder rows and the `rimsky_node_attributes` row persist across the async period — the supervisor does not release them until the callback arrives or the orphan-reap fires. Schedules a heartbeat-loss watchdog per its configured cutoff.
5. Executor does the work out-of-band (spawns a subprocess, posts to an LLM, etc.). It MAY issue zero or more incremental attribute writes during this window via the §5 callback.
6. When the work completes, the executor POSTs the terminal outcome to `${callback_url}/v1/callback/{async_ack_id}`.
7. The supervisor's callback endpoint validates the bearer token, validates `async_ack_id` against its registry of outstanding async-handoffs, and proceeds as if the terminal event had arrived on the original `Execute` stream.

### 4.2 Terminal-callback HTTP contract

The supervisor exposes a callback endpoint at the host/port it advertises to executors. The advertised base URL is supplied to the executor in `ExecuteRequest.callback_url`; the executor **appends** `/v1/callback/{async_ack_id}` to that base to reach the supervisor's chi-routed handler. (The advertised host is either `callback.advertise_host` from supervisor config or the listener's bound host — the latter is only correct on loopback; see `operator-guide.md` for container/k8s setup.)

**URL:** `${callback_url}/v1/callback/{async_ack_id}` — the `async_ack_id` travels as a URL path parameter.

**Method:** `POST`.

**Headers:**

- `Content-Type: application/json`
- `Authorization: Bearer <cancel_token>` — the supervisor validates this against the live dispatch row's cancel token; mismatches return `401 Unauthorized`.

**Body (flat; keyed by `type`):**

```json
{
  "type": "complete" | "blocked" | "errored",

  "changed": <bool>,                  // complete
  "change_summary": "<str>",          // complete
  "attributes_delta": <obj | null>,   // complete; null/absent for incremental-only

  "reason": "<str>",                  // blocked
  "context": <json>,                  // blocked

  "error_class": "<str>",             // errored
  "payload": <json>                   // errored
}
```

Only the fields relevant to the selected `type` are populated; unknown fields are ignored. `attributes_delta: null` (or absent) is the explicit "incremental writeback was used; no terminal-final delta" signal — the supervisor commits whatever incremental writes accumulated during the running window.

**Responses:**

- `200 OK` — callback received and applied. Response body: `{"status":"accepted"}`.
- `401 Unauthorized` — bearer token did not match the live dispatch claim.
- `404 Not Found` — `async_ack_id` does not match an outstanding async-handoff registered by this supervisor. Cause: the original supervisor restarted, the heartbeat-loss sweep released the claim before the callback arrived, or the executor is confused.
- `400 Bad Request` — body malformed (bad JSON or unknown `type`).
- `500 Internal Server Error` — supervisor-internal problem. The ack is re-registered so the executor may retry with idempotent backoff.

**Idempotency:** executors should not issue duplicate callbacks. On success the supervisor's registry removes the `async_ack_id`, so a duplicate callback is rejected with 404. On a transient 500 the ack is re-registered and retries are expected to succeed.

### 4.3 When to use async handoff

Use async handoff when:

- The work's runtime is highly variable or heavy-tailed (LLM calls, external API waits, human-in-the-loop approvals).
- The executor wants to survive its own restart while work continues (e.g. by kicking off a Kubernetes job and polling its status).
- Transport-level connection holds would be expensive or unreliable.

Don't use async handoff when:

- The work is deterministic and bounded (< 30s). A synchronous response via `Complete` is simpler.
- The executor cannot guarantee the callback will fire. The supervisor's heartbeat-loss sweep will eventually release the claim and re-enqueue, but "eventually" depends on cutoff config.

---

## 5. Incremental attributes callback

Executors that emit attribute writes progressively (canonically: `claude-agent`, which streams partial state into a long-lived agent loop and benefits from durable resumption) use the incremental writeback pattern. The terminal-final pattern via `Complete.attributes_delta` is the default; the incremental callback is for executors that need each write durable before the run terminates.

### 5.1 HTTP contract

**URL:** `${callback_url}/v1/attributes/{node_id}` — `node_id` is taken verbatim from `ExecuteRequest.node_id`.

**Method:** `POST`.

**Headers:**

- `Content-Type: application/json`
- `Authorization: Bearer <cancel_token>` — same token as the async terminal callback. The supervisor's handler resolves the token to a live dispatch row owned by this supervisor and verifies the row's `node_id` matches the URL path param. Mismatches return `401 Unauthorized`.

**Body:**

```json
{
  "delta": {
    "<field_name>": <value>,
    ...
  }
}
```

The keys of `delta` are top-level attribute property names. Values are arbitrary JSON. The supervisor merges (shallow, top-level keys replace) into `rimsky_node_attributes.data`, persists, and returns:

**Responses:**

- `204 No Content` — write applied. No body.
- `401 Unauthorized` — bearer token did not match a live dispatch claim, or the URL `node_id` did not match the token's dispatch row.
- `400 Bad Request` — body malformed (bad JSON, missing `delta`, or non-object `delta`).
- `404 Not Found` — no dispatch row for `node_id` is currently `running` on this supervisor.
- `500 Internal Server Error` — supervisor-internal problem; safe to retry.

The supervisor does **not** validate against `attributes_schema` on each incremental write — schema validation runs once at terminal commit (§3.2). Executors are free to write a partial state that would not pass schema mid-run, as long as the cumulative state at terminal time is schema-valid.

### 5.2 Combining incremental and terminal-final writeback

Executors may use only the incremental path, only the terminal-final path (`Complete.attributes_delta`), or both. The final committed attribute state is the shallow merge of:

```
dispatch-resolved attributes
  ∪ incremental writes (in arrival order; later wins on collision)
  ∪ Complete.attributes_delta (last; wins on collision)
```

The TS `claude-agent` reference executor uses the incremental path exclusively and emits `Complete{ attributes_delta: null }` to signal "incremental writes are authoritative."

### 5.3 Idempotency and ordering

The supervisor processes each POST atomically and in arrival order; concurrent POSTs from the same executor instance are not expected and are not specified beyond "ordering may not be preserved across overlapping in-flight requests." Executors should serialize their incremental writes per node.

A duplicate write of the same key (e.g. due to network retry) will overwrite with the same value — idempotent in the trivial sense. There is no compare-and-swap primitive at the protocol layer.

---

## 6. Userdata conventions

`userdata` is opaque to rimsky. The orchestrator never parses, validates, or template-substitutes it. Its contents reach the executor byte-for-byte as supplied in the template. (The template-substitution surface is `attributes` source directives — see `node-graph-design.md`.)

### 6.1 Executor-defined schema

Each executor defines its own userdata schema. The executor is responsible for validating on receipt and rejecting with `Blocked` or `Errored` on malformed input.

Example (`http-node` executor):

```yaml
userdata:
  url: "https://example.com/ingest"
  method: POST
  headers:
    content-type: application/json
  timeout_ms: 30000
```

Example (`claude-agent` executor):

```yaml
userdata:
  model: claude-opus-4-7
  system_prompt: "You are a data extraction assistant..."
  tools: ["web_fetch", "code_interpreter"]
```

### 6.2 No template substitution within userdata

Rimsky does NOT substitute placeholders inside `userdata`. Executors that want template-like substitution (referring to `attributes`, store handles, etc.) implement it themselves on top of `ExecuteRequest.attributes` and `ExecuteRequest.stores`. Convention: executors that template on userdata document their template syntax in the executor author guide and reject malformed templates as `Errored("userdata_template_error")`.

### 6.3 Why opaque

The opacity is load-bearing. It is what lets rimsky serve every domain — HTTP, SQL, LLM, whatever — without growing a per-domain vocabulary. The cost is that rimsky cannot catch a template author's typo in the userdata block; that's the executor's job. The structured surface (`attributes` + `stores`) covers the domains where rimsky needs interoperability (cross-node data flow, store concurrency).

---

## 7. Supervisor-side action mapping per terminal event

The supervisor's per-store-claim action when the run terminates is determined by the terminal event together with the policy-chain action selected for the run. The table below is normative; the verbs are the 4-verb store interface (`Open` / `Commit` / `Abandon` / `Release`) defined in spec §4.1.

Rimsky carries only a success/failure binary across the wire: success → `Commit(claim_id)`; failure → `Abandon(claim_id)`. There is no template-level action vocabulary, no `policy_override` argument, and no `Delete` verb. Store disposition (what `Commit` / `Abandon` mean for the store's own state — e.g. promote-to-tail vs. release-to-back vs. delete vs. flip an items-table row) is governed by the store's own per-pick-policy / per-store configuration; rimsky neither inspects nor selects it.

| Terminal event                                 | Per-claim action (non-held; acquirer's terminal)                                  |
|------------------------------------------------|-----------------------------------------------------------------------------------|
| `Complete{changed: true}`                      | Validate attributes; `Commit(claim_id)`; delete lock-holder row                   |
| `Complete{changed: false}`                     | `Commit(claim_id)`; delete lock-holder row                                        |
| `Blocked` / `Errored` + `give_up`              | `Abandon(claim_id)`; delete lock-holder row                                       |
| `Blocked` / `Errored` + `retry`                | `Abandon(claim_id)`; delete lock-holder row; re-enqueue dispatch                  |
| `Errored` + `invalidate(targets)`              | `Abandon(claim_id)`; delete lock-holder row; emit invalidate to targets           |

For `direct`-mode regional `rw` claims, `Commit` is a store no-op and `Abandon` is degenerate (direct writes cannot be undone — the store-author guide documents this honest limitation).

For **held claims** (any claim referenced by an `inherits:` block in a downstream node), per-node terminals only update the corresponding `rimsky_claim_holders` row to `'completed'` or `'failed'` — no store verb fires. The auto-terminal mechanism (v3 spec §4.10 invariant 13) fires exactly one resolution at holding-subgraph completion based on aggregate outcome: all-success → `Commit(claim_id)`; any-failure → `Abandon(claim_id)`. The store then applies its configured disposition. See `architecture.md` §5 (blessed invariant 13) and `core/supervisor/auto_terminal.go`.

`AsyncAccepted` is not in the table because it is not a run-terminating outcome on its own — the row above is selected once the async terminal callback arrives carrying one of `complete | blocked | errored`.

---

## 8. Auth

### 8.1 v1 recommended: mTLS

At the executor boundary (orchestrator ↔ executor), v1 ships mTLS as the supported auth model for production deployments. The supervisor's config specifies per-executor client cert paths; executors verify orchestrator certs. Certificate rotation is deployment's concern — rimsky reads cert paths at startup.

### 8.2 Plain (dev-only)

In single-trust-zone deployments (docker-compose reference, local development, single-cluster with network isolation), mTLS is optional and can be disabled per-executor in supervisor config:

```yaml
executors:
  http-node:
    transport: http
    endpoint: "http://http-node:9091"
    tls: none
```

For production: mTLS or equivalent network-layer authentication (service mesh with identity-aware proxies) is recommended.

### 8.3 Callback auth (executor → supervisor)

Both callback paths (§4 async terminal and §5 incremental attributes) authenticate with the supervisor-issued `cancel_token` carried as `Authorization: Bearer <token>`. The supervisor resolves the token to a live dispatch row and rejects with `401` on mismatch. This is the same token surface in both directions and the same lifecycle as the dispatch — no separate credential store.

### 8.4 Control-API auth (separate concern)

The control API has a different auth concern (operator interface, not service-to-service). It uses a pluggable `Authenticator` interface. The v1 reference binary defaults to no auth and binds to localhost. Enterprise deployments provide their own module. See `architecture.md` §4.3.

---

## 9. Versioning

### 9.1 `v1` commitments

- Proto files live in `proto/v1/`. The `v1` directory name is part of the package path (`rimsky.v1`).
- Changes within `v1` are backward-compatible only: new fields with default values, new methods, new `ExecuteEvent` oneof variants that existing clients can ignore.
- Breaking changes go to `proto/v2/` (a new directory, a new package, a new generated-code tree).
- The rimsky module ships both versions during transition periods. Executors speaking `v1` work with orchestrators speaking `v1`, several minor versions apart.

### 9.2 Compatibility guarantees

**Forward compatibility:** executors MUST gracefully ignore unknown fields in `ExecuteRequest`. This lets the orchestrator add fields (e.g. a future `trace_id`) without breaking older executors.

**Backward compatibility:** orchestrators MUST gracefully ignore unknown fields in `ExecuteEvent`. This lets executors add fields (e.g. additional payload keys on `Complete`) without breaking older orchestrators.

**New terminal events:** a future v1 minor revision may add a new terminal event kind via a new oneof variant. Orchestrators that don't recognize the variant MUST treat the stream as `infra:unknown_terminal`. Older executors never emit the new variant, so existing behavior is preserved.

### 9.3 Capabilities advertisement (deferred)

A future `Capabilities` RPC on the protocol will let orchestrators query executors for supported features (transports, stub mode, optional event kinds). v1 uses probe-based or config-declared advertisement. See §10.

---

## 10. Conformance

### 10.1 The conformance suite

`rimsky-conformance` is a Go binary that validates a given executor endpoint against the protocol contract. Shipped in this repo at `core/cmd/rimsky-conformance/` and as a Docker image.

Run it against your executor:

```bash
rimsky-conformance --endpoint http://localhost:9091 --transport http
rimsky-conformance --endpoint grpc://localhost:9090 --transport grpc
```

Exit code 0 = conformant; nonzero with diagnostic output = non-conformant.

### 10.2 Scenarios covered

The suite exercises:

- Correct `Execute` for a valid request with populated `attributes` and `stores`.
- Correct rejection of malformed userdata (expects `Blocked` or `Errored`, never `Complete`).
- Correct `Blocked` emission.
- Correct `Errored` emission.
- Async handoff via `callback_url` (if the executor advertises async support), including bearer-token auth.
- Incremental attributes callback (if advertised), including bearer-token auth and 401/404 paths.
- Heartbeat emission on long-running calls.
- Cancel handling (gRPC context cancel, HTTP disconnect).
- `attributes_delta` round-trips a structured JSON object unchanged across encoder boundaries.
- Exactly-one-terminal invariant.
- Stream-close-after-terminal invariant.

### 10.3 Stub-mode requirement for nondeterministic executors

LLM-calling executors (e.g. `claude-agent`) must support a stub mode that short-circuits the LLM call with a canned response. Convention: env var `RIMSKY_EXECUTOR_STUB_MODE=1`.

The conformance binary takes a `--require-stub-mode` flag. When set, the binary issues a probe request at startup that MUST return a canned stub response; if the executor answers with evidence of a live call, conformance fails immediately.

Why: conformance must be deterministic and CI-runnable without paying API costs or depending on model availability. Live-mode testing with a real LLM is the executor author's concern, outside conformance scope.

For deterministic executors (e.g. `http-node` pointed at an HTTP fixture server), stub mode is not required; run with `--no-require-stub-mode`.

### 10.4 CI integration

The conformance image is suitable for executor authors' CI pipelines:

```yaml
# Example GitHub Actions step
- name: Run rimsky conformance
  run: |
    docker run --rm --network host \
      ghcr.io/fallguy/rimsky-conformance:v1 \
      --endpoint http://localhost:9091 \
      --transport http \
      --require-stub-mode
```

---

## 11. Examples

### 11.1 gRPC with `grpcurl`

Assuming an executor at `localhost:9090` and the `proto/v1/` files available:

```bash
# List services
grpcurl -plaintext \
  -import-path proto/v1 -proto node_executor.proto \
  localhost:9090 list

# Call Execute with a minimal ExecuteRequest
grpcurl -plaintext \
  -import-path proto/v1 -proto node_executor.proto \
  -d '{
    "node_id": "550e8400-e29b-41d4-a716-446655440000",
    "instance_id": "660e8400-e29b-41d4-a716-446655440001",
    "node_type": "example",
    "userdata": {"url": "https://httpbin.org/get", "method": "GET"},
    "attributes": {"source_id": "items-2026-04"},
    "attributes_schema": {"type": "object", "properties": {"source_id": {"type": "string"}}},
    "stores": {},
    "callback_url": "",
    "cancel_token": "",
    "run_attempt": 1
  }' \
  localhost:9090 rimsky.v1.NodeExecutor/Execute
```

The response is a stream of JSON-rendered `ExecuteEvent`s:

```json
{"heartbeat": {"timestamp_ms": 1713830400000, "note": "fetching"}}
{"complete": {"changed": true, "change_summary": "fetched 200 response", "attributes_delta": {"body": "..."}}}
```

### 11.2 HTTP+JSON with `curl`

```bash
curl -sN -X POST http://localhost:9091/v1/Execute \
  -H 'Content-Type: application/json' \
  -d '{
    "node_id": "550e8400-e29b-41d4-a716-446655440000",
    "instance_id": "660e8400-e29b-41d4-a716-446655440001",
    "node_type": "example",
    "userdata": {"url": "https://httpbin.org/get", "method": "GET"},
    "attributes": {"source_id": "items-2026-04"},
    "stores": {},
    "callback_url": "",
    "cancel_token": "",
    "run_attempt": 1
  }'
```

Response (newline-delimited JSON):

```
{"heartbeat":{"timestamp_ms":1713830400000,"note":"fetching"}}
{"complete":{"changed":true,"change_summary":"fetched 200 response","attributes_delta":{"body":"..."}}}
```

### 11.3 Async-handoff terminal callback

After receiving `AsyncAccepted` from the executor, the supervisor waits for the callback. The executor eventually issues:

```bash
curl -sS -X POST http://supervisor-callback:9100/v1/callback/async-abc-123 \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <cancel_token>' \
  -d '{
    "type": "complete",
    "changed": true,
    "change_summary": "2 items extracted",
    "attributes_delta": {"items": [{"code": "A1", "name": "Item A"}, {"code": "A2", "name": "Item B"}]}
  }'
```

Response:

```
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"accepted"}
```

When the executor used the incremental writeback path, the body carries `attributes_delta: null` (or omits the key):

```json
{
  "type": "complete",
  "changed": true,
  "change_summary": "agent run completed",
  "attributes_delta": null
}
```

### 11.4 Incremental attributes write

```bash
curl -sS -X POST http://supervisor-callback:9100/v1/attributes/660e8400-e29b-41d4-a716-446655440001 \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <cancel_token>' \
  -d '{
    "delta": {
      "items_so_far": 12,
      "current_phase": "extracting"
    }
  }'
```

Response:

```
HTTP/1.1 204 No Content
```

### 11.5 Errored terminal

An executor reporting an application-level error:

```json
{"errored": {"error_class": "fetch_non_json", "payload": {"url": "https://example.com", "status": 200, "content_type": "text/html"}}}
```

The supervisor routes this through the node's `error_types.fetch_non_json` policy chain if declared, or falls through to unknown-class `give_up` if not.

### 11.6 Blocked terminal

An executor reporting "I can't proceed":

```json
{"blocked": {"reason": "credentials expired", "context": {"auth_url": "https://example.com/oauth"}}}
```

Translates to supervisor-side error class `executor_blocked` (default) or a more specific class if the node declared one and the executor used it.

---

## 12. Notes for executor authors

### 12.1 Implementation checklist

A conformant executor:

1. Implements `NodeExecutor.Execute` (gRPC) and/or the HTTP+JSON bridge (at least one transport).
2. Validates `ExecuteRequest.userdata` on receipt; rejects malformed input with `Blocked` or `Errored` rather than `Complete`.
3. Treats source-directive subtrees of `ExecuteRequest.attributes` as read-only input; writes only into sourceless slots, either via `Complete.attributes_delta` or via the §5 incremental callback.
4. Uses the store handles in `ExecuteRequest.stores` only within the declared `write_regions` / `read_regions`. Writing outside the declared regions is undefined behavior; the supervisor does not police it but other executors may have conflicting reservations.
5. Emits zero or more `Heartbeat` events during long work.
6. Emits exactly one terminal event per stream.
7. Closes the stream immediately after the terminal event.
8. For async handoff (if used): POSTs the terminal outcome to `${callback_url}/v1/callback/{async_ack_id}` with `Authorization: Bearer <cancel_token>` and a valid `async_ack_id`.
9. For incremental attribute writeback (if used): POSTs to `${callback_url}/v1/attributes/{node_id}` with `Authorization: Bearer <cancel_token>` and a `{"delta": {...}}` body.
10. Supports a stub mode gated on `RIMSKY_EXECUTOR_STUB_MODE=1` if the executor is otherwise nondeterministic.
11. Passes `rimsky-conformance --endpoint ... --transport ...` (with `--require-stub-mode` for LLM-calling executors).
12. Publishes a Docker image; optionally a native-language package (npm, pypi, crates.io).

### 12.2 Anti-patterns

- **Do not emit multiple terminal events.** Second terminal is a protocol error; the supervisor logs `work_rejected` and the node's state is indeterminate on the executor side.
- **Do not hold the stream open after terminal.** Close it. Supervisors that wait indefinitely for stream close after terminal is a bug surface; closing promptly keeps the behavior clean.
- **Do not return 200 OK on callback errors.** If the callback POST has invalid `async_ack_id`, respond 404. If malformed, respond 400. A 200 on a bogus callback loses the error.
- **Do not treat `Blocked` as `Errored` or vice versa.** `Blocked` is intent; `Errored` is a specific failure class. Choose based on whether the issue is "can't make progress" or "specific failure mode."
- **Do not write into source-directive attribute fields.** Those subtrees are read-only inputs populated by rimsky at dispatch. Writing into them is undefined; the supervisor's commit-time validation may either accept (overwriting upstream-derived data) or reject (if the schema forbids the resulting shape).
- **Do not skip the bearer token on callbacks.** The supervisor's callback handlers reject unauthenticated requests with `401`. Reusing `cancel_token` is not optional.

### 12.3 See also

- `executor-author-guide.md` (operator-written) has minimal examples in Go, Python, and TypeScript.
- `proto/v1/node_executor.proto` is the authoritative contract source.
- `executors/http-node/` is the reference Go implementation; `executors/claude-agent/` is the reference TypeScript implementation (with async handoff and incremental attributes writeback).

### 12.4 Supervisor-internal: `frame_id`

The supervisor associates each dispatch with a `frame_id` per the frame-resolution design (`docs/specs/2026-04-26-frame-resolution-design.md`). This identifier is supervisor-internal — it is not transmitted in the executor protocol. Executors do not need to be aware of frames; the wire contract above is unchanged by the frame-resolution spec.
