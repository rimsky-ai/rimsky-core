# Node-Executor Protocol

Reference for the protocol rimsky supervisors use to dispatch work to executors. This document covers transports, message shapes, terminal-event semantics, async handoff, userdata conventions, versioning, auth, conformance, and worked examples. It is the wire contract in human-readable form; the authoritative source is `proto/v1/node_executor.proto`.

Conceptual context (nodes, messages, resources) lives in `node-graph-design.md`; implementation shape (package layout, processes, library entry points) lives in `architecture.md`.

---

## 1. Transports

The protocol supports two transports. Both are first-class; conformance-certified executors implement at least one. Supervisor config declares `transport: grpc | http` per executor.

### 1.1 gRPC (canonical)

The generated gRPC service is the authoritative contract. Code in `proto/v1/node_executor.proto` declares:

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
  // only the executor does.
  google.protobuf.Struct userdata = 4;

  // Instance params as supplied at POST /instances, with params_redact applied.
  google.protobuf.Struct instance_params = 5;

  // Current versions of dependency resources, keyed by dependency node_type.
  map<string, google.protobuf.Value> deps_data = 6;

  // Current versions of resources this node declared in reads_resources,
  // keyed by the declared name.
  map<string, google.protobuf.Value> reads_data = 7;

  // HTTP+JSON callback URL the executor may POST to for async handoff.
  // Populated by the supervisor; empty string if not configured.
  string callback_url = 8;

  // Reserved for future use. v1 uses grpc ctx / http disconnect for cancel.
  string cancel_token = 9;
}
```

Field details:

- `node_id` / `instance_id` — UUIDs. Echoed in async callbacks.
- `node_type` — template-relative type name. Executors can route internally on this.
- `userdata` — opaque JSON. See §5.
- `instance_params` — instance-level params; values listed in `params_redact` are omitted.
- `deps_data` — map of dependency-node-type → current-version data of that node's resource. For nodes with multiple owned resources, the entry is a JSON map of resource-name → data. Key: the dependency's `node_type` string.
- `reads_data` — map of read-name → current-version data for resources declared in `reads_resources`.
- `callback_url` — the supervisor's async-handoff endpoint. Empty string if the supervisor is not configured for async.
- `cancel_token` — reserved. v1 supervisors signal cancel via gRPC context cancellation or HTTP disconnect.

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
  google.protobuf.Value result = 1;
  bool changed = 2;
  string change_summary = 3;
}

message Blocked {
  string reason = 1;
  google.protobuf.Value context = 2;
}

message Errored {
  string error_class = 1;
  google.protobuf.Value payload = 2;
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

An in-progress progress indicator. The executor may emit any number during a long-running execution. Each refreshes the node's `last_heartbeat_at` on the supervisor side; no application-visible effect. `note` is free-form string (shown in logs if operators look); `timestamp_ms` is the executor's clock at emit time, purely advisory.

Heartbeats are optional. An executor that completes quickly may emit none.

### 3.2 `Complete` (terminal)

Successful execution. The executor reports:

- `result` — the work product. Serialized as `google.protobuf.Value` (arbitrary JSON).
- `changed` — producer-declared verdict on whether this output differs meaningfully from the previous version. Governs whether `recalculate` fans out to dependents (see `node-graph-design.md` §4.3).
- `change_summary` — optional human-readable note when `changed: true`.

The supervisor hands `result` to each of the node's owned resources (if any) via the resource interface for quality-rule evaluation and commit. If all resources accept, the node transitions to `fresh` and `recalculate` emits. If any resource rejects with a `severity: error` quality failure, the supervisor routes through `on_error(quality_rule_failed)`.

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
- **Stream close after terminal.** The executor must close the stream immediately after emitting a terminal event. Supervisors treat a hanging-open stream after terminal as infrastructure error.
- **Stream close without terminal is infrastructure error.** If the stream closes without any terminal event (executor process died, connection dropped), the supervisor routes through `on_error(infra:transport_closed)`.
- **Heartbeats do not count as terminal.** Zero, one, or many heartbeats before the terminal event are all valid.

---

## 4. Async handoff

Executors whose work cannot reasonably complete within a single held `Execute` call (canonically: `claude-agent` spawning a Claude CLI subprocess) use the async-handoff pattern.

### 4.1 Flow

1. Supervisor calls `Execute(request)`.
2. Executor optionally emits zero or more `Heartbeat` events.
3. Executor emits `AsyncAccepted(async_ack_id, expected_completion_ms)` as its terminal event. Closes the stream.
4. Supervisor holds the dispatch-row claim and keeps the node in `running` state. Schedules a heartbeat-loss watchdog per its configured cutoff.
5. Executor does the work out-of-band (spawns a subprocess, posts to an LLM, whatever).
6. When the work completes, the executor POSTs the terminal outcome to `request.callback_url`.
7. The supervisor's callback endpoint validates `async_ack_id` against its registry of outstanding async-handoffs and proceeds as if the terminal event had arrived on the original `Execute` stream.

### 4.2 Callback HTTP contract

The supervisor exposes a callback endpoint at the host/port it advertises to executors. The advertised base URL is supplied to the executor in `ExecuteRequest.callback_url`; the executor **appends** `/v1/callback/{async_ack_id}` to that base to reach the supervisor's chi-routed handler. (The advertised host is either `callback.advertise_host` from supervisor config or the listener's bound host — the latter is only correct on loopback; see `operator-guide.md` for container/k8s setup.)

**URL:** `${callback_url}/v1/callback/{async_ack_id}` — the `async_ack_id` travels as a URL path parameter.

**Method:** `POST`.

**Content-Type:** `application/json`.

**Body (flat; keyed by `type`):**

```json
{
  "type": "complete" | "blocked" | "errored",

  "result": <json>,              // complete
  "changed": <bool>,             // complete
  "change_summary": "<str>",     // complete

  "reason": "<str>",             // blocked
  "context": <json>,             // blocked

  "error_class": "<str>",        // errored
  "payload": <json>              // errored
}
```

Only the fields relevant to the selected `type` are populated; unknown fields are ignored. The supervisor matches the path's `async_ack_id` against its in-memory registry of outstanding async-handoffs.

**Responses:**

- `200 OK` — callback received and applied. Response body: `{"status":"accepted"}`.
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

## 5. Userdata conventions

`userdata` is opaque to rimsky. The orchestrator never parses, validates, or template-substitutes it. Its contents reach the executor byte-for-byte as supplied in the template.

### 5.1 Executor-defined schema

Each executor defines its own userdata schema. The executor is responsible for validating on receipt and rejecting with `Blocked` or `Errored` on malformed input.

Example (`http-node` executor):

```yaml
userdata:
  url: "https://example.com/ingest"
  method: POST
  headers:
    content-type: application/json
  body:
    source: "{{instance_params.source_id}}"
  timeout_ms: 30000
