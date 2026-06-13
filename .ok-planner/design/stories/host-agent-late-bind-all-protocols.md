---
story: host-agent-late-bind-all-protocols
status: as-is
---

# Every protocol works through late-bind

## Role

As a template author wiring a workflow against locally-running binaries (executor, claim-producer, publisher, validation, data-processing), I can run `rimsky agent` on my dev machine connected to a remote rimsky stack, declare bindings for each protocol, and have rimsky dispatch through the proxy to spawned local children identically across every supported protocol — no protocol left as a `Unimplemented` stub, so that I exercise the assembled product against local code without rebuilding images.

## Capability

Host-agent late-binding for every rimsky-implementable protocol: executor, claim-producer, publisher, validation, data-processing. Every dispatch reaches a real spawned binary; no protocol returns `Unimplemented` through the proxy.

## Business value

Template authors exercise the assembled product against local code without rebuilding images, across every protocol rimsky supports — no protocol is partially-supported.

## Acceptance

With `rimsky agent` connected to a deployed `rimsky-host-agent-proxy` and bindings declared for each protocol, instance dispatches reach spawned local binaries: a validation binding's rejecting validator causes registration rejection at the validation surface; a publisher binding publishes real messages into the instance; a data-processing binding performs a real typed-data operation; executor and claim-producer bindings work. Every dispatch is served by a real spawned binary; none returns gRPC `Unimplemented`.

## Falsifier

Any of the five protocols returns `Unimplemented` through the proxy, OR a dispatch's effect is canned at the proxy layer rather than reaching the spawned binary.

## Proof

Executable proof.
