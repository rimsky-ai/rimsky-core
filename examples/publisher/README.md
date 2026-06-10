# examples/publisher — Reference Publisher

A minimal, copy-and-modify Go Publisher that boots as a gRPC server and serves
the rimsky publisher protocol end-to-end.

This module is **Apache 2.0** (the protocols / examples / claude-agent
permissive surface) so you can fork it, rename the module in `go.mod`,
and ship a custom publisher without inheriting any AGPL obligations from
the rimsky orchestrator itself.

## What this example exhibits

A real custom publisher (or sensor) isn't just a Subscribe handler — it
advertises its message kinds at startup, tracks per-instance subscriptions
so rimsky can reconcile after a restart, and POSTs messages into rimsky
through the universal message endpoint with the mandatory dedup header.
This example covers each protocol surface:

- **Subscribe / Unsubscribe / ListSubscriptions** — the gRPC `Publisher`
  service handlers. Subscribe records a per-instance subscription;
  Unsubscribe forgets one; ListSubscriptions enumerates the live set so
  rimsky's startup `ResyncPublisherSubscriptions` can reconcile its own
  view against the publisher's. The example uses a mutex-guarded
  in-memory map; a real publisher persists subscriptions so a process
  restart doesn't drop them.
- **Capabilities** — advertises the publisher kinds this service emits.
  rimsky validates a template's `publishers:` kind references against
  this set; an unadvertised kind is refused at template registration.
- **Message emit through `POST /v1/instances/{id}/messages`** — message
  emission is NOT part of the gRPC surface. The publisher POSTs to
  rimsky's universal message endpoint with `sender_kind: "publisher"`,
  the captured `publisher_subscription_id`, and the mandatory
  `Idempotency-Key` HTTP header (any emission without the header is
  refused at the request boundary; the dedup guarantee is platform-
  enforced, not a publisher convention). The bundled
  `lib/protocols/publisherkit` carries the canonical retry + idempotency
  envelope for both the bundled sensors and any third-party publisher.

## File layout

| File                  | What it is                                                                                  |
| --------------------- | ------------------------------------------------------------------------------------------- |
| `publisher.go`        | The Publisher type and its three RPCs plus the per-call counters / `SubscriptionIDs()` test hook the cross-stack proof uses. Read this first; it carries the full wiring contract. |
| `main.go`             | The binary entry point — `Listen` + `RunGRPC` lifecycle.                                    |
| `publisher_test.go`   | Fast in-process test pinning the subscription lifecycle (Capabilities → Subscribe → ListSubscriptions → Unsubscribe). |
| `main_e2e_test.go`    | Cross-stack proof — boots a real rimsky-all-in-one stack and exhibits every surface above, including the restart-time `ListSubscriptions` reconcile that the spec's falsifier guards. |
| `go.mod` / `go.sum`   | Stand-alone Go module; the build-time dep is `lib/protocols` (the wire contract); the test-only deps add `lib/services/test/harness` for the cross-stack proof and never reach a consumer's `go build`. |

## Running the publisher

The binary listens on TCP `:9600` by default; override with
`EXAMPLE_PUBLISHER_PORT` (the example currently hard-codes 9600; rename
the constant in `main.go` to plug in env-driven binding).

```sh
cd examples/publisher
go run .                               # listens on :9600
```

Point rimsky at the publisher by registering it in your `rimsky.yml`:

```yaml
publishers:
  example:
    endpoint: "127.0.0.1:9600"
    protocols: [publisher]
```

A template references the publisher by the same name and declares the
target node:

```yaml
name: my-publisher-driven-workflow
version: "1"
frame_resolution_mode: serial_queue
nodes:
  - type: reactor
    executor: my-executor
    subscribes:
      - instance: true
        type: message/invalidate/publisher/reactor
        frame: in
publishers:
  - name: example
    kind: example
    config: {}
    target_node: reactor
    message_kind: invalidate
```

When the instance is created, rimsky generates a
`publisher_subscription_id`, inserts a row into
`rimsky_publisher_subscriptions`, and calls the publisher's `Subscribe`
RPC with that id plus the resolved per-instance config. The publisher
then starts watching whatever source the config describes and POSTs
messages to `POST /v1/instances/{id}/messages` whenever it fires.

## In-process tests

`publisher_test.go` stands up the Publisher on a loopback port and
drives the protocol directly via gRPC — no Docker, no rimsky stack. The
test pins the subscription lifecycle:

- `TestSubscribeListUnsubscribe` — Capabilities advertises a kind,
  Subscribe records a subscription, ListSubscriptions returns it, and
  Unsubscribe removes it.

Run them:

```sh
cd examples/publisher
go test -count=1 ./...
```

## Cross-stack walkthrough

`main_e2e_test.go` is the cross-stack proof for the
`STORY-publisher-protocol` user-outcome story. It boots a real
`rimsky-all-in-one` container (testcontainers; Postgres state DB),
registers the example publisher AND a tiny inline stub executor on
host ports via `testcontainers.WithHostPortAccess`, and exhibits each
protocol surface end-to-end against the assembled product:

1. **Subscribe lands.** Creating an instance against a template whose
   `publishers:` block references the example publisher's kind causes
   rimsky to call `PublisherClient.Subscribe` synchronously inside the
   instance-create flow. The publisher's `subscribeCalls` counter
   exposed via `Calls()` is the load-bearing observable.
2. **Messages reach the targeted instance through the dedup header.**
   The publisher POSTs a message envelope to
   `POST /v1/instances/{id}/messages` with the mandatory
   `Idempotency-Key` header, `sender_kind: "publisher"`, and the
   `publisher_subscription_id` capability token. The downstream
   reactor node subscribing to
   `message/invalidate/publisher/reactor` fires through the real
   cascade — observable as the reactor's `work_started` count growing.
   The persisted message carries `sender_kind=publisher` with `sender`
   derived from the publisher subscription's `publisher_name` (rimsky
   overwrites the request body's `sender` for trust).
3. **The dedup header is mandatory.** A POST without the
   `Idempotency-Key` header is refused at the request boundary with
   HTTP 400; the diagnostic names the missing header so an operator
   knows what to fix. The dedup guarantee cannot be silently bypassed.
4. **Restart-time reconcile uses `ListSubscriptions`.** After rimsky
   is restarted (the harness's `RimskyHandle.Restart` method tears
   down the rimsky/all container and brings up a fresh one against
   the same Postgres state DB), the new control-api fires
   `ResyncPublisherSubscriptions` against every configured publisher.
   `ListSubscriptions` is called and the publisher reports the still-
   live subscription it has been holding; the reconcile sweep
   observes the subscription as already-present and does NOT
   re-issue `Subscribe`. The publisher's `Calls()` snapshot before
   and after the restart proves `ListSubscriptions` grew AND
   `Subscribe` did not.

### Prerequisites

The harness pulls `rimsky-all-in-one:latest` from the local Docker
daemon (nothing is fetched from a registry). Build the image first:

```sh
make core-images
```

Then run the cross-stack proof:

```sh
cd examples/publisher
go test -run TestE2E -count=1 -v -timeout 600s .
```

The test brings up testcontainer Postgres + rimsky-all-in-one,
restarts rimsky once mid-test, and runs the four legs against the
running stack (~20 s total wall time on a warm docker cache).

### How the harness wires the publisher

The example publisher is run as an **in-process gRPC server on a host
port**, not as a Docker container — the same pattern the executor
example uses. The rimsky container reaches it via testcontainers's
SSH tunnel:

```text
                                                  ┌──────────────────────────┐
┌────────────────────┐    "host.testcontainers   │  rimsky-all-in-one (ctr)  │
│ example Publisher  │←── .internal:<pubPort>" ──│  control-api → Subscribe  │
│ (host, in-process) │                            │  + reconcile.ListSubs    │
└────────────────────┘                            └──────────────────────────┘
        ^                                          ▲                  ▲
        │ go test                                  │                  │
        │                                          │ POST messages    │ Postgres state DB
┌────────────────────┐                             │                  │
│ main_e2e_test.go   │ ── POST /v1/instances/.../  │                  │
└────────────────────┘    messages w/ dedup hdr ───┘                  ▼
                                                          ┌──────────────────┐
                                                          │ postgres:15-alpine│
                                                          │ (testcontainer)   │
                                                          └──────────────────┘
```

The test ALSO stands up a tiny inline stub executor on a second host
port so the reactor node has a reachable executor to dispatch through
— the load-bearing observable is "did the publisher emit cause a NEW
dispatch on the subscribing node", not "did the dispatch do real work".

The restart leg uses the harness's `RimskyHandle.Restart` method,
which terminates the rimsky/all container and brings up a fresh one
with the same config + peer wiring against the same Postgres state
DB. The publisher's gRPC server (and its in-memory subscription
registry) survive the restart — that's what lets the second
`ListSubscriptions` call report the same subscription id the first
boot recorded.

## Migrating from this example

1. Copy `examples/publisher/` into your own repo.
2. Rename the module in `go.mod`.
3. Replace the in-memory subscription registry with whatever watches
   your source (a cron timer, an HTTP poller, an object-store
   notifier, an inbound webhook listener) and POSTs to
   `POST /v1/instances/{id}/messages` when it fires. Use
   `pkg:github.com/rimsky-ai/rimsky-core/lib/protocols/publisherkit`
   for the universal retry-with-idempotency-header envelope so your
   publisher matches the bundled sensors' behavior.
4. Adjust `Capabilities` to advertise the real message kinds your
   publisher emits. rimsky's template validator will refuse a
   template whose `publishers:` block names a kind your service
   doesn't advertise.
5. Drop the per-call counters (`subscribeCalls` / `unsubscribeCalls`
   / `listSubscriptionsCalls`) and the `Calls()` / `SubscriptionIDs()`
   test hooks if you don't need them — they exist solely for this
   directory's `main_e2e_test.go` proof and a production publisher
   would expose comparable signals through its own metrics surface.
6. Drop `publisher_test.go` and `main_e2e_test.go` if they no longer
   match your shape, or adapt them as a starting point for your own
   tests.

The Apache license file (`../../LICENSE.apache`) covers the example
itself; your fork inherits Apache 2.0 unless you explicitly relicense
it.