```

Example (`claude-agent` executor):

```yaml
userdata:
  model: claude-opus-4-7
  system_prompt: "You are a data extraction assistant..."
  user_prompt_template: "Extract items from {{deps.source.url}}"
  tools: ["web_fetch", "code_interpreter"]
  result_schema:
    type: object
    properties:
      items: { type: array }
```

### 5.2 Template substitution within userdata

Rimsky does NOT substitute placeholders inside `userdata`. Executors that want template-like substitution (using `instance_params`, `deps_data`, `reads_data`) implement it themselves. The executor receives `instance_params`, `deps_data`, and `reads_data` in the `ExecuteRequest` and can template over `userdata` at execute time however it wishes (e.g. `{{deps.X}}` in the `http-node` body).

Convention: executors that template on userdata should document their template syntax in the executor author guide and reject malformed templates as `Errored("userdata_template_error")`.

### 5.3 Why opaque

The opacity is load-bearing (see `node-graph-design.md` §3.4). It is what lets rimsky serve every domain — HTTP, SQL, LLM, whatever — without growing a per-domain vocabulary. The cost is that rimsky cannot catch a template author's typo in the userdata block; that's the executor's job.

---

## 6. Auth

### 6.1 v1 recommended: mTLS

At the executor boundary (orchestrator ↔ executor), v1 ships mTLS as the supported auth model for production deployments. The supervisor's config specifies per-executor client cert paths; executors verify orchestrator certs. Certificate rotation is deployment's concern — rimsky reads cert paths at startup.

### 6.2 Plain (dev-only)

In single-trust-zone deployments (docker-compose reference, local development, single-cluster with network isolation), mTLS is optional and can be disabled per-executor in supervisor config:

```yaml
executors:
  http-node:
    transport: http
    endpoint: "http://http-node:9091"
    tls: none
