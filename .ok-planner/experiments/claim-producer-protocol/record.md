---
experiment: claim-producer-protocol
commit: PENDING
---

# A custom claim producer plugging into a rimsky stack

## What it ran against

A claim producer written for this experiment against the published
claim-producer gRPC protocol — capabilities, Open, Commit, Abandon, Release —
started five times on loopback: one per advertised write-semantics value
(sync, staged-async, blocking-async, read-only) and one that always answers
Open with Unavailable carrying an error class it declares. A
`rimsky-all-in-one` stack from this tree's image is pointed at all five
through the `claim_producers` block of a mounted rimsky.yml. The nodes run on
the bundled http-node executor, which posts its resolved attributes to a
recorder on the host, so the values rimsky substituted into a dispatch are
readable from outside the stack. The producer keeps its own log of every call
it received and serves it over HTTP. Re-run unchanged at this tree.

## What was observed

Each of the five producers is listed by the control API's claim-producer view
carrying the error class that producer declares in its capabilities response,
so the startup handshake reaches the stack.

Four nodes, one per producer, each settled fresh. The producer's log shows
Open arriving with the selector resolved from the instance parameter
(`/regions/us-east-1` from a template that declares
`/regions/{{params.region}}`), the declared intent, the declared alias, and
the node's `data` blob byte-for-byte as the template wrote it. The successful
write claim was closed with Commit; the read-intent claim was also closed with
Commit rather than Release.

The claim handle each producer returned reached its node's dispatch: the
recorder received the address, the claim-scope bytes, and a named field of the
payload the producer synthesized. The persisted claim-handle rows carry each
producer's realized write semantics — sync, staged_async, blocking_async and
read_only, one per producer — and all four reached state committed.

The producer that answers Unavailable settled its node on
`terminal/error/scarce/exhausted`, the error class that producer declares and
returns, rather than on a generic acquisition failure.
