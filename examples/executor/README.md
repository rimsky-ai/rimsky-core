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
templates, route declared errors, and emit named events on the unified
event log. This example covers each protocol surface:

- **Dispatch** — `Execute` is a server-streaming RPC. The example sends
  one optional Heartbeat then exactly one terminal `StreamClose`. The
  three terminal outcomes (`Success`, `Error`, `Park`,
  `AwaitAsyncCallback`) are all built the same way; the example branches
  on the resolved `mode` attribute to demonstrate Success vs. declared
  Error vs. NamedEvent-then-Success.
- **Capabilities handshake** — `ExecutorObservability.Capabilities`
  advertises three load-bearing fields rimsky reads at startup:
  - `expected_attributes_schema`: a JSON Schema rimsky merges with the
    template's `attributes:` block. Used by the registration-time
    validator (mode `all`/`available`) to refuse a template whose
    static defaults violate the executor's value constraints.
  - `declared_events`: the set of `NamedEvent.name` values the executor
    may emit. Rimsky validates emissions at the supervisor and validates
    template `subscribes: [{type: event/<name>}]` references at
    registration.
  - `declared_error_classes`: the set of `Error.error_class` values the
    executor may surface. Operator `error_types:` policy keys are
    range-checked against this set so a typo can't silently no-op a
    policy chain.

## File layout

| File                  | What it is                                                                                  |
| --------------------- | ------------------------------------------------------------------------------------------- |
| `executor.go`         | The Executor type and its three RPCs. Read this first; it carries the full wiring contract. |
| `main.go`             | The binary entry point — `Listen` + `RunGRPC` lifecycle.                                    |
| `executor_test.go`    | Fast in-process tests pinning the dispatch happy path + declared-class + named-event modes. |
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
frame_resolution_mode: serial_queue
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
the protocol directly via gRPC — no Docker, no rimsky stack. The three
tests pin the dispatch contract:

- `TestExecuteReturnsSingleSuccessTerminal` — exactly one `StreamClose`
  with `Success`, plus non-empty `expected_attributes_schema`,
  `declared_events`, `declared_error_classes`.
- `TestExecute_RaiseErrorEmitsDeclaredClass` — `mode: raise_error`
  terminates with `Error.error_class = "example/forbidden"`.
- `TestExecute_EmitEventEmitsDeclaredName` — `mode: emit_event` emits a
  `NamedEvent` named `work_started` before the Success terminal.

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
2. **NamedEvent appears on the event log.** A template whose worker
   carries `mode: emit_event` causes the executor to emit a NamedEvent
   named `work_started` before the Success terminal; the supervisor
   persists it on `rimsky_events` as kind `event/work_started`, visible
   via `GET /v1/events?kind=event/work_started`.
3. **Declared error class routes through `error_types:`.** A template
   whose worker declares `error_types: { example/forbidden: { policy:
   [give_up] } }` and carries `mode: raise_error` causes the executor
   to emit `Error{error_class: example/forbidden}`; rimsky routes the
   give_up action through the declared chain and emits the canonical
   signal `terminal/error/example/forbidden` on the event log.
4. **Attribute schema validates at registration.** A template whose
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
the four legs against the SAME running stack (single bring-up,
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
4. Adjust `Capabilities` to advertise your real schema, event names,
   and error classes — the three handshake fields are the contract
   rimsky reads at startup.
5. Drop `executor_test.go` and `main_e2e_test.go` if they no longer
   match your shape, or adapt them as a starting point for your own
   tests.

The Apache license file (`../../LICENSE.apache`) covers the example
itself; your fork inherits Apache 2.0 unless you explicitly relicense
it.