```

For production: mTLS or equivalent network-layer authentication (service mesh with identity-aware proxies) is recommended.

### 6.3 Control-API auth (separate concern)

The control API has a different auth concern (operator interface, not service-to-service). It uses a pluggable `Authenticator` interface. v1 reference binary defaults to no auth and binds to localhost. Enterprise deployments provide their own module. See `architecture.md` §4.3.

---

## 7. Versioning

### 7.1 `v1` commitments

- Proto files live in `proto/v1/`. The `v1` directory name is part of the package path (`rimsky.v1`).
- Changes within `v1` are backward-compatible only: new fields with default values, new methods, new `ExecuteEvent` oneof variants that existing clients can ignore.
- Breaking changes go to `proto/v2/` (a new directory, a new package, a new generated-code tree).
- The rimsky module ships both versions during transition periods. Executors speaking `v1` work with orchestrators speaking `v1`, several minor versions apart.

### 7.2 Compatibility guarantees

**Forward compatibility:** executors MUST gracefully ignore unknown fields in `ExecuteRequest`. This lets the orchestrator add fields (e.g. a future `trace_id`) without breaking older executors.

**Backward compatibility:** orchestrators MUST gracefully ignore unknown fields in `ExecuteEvent`. This lets executors add fields (e.g. additional payload keys on `Complete`) without breaking older orchestrators.

**New terminal events:** a future v1 minor revision may add a new terminal event kind via a new oneof variant. Orchestrators that don't recognize the variant MUST treat the stream as `infra:unknown_terminal`. Older executors never emit the new variant, so existing behavior is preserved.

### 7.3 Capabilities advertisement (deferred)

A future `Capabilities` RPC on the protocol will let orchestrators query executors for supported features (transports, stub mode, optional event kinds). v1 uses probe-based or config-declared advertisement. See §9.

---

## 8. Conformance

### 8.1 The conformance suite

`rimsky-conformance` is a Go binary that validates a given executor endpoint against the protocol contract. Shipped in this repo at `core/cmd/rimsky-conformance/` and as a Docker image.

Run it against your executor:

```bash
rimsky-conformance --endpoint http://localhost:9091 --transport http
rimsky-conformance --endpoint grpc://localhost:9090 --transport grpc
```

Exit code 0 = conformant; nonzero with diagnostic output = non-conformant.

### 8.2 Scenarios covered

The suite exercises:

- Correct `Execute` for a valid request.
- Correct rejection of malformed userdata (expects `Blocked` or `Errored`, never `Complete`).
- Correct `Blocked` emission.
- Correct `Errored` emission.
- Async handoff via `callback_url` (if the executor advertises async support).
- Heartbeat emission on long-running calls.
- Cancel handling (gRPC context cancel, HTTP disconnect).
- Result-serialization edge cases (large numbers, null handling, nested structures).
- Exactly-one-terminal invariant.
- Stream-close-after-terminal invariant.

### 8.3 Stub-mode requirement for nondeterministic executors

LLM-calling executors (e.g. `claude-agent`) must support a stub mode that short-circuits the LLM call with a canned response. Convention: env var `RIMSKY_EXECUTOR_STUB_MODE=1`.

The conformance binary takes a `--require-stub-mode` flag. When set, the binary issues a probe request at startup that MUST return a canned stub response; if the executor answers with evidence of a live call, conformance fails immediately.

Why: conformance must be deterministic and CI-runnable without paying API costs or depending on model availability. Live-mode testing with a real LLM is the executor author's concern, outside conformance scope.

For deterministic executors (e.g. `http-node` pointed at an HTTP fixture server), stub mode is not required; run with `--no-require-stub-mode`.

### 8.4 CI integration

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

## 9. Examples

### 9.1 gRPC with `grpcurl`

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
    "userdata": {"url": "https://httpbin.org/get", "method": "GET"}
  }' \
  localhost:9090 rimsky.v1.NodeExecutor/Execute
```

The response is a stream of JSON-rendered `ExecuteEvent`s:

