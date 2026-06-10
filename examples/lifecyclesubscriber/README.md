# examples/lifecyclesubscriber — Reference Lifecycle Subscriber

A minimal, copy-and-modify Go LifecycleSubscriber that boots as a gRPC
server and receives the seven lifecycle callbacks rimsky fires as
templates and instances move through their lifecycle.

This module is **Apache 2.0** (the protocols / examples permissive
surface) so you can fork it, rename the module in `go.mod`, and ship a
custom lifecycle subscriber without inheriting any AGPL obligations
from the rimsky orchestrator itself.

## What this example exhibits

A real custom lifecycle subscriber implements all seven callbacks and
receives each one synchronously at the corresponding lifecycle
transition. Rimsky carries the relevant context on each callback so
the subscriber can react without a separate read against rimsky's
state. This example covers each callback in turn:

- **`OnTemplateRegistered`** — fires when an operator registers a new
  template. Carries the template content-hash AND the canonical
  JCS-canonicalized spec bytes so the subscriber sees exactly what
  rimsky hashed.
- **`OnTemplateDeployed`** — fires when a template is marked deployable.
  Carries the template hash and the set of tags now bound to it.
- **`OnTemplateUndeployed`** — fires when a template is marked
  un-deployable. Carries the template hash.
- **`OnTemplateDeregistered`** — fires when a template row is removed.
  Carries the template hash.
- **`OnInstanceCreated`** — fires when an instance is created against a
  deployed template. Carries the instance id, the template hash, the
  instance-key (may be empty), the params payload, the per-instance
  late-bound `service_bindings` catalog (may be empty), and the
  `owner_api_key_id` of the api-key that authenticated the create
  request (empty for anonymous-mode creates).
- **`OnInstanceTerminated`** — fires when an instance is deleted (after
  it has reached terminal state). Carries the instance id, the
  template hash, and the row's `terminated_at` in Unix milliseconds.
- **`OnRunScopeTerminal`** — fires when a run-scope closes (main, sub-
  graph, or fan-out partition). Carries the run-scope id, the terminal
  reason, and the owning instance id.

Rimsky honors the subscriber's response **synchronously**: returning a
non-nil error from any callback surfaces the failure on the triggering
HTTP request (5xx with the per-store details), and the upstream
mutation does NOT proceed. This is the load-bearing property the
seventh acceptance leg in `main_e2e_test.go` exhibits — a subscriber
that returns an error on `OnTemplateRegistered` causes the
`POST /v1/templates` to fail with 5xx, not silently succeed.

## File layout

| File                       | What it is                                                                                  |
| -------------------------- | ------------------------------------------------------------------------------------------- |
| `subscriber.go`            | The Subscriber type and its seven RPCs. Read this first; it carries the full wiring contract. |
| `main.go`                  | The binary entry point — `Listen` + `RunGRPC` lifecycle, including the HTTP+JSON bridge.    |
| `subscriber_test.go`       | Fast in-process tests pinning a representative subset of the ack contract.                  |
| `main_e2e_test.go`         | Cross-stack proof — boots a real rimsky-all-in-one stack and exhibits all seven callbacks plus the synchronous-failure property. |
| `go.mod` / `go.sum`        | Stand-alone Go module; the build-time dep is `lib/protocols` (the wire contract); the test-only deps add `lib/services/test/harness` for the cross-stack proof and never reach a consumer's `go build`. |

## Running the subscriber

```
go run ./examples/lifecyclesubscriber
```

The binary binds to `0.0.0.0:9500` (gRPC). For non-Go consumers,
serverkit also exposes an HTTP+JSON bridge for this protocol — mount
it on a second listener; see the comments in `main.go`.

## Wiring the subscriber into a running rimsky

Add a `claim_producers:` or `executors:` entry to `rimsky.yml` whose
`protocols:` list includes `lifecycle_subscriber`. The subscriber
peer rides on either entry kind; rimsky fans out lifecycle events to
every peer the referencing template names in a node's `stores:` list
or `executor:` field. A subscriber that only wants to react to
events (and does no claim production or execution) advertises a
minimal Capabilities response: `write_semantics_allowed: [sync]` on
the claim-producer side, or no observability schema on the executor
side. See `main_e2e_test.go` for the test-side wiring this example
uses.

## Cross-stack proof

`main_e2e_test.go` is the executable proof for
`STORY-lifecycle-subscriber-author`. It boots `rimsky-all-in-one`
under testcontainers, wires the example Subscriber as a peer service
on a host port (with a minimal recording wrapper that delegates every
callback to the example), drives a template through the seven-callback
walk (`register → deploy → instance-create → terminate → instance-
delete → undeploy → deregister`), asserts each callback's documented
context fields are populated, and verifies the synchronous-failure
property by injecting a subscriber error on `OnTemplateRegistered`
and asserting the POST surfaces as 5xx.

Build requirement: `make core-images` must have produced
`rimsky-all-in-one:latest` locally. Without it, the test fails hard
at bring-up with the missing-image error.

Run:

```
go test ./examples/lifecyclesubscriber -run TestE2E -count=1
```
