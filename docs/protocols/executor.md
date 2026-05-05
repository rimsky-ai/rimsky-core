# Implementing an executor

This guide is for developers implementing an executor — in any language — and wiring it into a Rimsky deployment. The wire contracts live at `protocols/proto/v1/executor.proto` (the required dispatch protocol) and `protocols/proto/v1/executor_observability.proto` (the optional read-only observability protocol); this guide is the practical companion.

<!-- @source: concepts/executor.md -->
> The protocol-level term for the service that runs a node's work. Implements the dispatch protocol `NodeExecutor` (one method, `Execute`) and optionally the paired read-only `ExecutorObservability` protocol (`GetCapabilities`, `GetTrace`, `StreamTrace`). Out-of-process; supervisors dispatch to executors over gRPC, with an HTTP+JSON bridge available for non-Go peers.

> **Auth-blind advisory.** Rimsky has no machinery for credentials, encryption, or access control. Encrypt sensitive bytes before handing them to Rimsky if you need protection. Service-to-service auth is operator-configured at the deployment layer (mTLS, IAM).

---

## 1. The wire contract

The executor surface is split across two service definitions. The required dispatch protocol carries one method:

```protobuf
service NodeExecutor {
  rpc Execute(ExecuteRequest) returns (stream ExecuteEvent);
}
```

Source: `protocols/proto/v1/executor.proto`.

The optional read-only observability protocol carries three:

```protobuf
service ExecutorObservability {
  rpc GetCapabilities(GetCapabilitiesRequest) returns (ObservabilityCapabilities);
  rpc GetTrace(GetTraceRequest) returns (Trace);
  rpc StreamTrace(StreamTraceRequest) returns (stream TraceEvent);
}
```

Source: `protocols/proto/v1/executor_observability.proto`.

Rimsky's supervisor dials the executor at dispatch time and streams events back via `Execute`. Dashboards (and other read-only consumers) dial the executor's observability service to pull or stream per-dispatch traces. Peers MUST implement `NodeExecutor`; `ExecutorObservability` is opt-in but recommended for any executor whose dispatches are interesting to humans.

`Execute` is the load-bearing method. The executor receives an `ExecuteRequest` with substituted attributes plus opaque userdata, and streams back zero or more events ending in one of: `Complete`, `Blocked`, `Errored`, `AsyncAccepted`.

## 2. The methods

### `NodeExecutor.Execute(ExecuteRequest) → stream<ExecuteEvent>`

Dispatch a node. Inside `ExecuteRequest`:

- **node identity** — the supervisor names which (instance, node) it is dispatching.
- **attributes** — already substituted. The executor sees resolved values; the `{{...}}` directives have been resolved by the supervisor.
- **userdata** — opaque bytes copied verbatim from the template.
- **claim contexts** — for each claim the node holds (its own or inherited), the address, payload, and scope made available for executor access.

Stream back any number of these events:

- **`Heartbeat`** — keep-alive while work continues.
- **`Complete`** — terminal success. Three fields:
    - `bool changed` — producer-declared verdict on whether this run produced a different value than the previous run. A `false` value halts cascade propagation at this node.
    - `string change_summary` — free-text summary of the change (audit-log only; not parsed by Rimsky).
    - `Struct attributes_delta` — terminal-final attribute writeback (validated against the node's attributes schema). May be empty when the executor used the incremental-callback path during the run (see spec §12.5).
- **`Blocked`** — terminal: I cannot proceed; the supervisor schedules retry per the node's error policy.
- **`Errored`** — terminal: an application-level error. Two fields: `string error_class` (an executor-defined classifier) plus an opaque `Struct payload`. The executor does NOT pick the resolution. The supervisor's policy chain in the template maps `(error_class, retry_counter)` to one of `retry`, `discard_then_retry`, `resume_then_retry`, `invalidate(targets)`, or `give_up`.
- **`AsyncAccepted`** — non-streaming terminal: I'll send the final event later via callback (see §4).

### `ExecutorObservability.StreamTrace(StreamTraceRequest) → stream<TraceEvent>`

Streaming trace of executor activity for observability dashboards. Keyed by `dispatch_id`.

### `ExecutorObservability.GetTrace(GetTraceRequest) → Trace`

Pull a previously-streamed trace by `dispatch_id`. Useful for replaying past invocations from dashboards.

### `ExecutorObservability.GetCapabilities(GetCapabilitiesRequest) → ObservabilityCapabilities`

Startup handshake for the observability protocol. Declares whether the executor supports trace-get and trace-stream, the per-dispatch retention window, and any custom UI URL the dashboard should embed. Probed once per peer at process startup.

## 3. The userdata guarantee

<!-- @source: concepts/userdata.md -->
> Free-form opaque bytes a template author attaches to a node's executor invocation. Rimsky never inspects, parses, substitutes, or validates `userdata`. The executor receives the bytes verbatim. This is distinct from `attributes`, which are typed, substituted, and schema-validated.

This means:

- A `{{...}}` literal in `userdata` reaches the executor as a literal `{{...}}`.
- The executor's contract with the template author defines what shape userdata must have. Rimsky is uninvolved.
- Encrypted userdata stays encrypted in transit. Decryption is the executor's responsibility at point of use.

## 4. The async-callback path

For executors whose work outlives a streaming RPC (background jobs, async LLM calls, long-running batch processes), respond with `AsyncAccepted` carrying an `async_ack_id`. Later, when the work completes, POST the final event back to the supervisor:

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
- The body is keyed `type` (not `kind`). The supervisor's callback route enforces this exact key.
- Valid `type` values mirror the streaming-event terminal types: `complete`, `blocked`, `errored`.

The TS claude-agent reference impl's test suite (under `executors/claude-agent/`) covers this exact wire shape; refer to those tests when implementing async-callback in a different language.

## 5. Conformance

The `cmd/rimsky-conformance` binary exercises an executor against the wire-protocol contract. Run it pointing at your executor endpoint:

```
rimsky-conformance --endpoint <your-executor-host:port> --transport grpc
```

For LLM-calling executors, run with `--require-stub-mode`. The conformance harness probes the executor for stub mode at startup; non-stubbed peers are rejected. This prevents accidental real-LLM calls during conformance.

## 6. Reference impls

Three reference executors ship under `executors/`:

- `executors/http-node/` — Go executor that calls an external HTTP endpoint.
- `executors/claude-agent/` — TypeScript / npm executor that calls Anthropic's API. Uses the async-callback path.
- `executors/stub/` — Go test fixture; returns canned responses.

Each is runnable as a standalone process plus a Dockerfile.

## See also

- [`../concepts/executor.md`](../concepts/executor.md)
- [`../concepts/node.md`](../concepts/node.md)
- [`../concepts/attributes.md`](../concepts/attributes.md)
- [`../concepts/userdata.md`](../concepts/userdata.md)