```json
{"heartbeat": {"timestamp_ms": 1713830400000, "note": "fetching"}}
{"complete": {"result": {"body": "..."}, "changed": true, "change_summary": "fetched 200 response"}}
```

### 9.2 HTTP+JSON with `curl`

```bash
curl -sN -X POST http://localhost:9091/v1/Execute \
  -H 'Content-Type: application/json' \
  -d '{
    "node_id": "550e8400-e29b-41d4-a716-446655440000",
    "instance_id": "660e8400-e29b-41d4-a716-446655440001",
    "node_type": "example",
    "userdata": {"url": "https://httpbin.org/get", "method": "GET"}
  }'
```

Response (newline-delimited JSON):

```
{"heartbeat":{"timestamp_ms":1713830400000,"note":"fetching"}}
{"complete":{"result":{"body":"..."},"changed":true,"change_summary":"fetched 200 response"}}
```

### 9.3 Async-handoff callback

After receiving `AsyncAccepted` from the executor, the supervisor waits for the callback. The executor eventually issues:

```bash
curl -sS -X POST http://supervisor-callback:9100/v1/callback/async-abc-123 \
  -H 'Content-Type: application/json' \
  -d '{
    "type": "complete",
    "result": {"items": [{"code": "A1", "name": "Item A"}]},
    "changed": true,
    "change_summary": "2 items extracted"
  }'
```

Response:

```
HTTP/1.1 200 OK
Content-Type: application/json

{"status":"accepted"}
```

### 9.4 Errored terminal

An executor reporting an application-level error:

```json
{"errored": {"error_class": "fetch_non_json", "payload": {"url": "https://example.com", "status": 200, "content_type": "text/html"}}}
```

The supervisor routes this through the node's `error_types.fetch_non_json` policy chain if declared, or falls through to unknown-class `give_up` if not.

### 9.5 Blocked terminal

An executor reporting "I can't proceed":

```json
{"blocked": {"reason": "credentials expired", "context": {"auth_url": "https://example.com/oauth"}}}
```

Translates to supervisor-side error class `executor_blocked` (default) or a more specific class if the node declared one and the executor used it.

---

## 10. Notes for executor authors

### 10.1 Implementation checklist

A conformant executor:

1. Implements `NodeExecutor.Execute` (gRPC) and/or the HTTP+JSON bridge (at least one transport).
2. Validates `ExecuteRequest.userdata` on receipt; rejects malformed input with `Blocked` or `Errored` rather than `Complete`.
3. Emits zero or more `Heartbeat` events during long work.
4. Emits exactly one terminal event per stream.
5. Closes the stream immediately after the terminal event.
6. For async handoff (if used): POSTs the terminal outcome to `callback_url` with a valid `async_ack_id`.
7. Supports a stub mode gated on `RIMSKY_EXECUTOR_STUB_MODE=1` if the executor is otherwise nondeterministic.
8. Passes `rimsky-conformance --endpoint ... --transport ...` (with `--require-stub-mode` for LLM-calling executors).
9. Publishes a Docker image; optionally a native-language package (npm, pypi, crates.io).

### 10.2 Anti-patterns

- **Do not emit multiple terminal events.** Second terminal is a protocol error; the supervisor logs `work_rejected` and the node's state is indeterminate on the executor side.
- **Do not hold the stream open after terminal.** Close it. Supervisors that wait indefinitely for stream close after terminal is a bug surface; closing promptly keeps the behavior clean.
- **Do not return 200 OK on callback errors.** If the callback POST has invalid `async_ack_id`, respond 404. If malformed, respond 400. A 200 on a bogus callback loses the error.
- **Do not treat `Blocked` as `Errored` or vice versa.** `Blocked` is intent; `Errored` is a specific failure class. Choose based on whether the issue is "can't make progress" or "specific failure mode."
- **Do not interpret `instance_params.params_redact` yourself.** The supervisor has already applied redactions before populating `ExecuteRequest.instance_params`. What you see is what's safe.

### 10.3 See also

- `executor-author-guide.md` (operator-written) has minimal examples in Go, Python, and TypeScript.
- `proto/v1/node_executor.proto` is the authoritative contract source.
- `executors/http-node/` is the reference Go implementation; `executors/claude-agent/` is the reference TypeScript implementation (with async handoff).
