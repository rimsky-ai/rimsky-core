# examples/claimproducer — Reference ClaimProducer

A minimal, copy-and-modify Go ClaimProducer that boots as a gRPC server and
serves the rimsky claim-producer protocol end-to-end.

This module is **Apache 2.0** (the protocols / examples / claude-agent
permissive surface) so you can fork it, rename the module in `go.mod`,
and ship a custom claim-producer without inheriting any AGPL obligations
from the rimsky orchestrator itself.

## What this example exhibits

A real custom claim-producer isn't just an Open RPC — it advertises its
write-semantics envelope to rimsky at startup so the orchestrator can
gate registrations, handles the terminal verbs Commit / Abandon /
Release so rimsky's terminal pipeline can settle claims through it, and
returns JSON-encoded address bytes so rimsky's `json.RawMessage`-typed
`address` column can persist them. The example covers each protocol
surface:

- **Capabilities handshake.** `Capabilities` advertises a single
  `write_semantics_allowed` entry — `WRITE_SEMANTICS_READ_ONLY`. The
  operator's `rimsky.yml` per-producer `write_semantics_allowed:` block
  MUST be a non-empty subset of this set; rimsky's startup config load
  (`peer.Dial + Client.ValidateCapabilities`) refuses any operator
  envelope claiming a semantics the producer never advertised, citing
  "capabilities mismatch". The example does NOT advertise `SplitScope`
  or `ScopesConflict`, so rimsky never calls them.
- **Open.** Returns the `Acquired` arm of the `OpenResponse` oneof with
  a JSON-encoded address (the claim_id, JSON-quoted). The bytes are
  opaque to rimsky per `@blessed-invariant 20`, but they MUST be
  syntactically-valid JSON — rimsky's `address` column is
  `json.RawMessage` on both Postgres and SQLite, and invalid JSON
  surfaces as SQLSTATE 22P02 at the supervisor's acquireClaim step.
- **Commit.** No-op acknowledgement for a read-only producer; a
  stateful producer (queue, store) does its real settlement here. The
  RPC handler bumps an in-memory counter so the cross-stack proof can
  assert the verb really landed.
- **Abandon.** Symmetric no-op for the failure path.
- **Release.** Called by rimsky's `ReleaseHeldDurableClaims` at
  instance terminate against held durable claims (asset pattern). The
  example is not data-processing-capable so rimsky never wires it
  through that path; the cross-stack proof exhibits the verb by
  driving the producer's RPC handler directly through the same
  `lib/runtime/peer.Client.Release` call site rimsky's supervisor
  uses on a held-durable terminate.

## File layout

| File                       | What it is                                                                                                                                             |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `claimproducer.go`         | The `Producer` type and its five RPCs. Read this first; it carries the full wiring contract.                                                           |
| `main.go`                  | The binary entry point — `Listen` + `RunGRPC` lifecycle.                                                                                               |
| `claimproducer_test.go`    | Fast in-process tests pinning the Open happy path + Capabilities surface.                                                                              |
| `main_e2e_test.go`         | Cross-stack proof — boots a real `rimsky-all-in-one` stack and exhibits every protocol surface end-to-end.                                              |
| `Dockerfile.example`       | Build recipe used by `main_e2e_test.go` via `testcontainers.FromDockerfile`. Never published as a product image.                                        |
| `go.mod` / `go.sum`        | Stand-alone Go module; the build-time dep is `lib/protocols` (the wire contract); the test-only deps add `lib/services/test/harness` for the cross-stack proof and never reach a consumer's `go build`. |

## Running the producer

The binary listens on TCP `:9400` by default (the port `Dockerfile.example`
exposes). To change the bind address, edit `main.go` — the example uses
the literal port directly rather than reading an env var, to keep the
copy-and-modify surface minimal.

```sh
cd examples/claimproducer
go run .                               # listens on :9400
```

Point rimsky at the producer by registering it in your `rimsky.yml`:

