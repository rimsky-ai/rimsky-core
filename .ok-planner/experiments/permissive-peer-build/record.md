---
experiment: permissive-peer-build
commit: d977250c
---

# A peer built without copyleft obligations

## What it ran against

`peer/` is a complete third-party service: its own Go module, whose only rimsky
requirement is the protocols module (resolved locally by the `replace` line the
run materialises). It implements the executor protocol and its observability
capability probe. The run builds it, inspects its dependency graph, then brings
up a `rimsky-all-in-one` stack from the tree's own image tag with the peer
declared as an ordinary gRPC executor and the peer itself running in an alpine
container on the same docker network. Re-run unchanged at this tree.

## What was observed

The module built for the host and cross-built for the stack's platform. Its
module graph names exactly one rimsky module — the protocols module — and every
rimsky package it links is under that module. All 105 Go files in the protocols
module declare Apache-2.0, and the root module it does not depend on declares
AGPL, so the boundary the story rests on is the one the build actually respects.

Against the running stack the protocol's verbs were exchanged in both
directions: the discovery probe's Capabilities call returned the peer's declared
error class `third-party/refused`, and two Execute dispatches settled — one node
fresh with the peer's success delta (`served_by: third-party-peer`) on the
record, one node failed carrying the peer's own error class. The peer's container
log shows both executions.
