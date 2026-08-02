---
audit: host-agent-late-bind-all-protocols
artifact: story:host-agent-late-bind-all-protocols
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:12Z
---

# Executor and claim-producer late-bind through the proxy via one shared spawn/dispatch mechanism

Supported. The proxy registers gRPC servers for all 5 rimsky service protocols on its supervisor-facing side (executor, claim_producer, validation, publisher, data_processing — `cmd/rimsky-host-agent-proxy/agent_server.go` registrations); `test/scenarios/host_agent_latebind_rejects_unsupported_protocols_test.go` checks all 3 of the non-sanctioned ones (validation, publisher, data_processing) and confirms each is rejected `Unimplemented`, leaving exactly the claimed 2 (executor, claim_producer) as the late-bind surface. Both of those 2 are dispatched by the identical generic mechanism: `lib/runtime/hostagent/dispatch.go` switches on protocol name into `dispatchExecute`/`dispatchClaimProducer` sharing the same spawn/forward code, and `spawn.go`'s single `SpawnService` primitive is what both call sites (and `handleSpawn`) launch children through. Executor late-bind is proven full-stack via `test/scenarios/host_agent_late_bind_executor_test.go` (real host-agent, real spawned child, instance driven to `terminal/success`); claim-producer parity is proven by `lib/runtime/hostagent/spawn_test.go`'s `TestDispatchClaimProducerUnary`/`TestDispatchClaimProducerVerbFidelity` (real host-agent daemon dispatching all 4 claim-producer verbs to a real spawned child) plus `cmd/rimsky-host-agent-proxy/conformance_test.go`'s `TestProxyPassesClaimProducerConformanceSuite`, which runs the full claim-producer conformance suite through the proxy against a wire-protocol-faithful tunnel agent, mirroring the equivalent executor-conformance proof (`TestProxyPassesExecutorConformanceSuite`) in the same file.
