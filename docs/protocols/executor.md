# Implementing an executor

This guide is for developers implementing an executor — in any language — and wiring it into a Rimsky deployment. The wire contracts live at `protocols/proto/v1/executor.proto` (the required dispatch protocol) and `protocols/proto/v1/executor_observability.proto` (the optional read-only observability protocol); this guide is the practical companion.

<!-- @source: ../../.ok-planner/design/concepts/executor.md -->
> The protocol-level term for the service that runs a node's work. Implements the dispatch protocol `Executor` (one method, `Execute`) and optionally the paired read-only `ExecutorObservability` protocol (`Capabilities`, `GetTrace`, `StreamTrace`). Out-of-process; supervisors dispatch to executors over gRPC, with an HTTP+JSON bridge available for non-Go services.

> **Auth-blind advisory.** Rimsky has no machinery for credentials, encryption, or access control. Encrypt sensitive bytes before handing them to Rimsky if you need protection. Service-to-service auth is operator-configured at the deployment layer (mTLS, IAM).

---

## 1. The wire contract

The executor surface is split across two service definitions. The required dispatch protocol carries one method:

```protobuf
service Executor {
  rpc Execute(ExecuteRequest) returns (stream ExecuteEvent);
}
```

Source: `protocols/proto/v1/executor.proto`.

The optional read-only observability protocol carries three:

```protobuf
service ExecutorObservability {
  rpc Capabilities(CapabilitiesRequest) returns (ObservabilityCapabilities);
  rpc GetTrace(GetTraceRequest) returns (Trace);
  rpc StreamTrace(StreamTraceRequest) returns (stream TraceEvent);
}
```

Source: `protocols/proto/v1/executor_observability.proto`.

Rimsky's supervisor dials the executor at dispatch time and streams events back via `Execute`. Dashboards (and other read-only consumers) dial the executor's observability service to pull or stream per-dispatch traces. Services MUST implement `Executor`; `ExecutorObservability` is opt-in but recommended for any executor whose dispatches are interesting to humans.

`Execute` is the load-bearing method. The executor receives an `ExecuteRequest` with substituted attributes, and streams back zero or more events ending in one of: `Complete`, `Error{error_class: "executor_blocked"}`, `Error{error_class}`, `AwaitAsyncCallback`.

## 2. The methods

### `Executor.Execute(ExecuteRequest) → stream<ExecuteEvent>`

Dispatch a node. Inside `ExecuteRequest`:

- **node identity** — the supervisor names which (instance, node) it is dispatching.
- **attributes** — already substituted. The executor sees resolved values; the `{{...}}` directives have been resolved by the supervisor. This is the only template-author-supplied input surface; the previous `userdata` field has been collapsed into `attributes` and is no longer carried on the wire.
- **claim contexts** — for each claim the node holds (its own or inherited), the address, payload, and scope made available for executor access.

Stream back any number of these events:

- **`Heartbeat`** — keep-alive while work continues.
- **`NamedEvent`** — non-terminal domain-shaped signal. Two fields: `string name` (must appear in `Capabilities.declared_events`) plus `bytes payload` (opaque to Rimsky; available to substitution as `nodes.<emitter>.event.<name>.<path>`). Zero or more emissions per run; does not close the stream.
- **`Complete`** — terminal success. Three fields:
    - `bool changed` — producer-declared verdict on whether this run produced a different value than the previous run. A `false` value halts cascade propagation at this node.
    - `string change_summary` — free-text summary of the change (audit-log only; not parsed by Rimsky).
    - `Struct attributes_delta` — terminal-final attribute writeback (validated against the node's attributes schema). May be empty when the executor used the incremental-callback path during the run (see spec §12.5).
- **`Error{error_class: "executor_blocked"}`** — terminal: I produced output but explicitly chose not to claim success. Use `Error{error_class: "executor_blocked"}` (rather than `Error{error_class}`) for low-confidence outputs that should route to human review or other downstream-decision flows. Retry semantics come from the node's error policy.
- **`Error{error_class}`** — terminal: an application-level error. Two fields: `string error_class` (an executor-defined classifier) plus an opaque `Struct payload`. The executor does NOT pick the resolution. The supervisor's policy chain in the template maps `(error_class, retry_counter)` to one of `retry`, `give_up`, or `pass`. Cascade coupling on failure is declared receiver-side via `subscribes: [{node: <sender>, on: state, when: failed, error_class: <class>}]` on the impactee node.
- **`AwaitAsyncCallback`** — non-streaming terminal: I'll send the final event later via callback (see §4).
- **`Park`** — terminal: pause this run until externally resumed. Four fields: `string reason` (non-empty discouraged but accepted), `bytes payload` (opaque; passed back as `ResumeContext.payload`), `google.protobuf.Timestamp resume_at` (optional; absent means signal-based-only), `string session_token` (optional; opaque executor-side identifier passed back as `ResumeContext.session_token`). Resume happens via time elapsed, an admin invalidate, or an in-graph `on_event` invalidate. See `.ok-planner/design/concepts/parked-state.md`.

### `ExecutorObservability.StreamTrace(StreamTraceRequest) → stream<TraceEvent>`

Streaming trace of executor activity for observability dashboards. Keyed by `dispatch_id`.

### `ExecutorObservability.GetTrace(GetTraceRequest) → Trace`

Pull a previously-streamed trace by `dispatch_id`. Useful for replaying past invocations from dashboards.

### `ExecutorObservability.Capabilities(CapabilitiesRequest) → ObservabilityCapabilities`

Startup handshake for the observability protocol. Declares whether the executor supports trace-get and trace-stream, the per-dispatch retention window, any custom UI URL the dashboard should embed, the executor's `expected_attributes_schema` (JSON Schema bytes; empty means accept-any), and the `declared_events` array (event names the executor may emit via `NamedEvent`). Probed once per service at process startup.

`expected_attributes_schema` is enforced by Rimsky at template registration (`template_validation_failed` when the declared template attribute schema is incompatible) and at dispatch (`attributes_schema_invalid` when the substituted bag violates the schema; the terminal write-back equivalent is `attributes_schema_failed`). `declared_events` is cross-validated against any `subscribes: [{on: event, name: <name>}]` entries in registering templates; references to undeclared events reject the registration.

## 3. The attribute surface

<!-- @source: ../../.ok-planner/design/concepts/attribute.md -->
> The single template-author-supplied input surface for an executor dispatch. Attributes are declared as a JSON Schema on the template node, substituted (`{{...}}` directives resolved) by the supervisor, validated against the schema at dispatch and again at terminal write-back, and delivered to the executor verbatim. There is no peer "opaque" surface — the historical `userdata` field was collapsed into `attributes` (see `_retired/userdata.md` for the migration record).

This means:

- Every key the executor consumes — model name, system prompts, transport config, etc. — appears under `attributes` and is governed by the node's attributes schema.
- The executor declares the shape it accepts via `expected_attributes_schema` on the `Capabilities` response; Rimsky enforces compatibility at template registration and the substituted bag at dispatch.
- Static configuration (constants the template author wants to hand the executor) lives in attribute `default:` entries; dynamic configuration (values pulled from other nodes or params) lives in `source:` entries. Both surface to the executor identically.
- Encrypted attribute values stay encrypted in transit. Decryption is the executor's responsibility at point of use.

## 4. The async-callback path

For executors whose work outlives a streaming RPC (background jobs, async LLM calls, long-running batch processes), respond with `AwaitAsyncCallback` carrying an `async_ack_id`. Later, when the work completes, POST the final event back to the supervisor.

Two callback body shapes are accepted; the supervisor parses the new shape first and falls back to the legacy shape on a parse error.

**New shape (preferred).** Carries an optional events array plus exactly one terminal:

```
POST ${callback_url}/v1/callback/{async_ack_id}
Content-Type: application/json

{
  "events": [
    { "name": "phase_complete", "payload": "..." }
  ],
  "complete": { "changed": true, "attributes_delta": { ... } }
}
```

Exactly one of `complete`, `blocked`, `errored`, `park_requested` must be set. Events from the array are persisted and processed before the terminal, so an `on_event` handler can fire mid-flight.

**Legacy shape (still accepted).** Single-terminal-event:

```
POST ${callback_url}/v1/callback/{async_ack_id}
Content-Type: application/json

{
  "type": "complete",
  "writeback": { ... }
}
```

Important wire details:

- The callback path is `${callback_url}/v1/callback/{async_ack_id}` — the supervisor's callback hostname (advertised via the `callback.advertise_host` config) plus the async_ack_id.
- For the legacy shape, the body is keyed `type` (not `kind`). The supervisor's callback route enforces this exact key.
- Valid legacy `type` values mirror the streaming-event terminal types: `complete`, `blocked`, `errored`.
- New-shape bodies that include `park_requested` map onto the `Park` terminal event.

The TS claude-agent reference impl's test suite (under `executors/claude-agent/`) covers this exact wire shape; refer to those tests when implementing async-callback in a different language.

## 4a. Resume context

When the supervisor resumes a parked node, the dispatch's `ExecuteRequest.resume_context` is populated with three fields:

- `bytes payload` — the original `Park.payload`.
- `string session_token` — the original `session_token` (executor-side correlation identifier).
- `string resume_reason` — `"deadline_elapsed"` (time-based via `resume_at`) or `"external_invalidate"` (admin or in-graph invalidate).

When `resume_context` is empty, this is a fresh dispatch. Executors that do not implement parking can ignore the field.

## 5. Conformance

The `cmd/rimsky-executor-conformance` binary exercises an executor against the wire-protocol contract. Run it pointing at your executor endpoint:

```
rimsky-executor-conformance --endpoint <your-executor-host:port> --transport grpc
```

For LLM-calling executors, run with `--require-stub-mode`. The conformance harness probes the executor for stub mode at startup; non-stubbed services are rejected. This prevents accidental real-LLM calls during conformance.

## 6. Reference impls

Three reference executors ship under `executors/`:

- `executors/http-node/` — Go executor that calls an external HTTP endpoint. Production-shaped; the right starting point if you are writing your own executor.
- `executors/claude-agent/` — TypeScript / npm executor that runs the Claude Code CLI. Production-shaped; demonstrates the async-callback path end-to-end.
- `executors/stub/` — Test double (Meszaros sense) for scenario tests, conformance, and no-op smoke deployments. **Not a skeleton template** — see `executors/stub/README.md`.

Each is runnable as a standalone process plus a Dockerfile.

## See also

- [`../../.ok-planner/design/concepts/executor.md`](../../.ok-planner/design/concepts/executor.md)
- [`../../.ok-planner/design/concepts/node.md`](../../.ok-planner/design/concepts/node.md)
- [`../../.ok-planner/design/concepts/attribute.md`](../../.ok-planner/design/concepts/attribute.md)
