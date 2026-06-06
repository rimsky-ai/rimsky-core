# rimsky protocol examples

Minimal, **copy-and-modify** servers — one per rimsky protocol a consumer
implements. Each is the smallest thing that registers, speaks its protocol
correctly, and is exercised by a test. They exist to carry the Go wiring that
prose can't: the generated import path, the streaming-server method signatures,
how to build the protobuf `oneof` terminals, and the startup-handshake answers
the supervisor requires.

These are **not** test doubles (rimsky's own test stub lives at
`test/support/executors/stub/`) and **not** deployable services. Copy a
directory, rename the module, and replace the body with your work.

## License

This `examples/` module is **Apache-2.0**, like `lib/protocols/` (the wire
contract) — so you may copy it into your own project freely. The rest of
rimsky-core is AGPL; these examples deliberately depend only on the Apache
`lib/protocols` module and stdlib + permissive third-party packages.

## Layout

| Directory | Protocol | Guarantee |
|---|---|---|
| `executor/` | `Executor` (+ `ExecutorObservability` handshake) | in-process gRPC behavioral test |
| `claimproducer/` | `ClaimProducer` (read-only) | in-process gRPC behavioral test |
| `lifecyclesubscriber/` | `LifecycleSubscriber` | behavioral test |
| `publisher/` | `Publisher` (in-memory subscriptions) | in-process gRPC behavioral test |

To run a copied example against the real conformance harness once you've filled
in your logic, see `rimsky conformance executor` (and the Go libraries under
`lib/protocols/conformance/`).

## Building in-tree

This module is part of the workspace (`go.work`), so the repo gate covers it:
`go build ./...`, `go test ./...`, and `cd examples && golangci-lint run`.
