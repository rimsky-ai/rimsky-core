---
audit: host-agent-proxy-tls
artifact: decision:host-agent-proxy-tls
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:16Z
---

# Agent-to-proxy pinned-root TLS plus mandatory local-CA mutual mTLS on the agent-child loopback

Supported. Agent→proxy hop: `lib/runtime/hostagent/tls.go::agentTransportCredentials` verifies the proxy's server certificate against a CA root file the agent loads and pins (`loadPinnedCAPool`), carrying the user's api-key over that channel with no client certificate, matching the "session tooling, no enrollment" framing; `TestAgentTLSDialTrustsPinnedCAAndCarriesKey` and `TestAgentTLSDialRejectsWrongCAPin` (`lib/runtime/hostagent/tls_test.go`) confirm both the trust and the pin-rejection paths. Loopback: `lib/runtime/hostagent/localtrust.go` (annotated `@decision: host-agent-proxy-tls`) is instantiated unconditionally on every agent start (`run.go`) — a fresh, self-contained local CA independent of the deployment's peer-auth CA — and both the dispatch dial (`dialChildTLSConfig`, verifying the child's leaf against the local CA pool) and the callback listener (`callbackServerTLSConfig`, `tls.RequireAndVerifyClientCert`) enforce mutual TLS unconditionally, not gated on any deployment posture flag. `lib/runtime/hostagent/mtls_test.go` exercises this end to end across 6 tests, including `TestCallbackListenerAcceptsMutualChildRejectsOthers`, `TestLocalReadinessRequiresMTLSHandshake`, and `TestSpawnRetriesPastPlaintextSquatterThenDispatchesOverMTLS`, which specifically proves a plaintext-only squatter on the child's port is rejected and the agent retries onto the real, mTLS-speaking child.
