# Intent Dossier: host-agent-proxy

Distilled 2026-07-13 from session transcripts (2026-06-12..2026-07-13) and ok-planner
history artifacts (2026-05-04..2026-06-11). Transcript tier outranks artifact tier;
later intent supersedes earlier. Part of the drift-remediation intent ledger.

## Net position

- From rimsky's perspective the proxy is a normal multi-protocol service: declared in rimsky.yml, conformance-testable, dispatched-to like any hosted peer. No tunnel-awareness may leak into the supervisor, the dispatch path, the error vocabulary, or graph processing (2026-05-24, host-agent-and-proxy, artifact).
- Protocol-transparency is the governing invariant: the proxy transparently forwards every rimsky service protocol it fronts (executor, claim-producer, publisher, validation, data-processing) by one uniform mechanism, each presenting exactly the fronted service's protocol; no protocol ships as a registered-but-unimplemented stub (2026-06-06, comprehensive-gap-closure, artifact, superseding the 2026-05-24 v1 stub posture).
- Spawn model: birth is lazy (first dispatched call for a (run-scope, binding) pair); lifetime is per run-scope with one spawned process serving all dispatches in that scope; a multi-protocol binary gets one Spawn with multiple expected_protocols; reap on run-scope termination via SIGTERM then SIGKILL after a grace period (2026-05-24, artifact). Isolation is per run-scope — concurrent run-scopes never share executor process state — requiring run_scope_id on the dispatch wire (2026-06-06, comprehensive-gap-closure, artifact, correcting the shipped instance-keyed spawns).
- Routing to the proxy uses a per-call x-rimsky-service-name gRPC metadata header injected by client interceptors; hosted services ignore it; the Endpoint type and Resolver stay unchanged (2026-05-24, artifact).
- The callback_url rewrite to the agent's local_callback_base_url is the ONLY URL the proxy touches (2026-05-24, artifact).
- service_bindings is an opaque JSONB column populated at instance creation, stored verbatim, exposed on GET /instances/{id}, never inspected by the server; the proxy is the sole consumer. cwd is an instance-level param, not part of the binding (2026-05-24, artifact).
- When two agents connect under one api-key, latest Register wins; the older connection closes gracefully and RegisterAck carries displaced_prior (2026-05-24, artifact).
- Anonymous-mode and late-binding are no longer mutually exclusive: ownerless instances resolve the serving agent via an anonymous-mode routing identity / default agent route (2026-06-06, comprehensive-gap-closure, artifact, resolving the anonymous-mode-locks-out-late-bind tension).
- The proxy is not used in one-shot compose-run mode — it exists to bridge a remote long-running stack to a dev box (2026-06-13, 65667e33, transcript).
- License split: host-agent code is Apache (client-side, bundled into the CLI); the proxy binary and its migration SQL are AGPL platform-side (2026-05-24, artifact).
- The agent-facing side is served over TLS: the proxy presents a server cert the agent verifies against a pinned deployment-CA root, so the agent's api-key transits an encrypted channel over the dev-machine→deployment hop; the agent presents no client cert and authenticates by api-key inside the channel (2026-07-16, peer-auth-posture, transcript; see the peer-auth dossier).

## Required behaviors (open promises)

- `rimsky run --service <name>=<path>` works end-to-end: the CLI ensures a local host-agent daemon (auto-start), posts the run with per-instance service_bindings, binaries spawn on the user's machine in the supplied cwd, are held for the run-scope's lifetime, and are reaped on close (2026-05-24, host-agent-and-proxy, artifact).
- Every dispatch through the proxy across all five protocols is served by a real spawned local binary — no gRPC Unimplemented stubs; each run-scope spawns its own isolated child and terminating a run-scope reaps only that scope's child (2026-06-06; 2026-06-08, corpus-bootstrap, artifact).
- Reap on run-scope terminal must actually function: OnInstanceCreated populates the binding cache, OnRunScopeTerminal drives reap (2026-05-24, artifact) — see the recorded no-op defect under Corrections; the spec's reap promise was never retracted.
- The claim-route map: claim_id → (api_key_id, spawn_id) recorded at Open time so Commit/Abandon/Release (which carry only claim_id) route back to the spawned producer that opened the claim (2026-05-24, host-agent-and-proxy-divergences, artifact).
- The proxy's claim-producer entry advertises the full write-semantics envelope (pure transport; any binding might realize any semantics); per-claim realized semantics come from each spawned producer's Open; the envelope is served on ClaimProducer.Capabilities (2026-05-24, artifact).
- Deferred validation: the spawned binary's Capabilities handshake provides the actual schema; the proxy validates resolved attribute values against it; mismatch → contract_mismatch — the accepted price of late-binding (2026-05-24, artifact).
- Crash recovery matrix: spawned-process crash → executor_crashed and fresh spawn on next dispatch; agent disconnect → host_agent_disconnected on in-flight dispatches and SIGKILL of orphans on reconnect-recovery; proxy restart → agents reconnect with backoff; supervisor crash invisible to the proxy (orphan-reaper reclaims rows) (2026-05-24, artifact).
- Callback rewrite round-trip: spawned processes behind NAT POST async callbacks to the agent's local listener; the agent forwards through the tunnel; the proxy POSTs to the original supervisor URL (2026-05-24, artifact).
- DispatchFrame multiplexing across stream-ids within one spawn-id, with a Kind enum for data/half-close/cancel mirroring gRPC stream semantics — no head-of-line blocking for concurrent calls (2026-05-24, artifact-only).
- The proxy passes the existing executor and claim-producer conformance suites unmodified, run with a stub spawned process behind an in-process agent fake (2026-05-24, artifact).
- GET /instances/{id} exposes service_bindings and created_by_api_key_id so the proxy's cache-miss GET-fallback can recover an instance (2026-05-24, host-agent-and-proxy-divergences, artifact — fixed during execution).
- Main run-scope close at instance termination fires OnRunScopeTerminal before the OnInstanceTerminated fan-out, so main-scope spawns get reaped (2026-05-24, artifact; note the 2026-07-07 frame-owned-run-scope reversal on the lifecycle-subscriber dossier — no scope exists for never-ran instances).
- End-to-end scenario tests exercise the real rimsky-host-agent-proxy binary as a real gRPC server with a real stub child honoring RIMSKY_AGENT_PORT — the real dispatch path, not an in-process fake (2026-05-24, artifact-only).
- The lifecycle fan-out filter includes late_bind_service_proxies peer names for templates with late_bind_services, scoped to instance/run-scope events so proxy idempotency rows are bounded by instance count (2026-05-24, artifact-only).

