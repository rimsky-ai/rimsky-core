# examples/data-processing — Reference DataProcessing Mix-in

A minimal, copy-and-modify Go DataProcessing service: the gRPC mix-in
protocol a ClaimProducer advertises alongside its primary
`claim_producer` surface when it materializes typed content (Parquet
files, PostGIS tables, Iceberg snapshots, etc.) against partitioned
writes.

This module is **Apache 2.0** (the protocols / examples / claude-agent
permissive surface) so you can fork it, rename the module in `go.mod`,
and ship a custom DataProcessing-capable producer without inheriting any
AGPL obligations from the rimsky orchestrator itself.

## What this example exhibits

A real DataProcessing mix-in covers the candidate lifecycle (Begin /
Commit / Abandon per fan-out partition) AND the version-history reads
(ListVersions / ListPartitions / GetVersionSchema) that surface the
producer's committed snapshots to operators and dashboards. The example
covers each protocol surface:

- **Capabilities handshake.** `Capabilities` advertises a single entry
  in each of the four envelope fields — `data_shapes:["parquet"]`,
  `materializations:["partitioned"]`, `partition_kinds:["date_range"]`,
  `aggregators:["union"]`. rimsky cross-checks template `data:`
  declarations against this set at canonicalization time; an operator
  template demanding an unsupported entry is refused at registration.
- **BeginCandidate.** Allocates an in-memory staging entry keyed by
  `idempotency_key` and returns an opaque `candidate_handle`. The
  handle's bytes are derived from the `claim_handle_id` +
  `idempotency_key` so they are stable across retries and
  human-legible in logs. A real producer would stage a unique
  object-store prefix or staging schema here. The supervisor calls
  this once per fan-out sub-claim inside the rimsky-side acquisition
  transaction and persists the returned bytes on
  `col:rimsky_claim_handles.producer_candidate_handle`; the leaf
  dispatch reads them back onto `ExecuteRequest.StoreHandle.candidate_handle`
  so the leaf executor knows which staging area to write into.
- **CommitCandidate.** Moves the staged entry into the per-claim
  version list with a fresh monotonic `version_id`, the wall-clock
  commit time, opaque producer metadata, a partition descriptor
  keyed by the BeginCandidate sub-scope, and an illustrative JSON
  schema. The supervisor calls this at leaf-run success; the
  metadata bytes surface via the parent's writeback.
- **AbandonCandidate.** Drops the staging entry without finalizing
  it. The supervisor calls this at leaf-run failure or
  strict-cancel-siblings cancellation. Abandoning an unknown handle
  is an idempotent no-op success — the supervisor may retry the
  verb on a partial outage.
- **ListVersions.** Returns the committed versions for a claim
  handle in commit order. Used by dashboards / operators / the
  control-api asset surface to enumerate the producer's snapshots.
- **ListPartitions.** Returns the partition descriptors for a
  given `(claim_handle_id, version_id)` pair. The example surfaces
  one descriptor per committed candidate (the sub-scope that
  BeginCandidate received); a real producer materializing N
  partitions per Commit would surface N descriptors.
- **GetVersionSchema.** Returns the producer-declared schema bytes
  for a given version. Opaque to rimsky — the example seeds an
  illustrative JSON Schema describing a `{ts, value}` row layout.

## File layout

| File                    | What it is                                                                                                                                              |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `dataprocessing.go`     | The `DataProcessing` type and its seven RPCs. Read this first; it carries the full wiring contract.                                                     |
| `main.go`               | The binary entry point — `Listen` + `RunGRPC` lifecycle.                                                                                                |
| `dataprocessing_test.go`| Fast in-process test pinning the Begin → Commit happy path + Capabilities surface.                                                                      |
| `main_e2e_test.go`      | Cross-stack proof — drives each protocol surface through the rimsky-side client (`lib/runtime/peer.DialDataProcessing`) and asserts the Falsifier's four observable failure modes do not fire. |

## Running the producer

The binary listens on TCP `:9500` by default. To change the bind
address, edit `main.go` — the example uses the literal port directly
rather than reading an env var, to keep the copy-and-modify surface
minimal.

```sh
cd examples/data-processing
go run .                               # listens on :9500
```

Point rimsky at the producer by registering it as a claim-producer
that advertises the `data_processing` mix-in protocol in your
`rimsky.yml`. The DataProcessing surface is a mix-in: a producer
ALWAYS advertises `claim_producer` as its primary protocol and
`data_processing` as an additional protocol that rimsky's
sub-claim-acquisition path consults at fan-out time.

```yaml
claim_producers:
  example:
    endpoint: "127.0.0.1:9500"
    protocols: [claim_producer, data_processing]
    write_semantics_allowed: [sync]
```

A template node references the producer through the standard
`stores:` list. For the fan-out path the example targets, the node
also declares a `fan_out:` block whose claim names the store alias
and whose `partition_request` carries the partition selector the
producer's `SplitScope` will decode:

```yaml
name: my-fan-out-pipeline
version: "1"
frame_resolution_mode: serial_queue
nodes:
  - type: fan-leaf
    executor: my-executor
    fan_out:
      claim: data
      partition_request: '{"partition_keys":["2026-01","2026-02","2026-03"]}'
      error_policy:
        kind: best_effort
    stores:
      - name: example
        selector: r1            # opaque to rimsky; the producer parses it
        intent: rw              # the example advertises sync, so rw is OK
        alias: data
```

## In-process tests

`dataprocessing_test.go` stands up the `DataProcessing` on a loopback
port and drives the protocol directly via gRPC — no rimsky, no Docker:

- `TestBeginThenCommitCandidate_RoundTrips` — Begin returns a
  non-empty candidate_handle; Commit on that handle returns
  non-empty metadata; Capabilities advertises a non-empty set.

Run it:

```sh
cd examples/data-processing
go test -count=1 ./...
```

## Cross-stack walkthrough

`main_e2e_test.go` is the cross-stack proof for the
`STORY-data-processing-author` user-outcome story. It exhibits each
protocol surface against the example DataProcessing server through the
EXACT rimsky-side client surface — `lib/runtime/peer.DialDataProcessing`
+ the `clientiface.DataProcessingClient` interface — that
`lib/runtime/peer/registry` constructs at rimsky startup for every
operator-declared `claim_producer` that lists `data_processing` in its
protocols block.

The cross-stack rimsky-side fan-out path (the supervisor calling
`BeginCandidate` once per sub-claim inside the acquisition
transaction and threading the returned handle onto the leaf's
`ExecuteRequest.StoreHandle.candidate_handle`) is already exhibited
end-to-end against a real `rimsky-all-in-one` stack by
`test/scenarios/leaf_candidate_handle_e2e_test.go`, which declares a
fan-out node referencing a remote stub store whose DataProcessing
surface mints one candidate per `BeginCandidate` and asserts each
fan-out leaf dispatches with a non-empty per-partition-unique
candidate handle. The test file here pins the protocol-surface
behavior of THIS example through the same rimsky-side client, so
together the two cover the spec's full Acceptance:

1. **BeginCandidate is called per fan-out partition with a non-empty
   handle.** The leaf-candidate-handle scenario drives this against a
   running rimsky stack; the test here exhibits the same client →
   server call against this example.
2. **CommitCandidate's effect is real, not canned.** The test bumps
   the example's `CommitCount` counter and asserts it grew across
   the call — proof the verb landed on the producer's real handler
   and the metadata is not a canned response.
3. **AbandonCandidate is NOT skipped on leaf failure.** A
   Begin → Abandon sequence drives `AbandonCount` up and clears the
   staged entry; ListVersions afterward returns an empty list —
   proof an abandoned candidate is NOT silently committed (the
   Falsifier "AbandonCandidate is skipped on leaf failure" fails when
   the counter does not grow).
4. **A declared version DOES appear in ListVersions.** The
   CommitCandidate response declares a `version_id` in its metadata;
   the subsequent ListVersions response includes that version_id,
   ListPartitions returns the partition descriptor keyed by the
   BeginCandidate sub-scope (proving the partition list is not
   canned), and GetVersionSchema returns valid JSON Schema bytes
   (proving the schema response is not canned).

### Prerequisites

`main_e2e_test.go` is a pure in-process test — no Docker, no
testcontainers, no rimsky-all-in-one image. The test stands up the
example DataProcessing server on a loopback port and drives it via the
rimsky-side client constructor. Total wall time: <1s.

The cross-stack scenario `test/scenarios/leaf_candidate_handle_e2e_test.go`
DOES require Docker + the rimsky-all-in-one image; see the rimsky-core
repo's `Makefile` for the bring-up commands.

```sh
cd examples/data-processing
go test -run TestE2E -count=1 -v .
```

### How the proof wires the example

```text
┌──────────────────────────────┐
│ example DataProcessing       │  in-process on a loopback port
│ (DataProcessing type)        │  ─ tracks staged candidates
│ :<free port>                 │  ─ tracks committed versions
│  Capabilities advertises:    │  ─ exposes CommitCount /
│  data_shapes:["parquet"]     │    AbandonCount for proof
│  partition_kinds:            │    assertions
│   ["date_range"]             │
└──────────────────────────────┘
              ▲
              │
              │  EXACT rimsky-side client surface
              │  (peer.DialDataProcessing →
              │   clientiface.DataProcessingClient)
              │
┌─────────────┴────────────────┐
│ main_e2e_test.go             │
│  ─ Capabilities (gRPC direct)│
│  ─ Begin+Commit+List path    │
│  ─ Begin+Abandon path        │
└──────────────────────────────┘

(the rimsky-side dispatch through the all-in-one stack is exhibited
 by test/scenarios/leaf_candidate_handle_e2e_test.go against a stub
 store fixture whose DataProcessing surface mirrors this example)
```

## Migrating from this example

1. Copy `examples/data-processing/` into your own repo.
2. Rename the module in `go.mod`.
3. Replace the in-memory candidate / version map with real
   persistence — Postgres, the object store's own metadata table,
   etc. Remember that `version_id` MUST be stable: a dashboard
   subscribing to your ListVersions stream and a later
   ListPartitions / GetVersionSchema MUST agree on the same
   version_id values.
4. Adjust `Capabilities` to advertise your real envelope.
5. Implement Commit / Abandon with your real atomic-swap logic.
   Each verb is idempotent in `candidate_handle` on the rimsky side
   (the supervisor may retry); your producer MUST handle a repeated
   terminal verb cleanly — the example exhibits this for Abandon
   (a repeat on an already-cleared handle is a no-op success) and
   you should mirror it for Commit (a repeat on a now-finalized
   handle should be idempotent or surface a FailedPrecondition,
   depending on your atomicity guarantees).
6. Drop `dataprocessing_test.go` and `main_e2e_test.go` if they no
   longer match your shape, or adapt them as a starting point for
   your own tests.

The Apache license file (`../../LICENSE.apache`) covers the example
itself; your fork inherits Apache 2.0 unless you explicitly relicense
it.
