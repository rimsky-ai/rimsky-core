---
error: capability_envelope_mismatch
surfaced_to: operator
---

# Capability envelope mismatch

## What it means

The operator-declared `write_semantics_allowed` for a claim producer is not a subset of the envelope the producer advertised at startup via `Capabilities()`. Rimsky refuses to start.

## When it happens

At supervisor / scheduler / control-api startup, after the `Capabilities()` handshake. The operator's declared envelope contains values the producer did not advertise, or contains a value that is no longer supported by the producer's current version.

## What to do

Either narrow the operator's declared envelope (`rimsky.yml: claim_producers.<name>.write_semantics_allowed`) to be a subset of what the producer advertises, or upgrade the producer to advertise the operator's intended envelope. Run `Capabilities()` against the producer manually (e.g., via the conformance binary) to see the producer's current declaration.

## See also

- [`../../concepts/write-semantics.md`](../../concepts/write-semantics.md)
- [`../../concepts/claim-producer.md`](../../concepts/claim-producer.md)
- [`../../protocols/claim-producer.md`](../../protocols/claim-producer.md)