## Intentional absences

- BlobBackend fronting — it is an in-process Go interface, not a wire protocol; no gRPC surface exists to front (2026-05-24, artifact).
- A dedicated host-agent/proxy conformance binary — v1 follow-up per the spec (2026-05-24), then made permanently unnecessary by protocol transparency (2026-06-06, artifact).
- The ServiceName interceptor on the lifecycle, publisher, validation, and data-processing dial paths — installed only on executor and claim-producer paths, per spec optionality; intentional, not dropped work; the TODO(host-agent-proxy v2) markers recording it were residue to delete (2026-06-13, c41b7afe, transcript). See Conflicts.
- Explicitly deferred beyond v1: pool-routed bindings, long-running pinned bindings (attach to an already-running process), per-binding env/args/cwd/timeout overrides, sandboxing beyond --allow-paths, internal-service auth between rimsky processes, a synthetic error class for unreachable services, explicit multi-agent routing (2026-05-24, artifact).
- Process-to-process authentication — deployment-level network isolation instead; cataloged as the internal-service-auth-unspeced tension, not solved in v1 (2026-05-24, artifact). SUPERSEDED 2026-07-16: the internal-service-auth-unspeced tension is RESOLVED by the peer-auth posture — internal service↔service traffic is optionally mTLS under `peer_auth: mtls` (default `none` keeps the network-isolation model), and the agent→proxy hop uses pinned-root TLS (2026-07-16, peer-auth-posture, transcript).
- Proxy participation in one-shot compose-run — the supervisor is already local there (2026-06-13, 65667e33, transcript).

## Corrections and restorations (drift-fight record)

- Known landed defect, recorded unfixed at the time: the OnRunScopeTerminal reap handler matches spawn state by scopeID holding the instance id while every firing site passes a run-scope id — the ids never match, dropSpawnsForRunScope finds nothing, and reap-on-terminal is a no-op (cleanup only via agent disconnect/displacement) (2026-05-24, host-agent-and-proxy-divergences, artifact). The 2026-06-06 gap-closure ruling (run-scope-keyed spawns with run_scope_id on the wire) is the corrective intent.
- Spawn-dedup keyed on instance id instead of the designed (run_scope_id, binding_name) because ExecuteRequest/OpenRequest carried no run_scope_id (2026-05-24 divergence) — ruled a contradiction of the documented invariant; run_scope_id must ride the dispatch wire (2026-06-06, comprehensive-gap-closure, artifact).
- GET /instances/{id} omitted service_bindings / created_by_api_key_id that the cache-miss fallback reads — found and fixed during execution (2026-05-24 divergences, artifact).
- Proto name collisions: Heartbeat/HeartbeatAck/Error renamed HostAgentHeartbeat/HostAgentHeartbeatAck/HostAgentError (flat rimsky.v1 package; executor.proto owns the originals) (2026-05-24 divergences, artifact).
- Write-semantics envelope served on ClaimProducer.Capabilities rather than the plan's ClaimProducerObservability.Capabilities (no field exists there); observability capabilities return empty (2026-05-24 divergences, artifact).

## Superseded / historical

- v1 posture of Publisher/Validation/DataProcessing handlers registered but returning UNIMPLEMENTED (2026-05-24) — superseded by the protocol-transparency invariant and the all-five-protocols promise (2026-06-06 / 2026-06-08, artifact).
- Hard mutual exclusion where an ownerless instance's late-bound dispatch failed with host_agent_not_connected (2026-05-24) — superseded by anonymous-mode routing (2026-06-06, artifact).
- Spawn scope keyed on instance id (shipped v1 shape) — superseded by run-scope keying (2026-06-06, artifact).

## Conflicts needing human ruling

- **RESOLVED 2026-07-14 (user ruling, transcript tier): executor + claim-producer is the sanctioned late-bind surface.** Use-case review confirmed the subset falls out of the need: the proxy exists for the no-inbound-route problem, and executor/claim-producer are the dispatch-target protocols with a real dev loop (iterate a local binary against a shared deployment; machine-bound resources); publishers talk mostly inbound (POST to control-api needs no tunnel), validation fires at registration, data-processing has no per-node iteration story, and single-process all-in-one owns the purely-local case. Consequences: (a) late-binding a publisher/validation/data-processing service is REJECTED loudly at registration/config — silent non-routing is a defect; (b) the 2026-06-08 all-five/no-stubs artifact promise is superseded; (c) the proxy's server-side handlers for the unreachable three protocols are trimmed per erase-completely; (d) if a real use case for the other three ever appears, it is a fresh spec.