```yaml
claim_producers:
  example:
    endpoint: "127.0.0.1:9400"
    protocols: [claim_producer]
    # MUST be a non-empty subset of the producer's
    # Capabilities.write_semantics_allowed envelope. The example
    # advertises only `read_only`, so anything else here (sync /
    # staged_async / blocking_async) is refused at startup with
    # "capabilities mismatch".
    write_semantics_allowed: [read_only]
```

A template node references the producer by the name above:

```yaml
name: my-pipeline
version: "1"
frame_resolution_mode: serial_queue
nodes:
  - type: worker
    executor: my-executor
    stores:
      - name: example
        selector: r1            # opaque to rimsky; the producer parses it
        intent: r               # must be "r" or "rw"; the example
                                # advertises read_only, so "r" is the
                                # only honest intent here
        alias: claim
```

## In-process tests

`claimproducer_test.go` stands up the `Producer` on a loopback port and
drives the protocol directly via gRPC — no Docker, no rimsky stack:

- `TestOpenReturnsAcquired` — Open returns the `Acquired` arm with a
  non-empty address; Capabilities advertises a non-empty
  `write_semantics_allowed`.

Run it:

```sh
cd examples/claimproducer
go test -count=1 ./...
```

## Cross-stack walkthrough

`main_e2e_test.go` is the cross-stack proof for the
`STORY-claim-producer-protocol` user-outcome story. It builds the
example's Docker image on demand from `Dockerfile.example`, brings up
the producer on the shared docker network alongside two stub executors
(success + error), then boots a real `rimsky-all-in-one` container
(testcontainers; Postgres state DB) wired to all three peers, and
exhibits each protocol surface against the assembled product:

1. **Open + Commit on success.** A template referencing the example
   producer (intent: r, the producer's only honest intent — it
   advertises read_only) on the success executor stub drives a
   dispatch through the real supervisor. The node settles to `fresh`,
   and a `claim_resolution.commit` event lands on `/v1/events` for the
   instance with `producer_name=example` — proof rimsky's terminal
   pipeline called the producer's Commit RPC and the RPC returned
   successfully (the event is emitted by
   `lib/runtime/terminal_decision_forensics.go::emitTerminalForensics`
   AFTER the verb succeeds).
2. **Open + Abandon on failure.** A second template references the
   same producer on the erroring executor stub
   (`stub/forced_error` → give_up policy). The node settles to
   `failed`, and a `claim_resolution.abandon` event lands on
   `/v1/events` for the instance — proof rimsky's terminal pipeline
   called the producer's Abandon RPC.
3. **Release.** A SEPARATE in-process producer on a host port is
   dialed directly via `lib/runtime/peer.Dial` + `Client.Release` —
   the exact wire shape rimsky uses on a held-durable instance
   terminate (`lib/runtime/instance_termination.go::ReleaseHeldDurableClaims`).
   The producer's in-memory `releaseCalls` counter grows and its
   `ReleaseClaimIDs()` snapshot records the claim_id the dial passed
   — proof the verb really lands on the producer's handler (the
   "Release is called but the producer's effect is canned" falsifier
   fails when either the counter doesn't grow or the claim_id is
   dropped).
4. **Un-advertised write-semantics is refused at registration.** The
   same in-process producer is dialed via `peer.Dial` (the same
   function `lib/control/config/stores.go::dialRemoteStores` runs at
   rimsky startup); calling `Client.ValidateCapabilities` with an
   operator-declared envelope of `[sync]` returns the canonical
   "capabilities mismatch" error and never proceeds to instantiate a
   Client — proof "a write-semantics the producer didn't advertise is
   silently accepted at registration" is FALSE. A startup config with
   the same misshapen envelope causes the all-in-one container to exit
   non-zero before `/health` flips to 200.

### Why a Docker image and not host-port access?

