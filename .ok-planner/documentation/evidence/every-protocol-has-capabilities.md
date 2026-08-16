---
trap: every-protocol-has-capabilities
release: d977250c
---
# Evidence set — every gRPC service exposes a `Capabilities` RPC as its handshake, so an executor, a validator, and a host agent each answer one.

Source of the prior: sibling-symmetry — `Capabilities` on ClaimProducer, ClaimProducerObservability, DataProcessing, ExecutorObservability, and Publisher, but not on Executor, Validation, LifecycleSubscriber, or HostAgent

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-every-protocol-has-capabilities)

# Asking every shipped protocol for its capabilities handshake

## What it ran against

The bundled service images at this tree, each publishing its gRPC port on
loopback: `rimsky-executor-http-node` (Executor + ExecutorObservability),
`rimsky-executor-verifier-shape-checks` (Executor + ExecutorObservability +
Validation), `rimsky-claim-producer-filesystem` (ClaimProducer +
ClaimProducerObservability, and LifecycleSubscriber when its config sets
`enable_lifecycle`), `rimsky-sensor-cron` (Publisher) and
`rimsky-host-agent-proxy` (HostAgent). A probe built against the published
`github.com/rimsky-ai/rimsky-core/lib/protocols` module invokes
`/rimsky.v1.<Service>/Capabilities` on each and prints the gRPC status. No
rimsky stack is involved; the shipped method names and the service images are
the whole instrument.

## What was observed

Four protocols answered the handshake: `ClaimProducer.Capabilities`,
`ClaimProducerObservability.Capabilities`, `ExecutorObservability.Capabilities`
and `Publisher.Capabilities` each returned OK.

Five probes came back `Unimplemented` with the message "unknown method
Capabilities for service rimsky.v1.<Service>" — the answer a server gives when
it serves the service and the method does not exist:
`rimsky.v1.Executor` (from both the http-node and the verifier-shape-checks
image), `rimsky.v1.Validation`, `rimsky.v1.LifecycleSubscriber` and
`rimsky.v1.HostAgent`. The gap is per method, not per endpoint: the same
lifecycle endpoint answered `LifecycleSubscriber.OnInstanceCreated`, and the
same publisher answered both `Capabilities` and `ListSubscriptions`.

`DataProcessing` declares `Capabilities` in the shipped proto and no bundled
service implements the protocol, so no live probe covers it.

Runnables: `src:.ok-planner/experiments/assumption-every-protocol-has-capabilities/` at the stamped commit.
