---
audit: bundled-executor-inproc-capability-advertisement
artifact: decision:bundled-executor-inproc-capability-advertisement
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:52Z
---

# Bundled in-proc handlers advertise capabilities into the discovery cache directly, marked static and skipped by the re-probe loop

Supported. Each of the 4 bundled executor packages consumed by `lib/services/bundled/bundled.go` (`claude-agent`, `http-node`, `verifier-http`, `verifier-shape-checks`) exports package-scope `SchemaBytes()`/`DeclaredTags()`/`DeclaredErrorClasses()` accessors; `bundled.go` feeds these into `DiscoverySink.AdvertiseExecutor`, and the same accessors back the standalone gRPC `Capabilities` handshake response (verified concretely for http-node: `ObservabilityServer.CapabilitiesPayload` in `observability.go` calls the identical `SchemaBytes`/`DeclaredTags`/`DeclaredErrorClasses` functions). `config.BundledRegistrations.AdvertiseInto` (`lib/control/config/bundled.go`) writes these into the `Discovery` cache with `Static: true`, and `Discovery.refreshAll`'s periodic loop (`lib/control/observability/handshake.go`) explicitly skips any entry with `Static` set for both executors and claim producers, so the loop cannot mark an in-proc entry unreachable. Bundled claim producers (filesystem, postgres) follow the same path via `AdvertiseClaimProducer`, deriving capabilities from the constructed handler at registration.