rimsky's control-api, scheduler, and supervisor ALL eager-dial every
declared claim-producer at startup (`dialRemoteStores`) and EXIT
NON-ZERO if any is unreachable. An in-process producer exposed via the
harness's `WithHostPortAccess` option races the reverse-SSH host-port
tunnel against rimsky's startup dial — under load the tunnel loses,
rimsky fails to dial the producer, and the all-in-one container exits
before `/health` flips to 200. The same race afflicts every
claim-producer dialed eagerly at startup; the rimsky-core test
harness's
`lib/services/test/harness/claimproducer_custom.go::StartOverlapClaimProducerOnNetwork`
documents the workaround we adopt here: a container on a stable
in-network alias is up BEFORE rimsky boots, so the handshake reaches it
deterministically. The publisher and executor cross-stack examples DO
use `WithHostPortAccess` because rimsky's startup handshake for those
peer types is non-fatal (an unreachable executor surfaces as an
operational warning, not a process exit).

The Release + un-advertised-write-semantics legs run against a
SEPARATE in-process producer on a host port because they do NOT drive
rimsky; they observe the protocol surface directly. The host-port-
tunnel race is irrelevant for them.

### Prerequisites

The harness pulls `rimsky-all-in-one:latest` from the local Docker
daemon (nothing is fetched from a registry). Build the image first:

```sh
make core-images
```

The example producer's image is built ON DEMAND from `Dockerfile.example`
via `testcontainers.FromDockerfile` — no `make` target is required for
it. The stub executor's image is similarly built on demand.

Then run the cross-stack proof:

```sh
cd examples/claimproducer
go test -run TestE2E -count=1 -v -timeout 600s .
```

The test brings up the docker network + the example producer container +
two stub executor containers + the Postgres testcontainer +
rimsky-all-in-one and runs the four legs against the SAME running
stack (single bring-up, ~50-90 s total wall time depending on Docker
layer cache).

### How the harness wires the producers

```text
                                       (peers UP before rimsky boots)
┌───────────────────────┐  ┌──────────────────┐  ┌─────────────────────┐
│ example claim-producer│  │ stub executor    │  │ stub executor       │
│ (container; alias     │  │ exec-ok          │  │ exec-err            │
│  example-producer)    │  │ (container)      │  │ EXECUTOR_STUB_      │
│  :9400 advertises     │  │  :9300 returns   │  │ FORCE_ERROR=1       │
│  WRITE_SEMANTICS_     │  │  Success         │  │ returns Error       │
│  READ_ONLY            │  │                  │  │  stub/forced_error  │
└───────────────────────┘  └──────────────────┘  └─────────────────────┘
            ▲                       ▲                       ▲
            │                       │                       │
            │      (eager startup Capabilities handshake)   │
            │                       │                       │
            └───────────────────────┼───────────────────────┘
                                    │
                          ┌─────────┴───────────┐
                          │ rimsky-all-in-one   │ Postgres state DB ◀── postgres:15-alpine
                          │ (3 roles)           │
                          └─────────────────────┘

                          ┌─────────────────────┐
                          │ in-process Producer │  legs 3 + 4 only
                          │ (host port; dialed  │  (direct gRPC,
                          │  via peer.Dial)     │   no rimsky)
                          └─────────────────────┘
                                    ▲
                                    │
                          ┌─────────┴───────────┐
                          │ main_e2e_test.go    │
                          └─────────────────────┘
```

## Migrating from this example

1. Copy `examples/claimproducer/` into your own repo.
2. Rename the module in `go.mod`.
3. Replace the body of `Open` with real acquisition against your
   backing store. Remember the address bytes MUST be valid JSON.
4. Adjust `Capabilities.WriteSemanticsAllowed` to advertise your real
   envelope (sync / staged_async / blocking_async / read_only). If you
   need scope-conflict semantics for non-trivial overlap, set
   `SupportsScopesConflict: true` and implement `ScopesConflict`.
   For fan-out support set `SupportsSplitScope: true` and implement
   `SplitScope`.
5. Implement Commit / Abandon / Release with your real lifecycle
   logic. Each verb is idempotent in `claim_id` on the rimsky side
   (rimsky's at-least-once delivery may retry), so your producer
   MUST handle a repeated terminal verb cleanly.
6. Drop `claimproducer_test.go` and `main_e2e_test.go` if they no
   longer match your shape, or adapt them as a starting point for
   your own tests.

The Apache license file (`../../LICENSE.apache`) covers the example
itself; your fork inherits Apache 2.0 unless you explicitly relicense
it.
