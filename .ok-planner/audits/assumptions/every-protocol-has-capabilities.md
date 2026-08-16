---
assumption: every-protocol-has-capabilities
commit: PENDING
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# every gRPC service exposes a `Capabilities` RPC as its handshake, so an executor, a validator, and a host agent each answer one.

As service author implementing a protocol, I would take it that every gRPC service exposes a `Capabilities` RPC as its handshake, so an executor, a validator, and a host agent each answer one.

## Source

sibling-symmetry — `Capabilities` on ClaimProducer, ClaimProducerObservability, DataProcessing, ExecutorObservability, and Publisher, but not on Executor, Validation, LifecycleSubscriber, or HostAgent

## What a run would observe

call `Capabilities` on each of the ten protocols against a bundled implementation and record which have no such method.

## Measured

The experiment `assumption-every-protocol-has-capabilities` called
`/rimsky.v1.<Service>/Capabilities` on the bundled implementation of each
protocol and recorded the gRPC status. Four protocols answered:
`ClaimProducer`, `ClaimProducerObservability`, `ExecutorObservability` and
`Publisher`. Four answered `Unimplemented` with "unknown method Capabilities
for service <name>", which is what a server returns when it serves the service
and the method does not exist: `Executor` (from both the http-node and the
verifier-shape-checks image), `Validation`, `LifecycleSubscriber` and
`HostAgent`. The prior names an executor, a validator and a host agent
explicitly, and all three contradict it. The gap is per method: the same
lifecycle endpoint answered `OnInstanceCreated`, and the same publisher
answered both `Capabilities` and `ListSubscriptions`. Of the nine gRPC services
in the ten shipped proto files, five declare `Capabilities` and four do not;
`DataProcessing` declares it and no bundled service implements the protocol, so
no live probe covers that one. A service author who builds a client that opens
every connection with a `Capabilities` handshake gets a runtime `Unimplemented`
against four of the nine protocols.
