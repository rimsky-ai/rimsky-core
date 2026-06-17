<!-- @story: executor-protocol -->
# examples/executor — Reference Executor

A minimal, copy-and-modify Go Executor that boots as a gRPC server and serves
the rimsky executor protocol end-to-end.

This module is **Apache 2.0** (the protocols / examples / claude-agent
permissive surface) so you can fork it, rename the module in `go.mod`,
and ship a custom executor without inheriting any AGPL obligations from
the rimsky orchestrator itself.

## What this example exhibits

A real custom executor isn't just an Execute RPC — it advertises its
contract surface to rimsky at startup so the orchestrator can gate
templates, route declared errors, and surface tags on the unified
event log. This example covers each protocol surface:

- **Dispatch** — `Execute` is a unary RPC. The example returns
  exactly one `Outcome` carrying one of the four variants
  (`Success`, `Error`, `Park`, `AwaitAsyncCallback`). The
  example branches on the resolved `mode` attribute to demonstrate
  Success vs. declared Error vs. tagged-Success.
- **Capabilities handshake** — `ExecutorObservability.Capabilities`
  advertises three load-bearing fields rimsky reads at startup:
  - `expected_attributes_schema`: a JSON Schema rimsky merges with the
    template's `attributes:` block. Used by the registration-time
    validator (mode `all`/`available`) to refuse a template whose
    static defaults violate the executor's value constraints.
  - `declared_tags`: the set of tag names this executor may include
    on the `tags` field of a settling terminal verdict (Success /
    Error / Park). Rimsky rejects emissions of undeclared names at
    the supervisor's terminal handler and validates template
    `subscribes: [{type: terminal/*, when: "<tag>" in payload.tags}]`
    references at registration.
  - `declared_error_classes`: the set of `Error.error_class` values the
    executor may surface. Operator `error_types:` policy keys are
    range-checked against this set so a typo can't silently no-op a
    policy chain.

## File layout

| File                  | What it is                                                                                  |
| --------------------- | ------------------------------------------------------------------------------------------- |
| `executor.go`         | The Executor type and its two RPCs. Read this first; it carries the full wiring contract. |
| `main.go`             | The binary entry point — `Listen` + `RunGRPC` lifecycle.                                    |
| `executor_test.go`    | Fast in-process tests pinning the dispatch happy path + declared-class + tagged-Success modes. |
| `main_e2e_test.go`    | Cross-stack proof — boots a real rimsky-all-in-one stack and exhibits every surface above.  |
| `go.mod` / `go.sum`   | Stand-alone Go module; the build-time dep is `lib/protocols` (the wire contract); the test-only deps add `lib/services/test/harness` for the cross-stack proof and never reach a consumer's `go build`. |

## Running the executor

The binary listens on TCP `:9300` by default; override with
`EXAMPLE_EXECUTOR_PORT`.

```sh
cd examples/executor
go run .                               # listens on :9300
EXAMPLE_EXECUTOR_PORT=9999 go run .    # listens on :9999
```

Point rimsky at the executor by registering it in your `rimsky.yml`:

```yaml
executors:
  example:
    transport: grpc
    endpoint: "127.0.0.1:9300"
    tls: off
    protocols: [executor]
```

A template node references the executor by the name above:

```yaml
name: my-pipeline
version: "1"
nodes:
  - type: worker
    executor: example
    attributes:
      schema:
        type: object
        properties:
          mode:
            type: string
            default: ok
```

## In-process tests

`executor_test.go` stands up the Executor on a loopback port and drives
the protocol directly via gRPC — no Docker, no rimsky stack. The tests
pin the dispatch contract:

- `TestExecuteReturnsSuccessOutcome` — exactly one `Outcome{Success}`,
  plus non-empty `expected_attributes_schema`, `declared_tags`,
  `declared_error_classes`.
- `TestExecute_RaiseErrorEmitsDeclaredClass` — `mode: raise_error`
  settles with `Outcome{Error}` carrying
  `error_class = "example/forbidden"`.
- `TestExecute_EmitEventEmitsDeclaredTag` — `mode: emit_event` settles
  with `Outcome{Success}` whose `tags` carries `work_started`.
- `TestExecute_AsyncMode_ReturnsAwaitAsyncCallback` — `mode:
  async_callback` with a supplied `async_ack_id` attribute settles
  with `Outcome{AwaitAsyncCallback}` carrying that exact ack id, ready
  for the supervisor to persist on
  `col:rimsky_node_runs.async_ack_id`.
- `TestExecute_AsyncMode_MissingAckIDSurfacesError` — the same mode
  with an empty `async_ack_id` surfaces an Error rather than getting
  stuck against a non-correlatable empty registry row.

Run them:

```sh
cd examples/executor
go test -count=1 ./...
```

## Cross-stack walkthrough

