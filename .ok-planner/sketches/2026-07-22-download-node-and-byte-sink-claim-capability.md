# 2026-07-22 — Download node and a streaming read/write claim capability

Pre-spec. No authorization to build.

Surfaced while decomposing GitHub issue 23, which fused several problems. The
async-HTTP half of that issue stays on the issue and its ledger row
(`issue:http-async-node`); this sketch covers only the part that has no home
today: a node that moves bulk bytes, and the claim capability it needs to have
somewhere to put them.

## The gap

A node whose job is to fetch a large payload — a bulk export, a dataset
download, anything that runs long and lands many megabytes — has nowhere to put
the result.

`http-node` already refuses to try. A body over `MaxBodyBytes` (10MB default)
errors as `http/response_truncated` rather than delivering a partial body
(`code:lib/services/executors/http-node/server.go::executeCore`). That refusal is
correct and worth preserving: it fails loudly instead of silently corrupting an
attribute.

Blob storage does not change the verdict. `concept:blob-backend` exists to spill
*attribute values* so that "a small attribute value and a large attribute blob
behave the same to substitution consumers." The bytes stay logically inside the
attribute plane — persisted, versioned, walked by substitution — while
`invariant:21` makes them inert, so rimsky may never read them. Routing bulk data
through it pays the full cost of the attribute plane for content the platform is
forbidden to inspect.

`concept:data-processing` states the governing principle directly: *"Data motion
stays substrate-direct via the acquired result's address; the protocol carries
control-plane only."*

**So a download node returns a reference, never a payload** — where the bytes
landed, how many, a checksum, an outcome status. Small, inspectable, and the
thing downstream nodes actually branch on.

## Why the destination is the hard part

The claim's address already reaches the executor, for any claim:
`proto:executor.proto::ClaimProducerHandle.handle` carries the producer-supplied
Address bytes at dispatch. No producer change is needed for a node to *have* a
destination.

The problem is that the bundled producers do not agree on what an address
denotes:

| producer   | address bytes                             | site                                                          |
| ---------- | ----------------------------------------- | ------------------------------------------------------------- |
| filesystem | JSON string holding an absolute path      | `code:lib/services/claim_producers/filesystem/store/store.go`   |
| postgres   | the row selector, or a staging descriptor | `code:lib/services/claim_producers/postgres/store/store.go`, `staging.go` |

Only the filesystem address denotes a place bytes can go; a postgres address is a
row selector, and no amount of streaming writes a file into one.

This divergence is intentional, not an oversight.
`proto:executor.proto::ClaimProducerHandle` documents the address as *"opaque to
Rimsky, decoded by the executor per its producer-specific knowledge,"* with
`kind` (field 1) downgraded to *"conventionally informational — the executor
knows its producer's kind from the deployment's operator config."* There is no
abstraction over address shape, by design.

So a download node cannot be generic over producers by inspecting addresses. It
needs the producer to *tell it* whether bytes are accepted, and to hand it the
operations for moving them.

## The proposal: a byte-sink mix-in

Add a streaming read/write mix-in protocol to the claim-producer capability
handshake.

This is not a new mechanism. `proto:claim_producer.proto::CapabilitiesResponse`
already carries `repeated string protocols` (field 4), documented as *"the mix-in
service protocols this binary implements alongside ClaimProducer (e.g.
`data_processing`, `validation`, `lifecycle_subscriber`)."* `concept:data-processing`
is itself described as an "optional mix-in protocol on a claim producer,
advertised in the capabilities handshake." A byte-sink capability is a fourth
name in that list.

The split that makes it work is already the platform's: **rimsky gates on the
advertised capability name without interpreting the substrate behind it.** It
already rejects fan-out templates when a producer advertises
`supports_split_scope: false`, and validates `write_semantics_allowed` as a
strict subset at startup — enforcement on the advertised name, ignorance of what
sits behind it.

Applied here: template registration rejects a download node pointed at a producer
that does not advertise the byte-sink protocol, while rimsky still never learns
what a path or a row selector is. Filesystem advertises it; postgres does not.

The operations are producer-supplied, so the surface can grow without rimsky
gaining awareness of any substrate. The general form — each producer advertises
its capabilities and supports whatever operation set those capabilities imply —
is the durable idea here; byte-sink is the first instance beyond
`data_processing`.

## Consequence for the filesystem producer

A filesystem claim naming a specific file or path should be genuinely readable
and writable through the mix-in's operations, rather than an executor inferring a
path convention from an opaque address string. That is the concrete first
implementation: filesystem advertises byte-sink, and its claims become real
streaming handles.

## Sequencing note

Accepting a filesystem claim *without* the mix-in is the smaller build and meets
the immediate need. It forecloses nothing: the node reads its destination from
`ClaimProducerHandle.handle` either way, and adding the mix-in later changes
*which producers registration accepts*, not the node's dispatch path.

The decision recorded in discussion was to do it properly — the mechanism is
already there, so the mix-in is the intended path rather than the stretch goal.

## Relationship to assets

A versioned result — "this produced version N of dataset X" — is already modeled
and needs no new primitive. `concept:asset` is a committed, durable-lifetime claim
against a data-processing-capable producer; the producer commits a candidate and
returns a canonical version identifier, which rimsky records on the claim handle
and in the lineage ledger.

Two facts constrain reaching for it now:

- GitHub issue 38 records that no bundled claim producer advertises
  `data_processing`, so asset semantics are not exercisable on the stack as
  shipped.
- `proto:executor.proto::ClaimProducerHandle.candidate_handle` is populated only
  for data-processing claims **at fan-out leaf dispatch** — *"empty for
  non-DataProcessing claims and non-fan-out claims."* A single non-fan-out
  download node would receive no candidate handle even against a data-processing
  producer.

Asset semantics for a download node therefore depend on resolving both. Deferred
by explicit decision; noted so the dependency is not rediscovered later.

## Open questions

- The mix-in's operation set: open/write/close streaming versus a single put, and
  whether reads belong in the same mix-in or a sibling protocol.
- Whether the download node is its own type or a mode on `http-node`. It is a
  long *synchronous* dispatch — rimsky is the doer, so nothing calls back — which
  makes it closer to `http-node` than to the async node.
- Its deadline. As a long synchronous request it runs into the same
  deployment-wide 60s cap tracked as `issue:http-node-global-timeout`; a download
  node is unusable until that is per-node.
- What the returned reference contains, and whether its shape is fixed by rimsky
  or producer-supplied like the address.
- Whether byte-sink and `data_processing` compose on one producer, and what a
  claim advertising both means for commit semantics.
