---
concept: executor
definition: |
  The protocol-level term for the service that runs a node's work. Implements the dispatch protocol `Executor` (one method, `Execute`) and optionally the paired read-only `ExecutorObservability` protocol (`Capabilities`, `GetTrace`, `StreamTrace`). Out-of-process; supervisors dispatch to executors over gRPC, with an HTTP+JSON bridge available for non-Go services.
proto_symbol: Executor in protocols/proto/v1/executor.proto
config_field: rimsky.yml:executors
api_surface: (none)
related: [node, attributes, userdata]
deprecated_terms: []
---

# Executor

## Definition

The protocol-level term for the service that runs a node's work. Implements the dispatch protocol `Executor` (one method, `Execute`) and optionally the paired read-only `ExecutorObservability` protocol (`Capabilities`, `GetTrace`, `StreamTrace`). Out-of-process; supervisors dispatch to executors over gRPC, with an HTTP+JSON bridge available for non-Go services.

## Why it exists

Rimsky's role is orchestration: when to run a node, what claims and locks to acquire, what attributes to substitute, when to declare success. The "what code actually runs" is left to executors. This split keeps Rimsky's responsibilities tight; the system makes no assumptions about the language, runtime, or shape of node-side code.

The executor surface splits into two service definitions:

- **`Executor`** in `protocols/proto/v1/executor.proto`. The required dispatch protocol. One method:
    - **`Execute(ExecuteRequest) → stream<ExecuteEvent>`**: dispatch a node. The executor streams back events: `Heartbeat`, `NamedEvent`, `Complete`, `Error{error_class: "executor_blocked"}`, `Error{error_class}`, `AwaitAsyncCallback`, or `Park`. The supervisor's terminal handling is keyed on the final event type. `NamedEvent` is non-terminal (zero or more per run); `Park` is terminal-but-resumable (the node transitions to `parked` and can be re-dispatched with `ResumeContext`).
- **`ExecutorObservability`** in `protocols/proto/v1/executor_observability.proto`. The optional read-only protocol every executor MAY implement to expose per-dispatch traces to dashboards. Three methods:
    - **`Capabilities(CapabilitiesRequest) → ObservabilityCapabilities`**: startup handshake; declares trace-get/trace-stream support and per-dispatch retention.
    - **`GetTrace(GetTraceRequest) → Trace`**: pull a previously-streamed trace by `dispatch_id`.
    - **`StreamTrace(StreamTraceRequest) → stream<TraceEvent>`**: stream live trace events for an in-flight or recent dispatch.

The async-callback path is for executors whose work outlives a streaming RPC: the executor responds with `AwaitAsyncCallback` and later POSTs to the supervisor's callback URL — `${callback_url}/v1/callback/{async_ack_id}` — with the final event.

An HTTP+JSON bridge is available for languages without convenient gRPC tooling.

## How you encounter it

- **Operator config**: the `executors:` block in `rimsky.yml`. Each entry has `transport`, `endpoint`, `tls`, and an optional `protocols: [...]` list.
- **Implementing an executor**: speak gRPC against `protocols/proto/v1/executor.proto` (required) and optionally `protocols/proto/v1/executor_observability.proto`. Reference impls live under `executors/`: `http-node` (Go, production-shaped) and `claude-agent` (TypeScript / npm, production-shaped). The `stub` directory is a test double in the Meszaros sense (canned-outcome scripting for tests, conformance, and no-op demos) — not a skeleton template; copy from `http-node` or `claude-agent` instead.
- **Conformance**: `cmd/rimsky-executor-conformance` exercises an executor against the wire-protocol contract. For LLM-calling executors, run with `--require-stub-mode` to ensure the conformance run uses stubs (real LLM calls during conformance are rejected by the stub-mode probe).

## Using `Error{error_class: "executor_blocked"}` as a routing signal

An executor may emit `Error{error_class: "executor_blocked"}` when it produced output but explicitly chose not to claim success — for example, low-confidence outputs that should route to human review. This is distinct from `Error{error_class}` for other failure classes. Templates wire the routing receiver-side: the routing node declares `subscribes: [{node: <emitter>, on: state, when: failed, error_class: executor_blocked}]`. See `docs/concepts/handlers.md`, `docs/concepts/error-policy.md`, and `docs/concepts/subscription.md`.

## Consumer-visible guarantees

- The executor receives substituted attribute values verbatim — Rimsky has resolved every `{{...}}` directive in the schema's `source:` fields before dispatch. The executor sees only resolved values.
- `userdata` is opaque bytes — Rimsky never inspects, parses, substitutes, or validates `userdata`. The executor receives the bytes verbatim.
- The async-callback contract is precise: the executor must POST to `${callback_url}/v1/callback/{async_ack_id}` with body keyed `type` (not `kind`). The TS claude-agent reference impl's tests cover this exact wire shape.

## Common mistakes

- Treating `userdata` as a structured input. It's not — Rimsky sends opaque bytes; the executor's contract with the template author is what gives it shape.
- Posting async-callback events with body keyed `kind:` instead of `type:`. The supervisor route rejects the wrong key — the TS claude-agent test suite covers this.
- Running conformance against a non-stubbed LLM-calling executor without `--require-stub-mode`. The probe rejects non-stubbed services.

## See also

- [`node.md`](node.md)
- [`attributes.md`](attributes.md)
- [`userdata.md`](userdata.md)
