---
decision: uniform-attributes-delta
status: as-is
aliases: []
---

# Success / Error / Park uniformly carry attributes_delta

## Choice

Add `google.protobuf.Struct attributes_delta` to `Error` and `Park`. (`Success` already carries it.)

## Rationale

All three are cascade-firing terminals that settle the dispatch atomically. Attribute writeback should ride the same transaction as the verdict commit, uniformly. The historical asymmetry forced executors that wanted to write attributes on Error or Park to use the mid-dispatch attribute writeback callback, creating a parallel mechanism for what should be uniform.

## Alternatives

None considered — the alternative is the current asymmetry.
