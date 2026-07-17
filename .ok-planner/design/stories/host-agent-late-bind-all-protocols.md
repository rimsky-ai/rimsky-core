---
story: host-agent-late-bind-all-protocols
status: as-is
---

# Executor and claim-producer work through late-bind

## Role

As a template author wiring a workflow against locally-running executor and claim-producer binaries, I can run the host-agent on my dev machine connected to a remote rimsky stack, declare bindings for those two protocols, and have rimsky dispatch through the proxy to spawned local children identically across both, so that I exercise the assembled product against local code without rebuilding images.

## Capability

Host-agent late-binding covers exactly the two rimsky-implementable protocols with a genuine per-node local dev loop: executor and claim-producer. Every dispatch to a late-bound executor or claim-producer binding reaches a real spawned binary; neither protocol returns an unimplemented-method error through the proxy. A binding declared for any other protocol (publisher, validation, data-processing) is refused loudly at registration/config rather than silently accepted or served by a stub — those protocols talk mostly inbound, fire only at registration, or have no per-node iteration story, and the purely-local case is served by single-process all-in-one instead.

## Business value

Template authors exercise the assembled product against local executor and claim-producer code without rebuilding images. Authors who attempt to late-bind an unsupported protocol get a clear, immediate refusal instead of a silent stub or a runtime surprise.

## Acceptance

With the host-agent connected to a deployed host-agent-proxy (see `concept:host-agent`, `concept:host-agent-proxy`) and bindings declared for executor and claim-producer, instance dispatches reach spawned local binaries for both: an executor binding runs a real spawned executor to terminal, and a claim-producer binding's Open, Commit, and Abandon all reach the spawned binary. Declaring a late-bound binding for publisher, validation, or data-processing is refused at registration/config with a diagnostic naming the unsupported protocol.

## Falsifier

An executor or claim-producer dispatch through the proxy is served by an unimplemented-method error or by an effect canned at the proxy layer rather than reaching the spawned binary, OR a late-bound publisher, validation, or data-processing binding is silently accepted (registers or dispatches) instead of being refused.

## Proof

Executable proof.