`main_e2e_test.go` is the cross-stack proof for the
`STORY-executor-protocol` user-outcome story. It boots a real
`rimsky-all-in-one` container (testcontainers; Postgres state DB),
registers the example executor on a host port via
`testcontainers.WithHostPortAccess`, and exhibits each protocol surface
end-to-end against the assembled product:

1. **Execute is dispatched.** A template referencing the example
   executor produces an instance whose worker node settles to `fresh`
   through the real supervisor — proof the supervisor dialed the
   executor at the advertised endpoint and ran a real dispatch.
2. **Tag appears on the event log.** A template whose worker carries
   `mode: emit_event` causes the executor to return Success carrying
   `tags: ["work_started"]`; the supervisor persists the
   `terminal/success` event row with `payload.tags`
   including `work_started`, visible via
   `GET /v1/events?kind=terminal/success`. A downstream subscriber
   declared `subscribes: [{node: worker, type: terminal/success, when:
   "work_started" in payload.tags}]` dispatches in the same frame —
   proving end-to-end tag-keyed cascade.
3. **Declared error class routes through `error_types:`.** A template
   whose worker declares `error_types: { example/forbidden: { policy:
   [give_up] } }` and carries `mode: raise_error` causes the executor
   to emit `Error{error_class: example/forbidden}`; rimsky routes the
   give_up action through the declared chain and emits the canonical
   signal `terminal/error/example/forbidden` on the event log.
4. **Async-callback registration + delivery + persistent-registry
   survival across supervisor restart.** A template whose worker
   carries `mode: async_callback` and a known `async_ack_id` attribute
   causes the executor to return `Outcome{AwaitAsyncCallback}`
   synchronously; the supervisor persists the ack id to
   `col:rimsky_node_runs.async_ack_id` in tx with the
   `transient/await_async` signal emit (the production wiring runs in
   `code:lib/runtime/runner_dispatch.go::registerAsyncIfSet` and the
   columns are surfaced on `GET /v1/observability/node-runs`). After
   confirming the persisted ack id, the test calls
   `RimskyHandle.Restart()` — terminating the rimsky-all-in-one
   container and bringing up a fresh one against the SAME Postgres
   state DB. The fresh supervisor's in-memory `code:CallbackRegistry`
   is empty; when the test POSTs `AsyncCallbackBody{success:{...}}` to
   the post-restart `route:POST /v1/callback/{async_ack_id}`, the
   handler falls through to `code:Queue.LookupRunByAsyncAckID` against
   the persisted column, correlates the dispatch row, and drives the
   verdict to `terminal/success`. The node reaches `fresh`. This is
   the Falsifier-named "async-callback POST is dropped after the
   supervisor that registered it restarts" leg of
   STORY-executor-protocol.
5. **Attribute schema validates at registration.** A template whose
   worker carries a static default `count: -1` violates the executor's
   advertised `count.minimum: 0` constraint; rimsky's registration-time
   validator (default mode `all`) refuses the template registration
   with HTTP 400, citing the offending attribute and the violated
   constraint.

### Prerequisites

The harness pulls `rimsky-all-in-one:latest` from the local Docker
daemon (nothing is fetched from a registry). Build the image first:

```sh
make core-images
```

Then run the cross-stack proof:

```sh
cd examples/executor
go test -run TestE2E -count=1 -v -timeout 600s .
```

The test brings up testcontainer Postgres + rimsky-all-in-one and runs
the five legs against the SAME running stack (single bring-up,
~60-90 s total wall time depending on Docker layer cache).

### How the harness wires the executor

The example executor is run as an **in-process gRPC server on a host
port**, not as a Docker container. The rimsky container reaches it via
testcontainers's SSH tunnel:

```text
                                                  ┌──────────────────────────┐
┌────────────────────┐    "host.testcontainers   │  rimsky-all-in-one (ctr)  │
│ example Executor   │←── .internal:<port>" ─────│  supervisor → executor    │
│ (host, in-process) │                            │  observability handshake │
└────────────────────┘                            └──────────────────────────┘
        ^
        │ go test                                 ▲
        │                                         │ Postgres state DB
        │                                         │
┌────────────────────┐                            │
│ main_e2e_test.go   │                            │
└────────────────────┘                            ▼
                                          ┌──────────────────┐
                                          │ postgres:15-alpine│
                                          │ (testcontainer)   │
                                          └──────────────────┘
```

This avoids a per-test Docker build for the example binary while still
exercising the real value path through the assembled rimsky stack.

## Migrating from this example

1. Copy `examples/executor/` into your own repo.
2. Rename the module in `go.mod`.
3. Replace the body of `Execute` with your work.
4. Adjust `Capabilities` to advertise your real schema, tags,
   and error classes — the three handshake fields are the contract
   rimsky reads at startup.
5. Drop `executor_test.go` and `main_e2e_test.go` if they no longer
   match your shape, or adapt them as a starting point for your own
   tests.

The Apache license file (`../../LICENSE.apache`) covers the example
itself; your fork inherits Apache 2.0 unless you explicitly relicense
it.
