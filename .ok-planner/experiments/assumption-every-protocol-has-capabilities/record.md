---
experiment: assumption-every-protocol-has-capabilities
commit: d977250c
---

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
