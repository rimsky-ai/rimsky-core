---
audit: bundled-registry-entrypoint
artifact: decision:bundled-registry-entrypoint
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:52Z
---

# One statically-enumerated entrypoint registers every bundled handler, fails loud, and never shadows configured names

Supported. `bundled.RegisterAll` (`lib/services/bundled/bundled.go`) is declared against protocols-only-typed narrow interfaces (`ExecutorHandler`, `ExecutorRegistry`, `ClaimProducerRegistry`, `DiscoverySink`) and statically enumerates all 4 bundled executors (claude-agent, http-node, verifier-http, verifier-shape-checks) plus 2 claim producers (filesystem, postgres) via `executorEntries()` and `registerClaimProducers`; `cmd/internal/bundledwire.CollectBundled` supplies the rimsky-side adapters onto the real registries/discovery cache and is called exactly once per unified process (`cmd/rimsky-entrypoint/main.go`'s `runUnified`, and the compose driver's local-run launcher). Construction failure of a configured handler aborts with an error naming the handler (`TestRegisterAllExecutorRegistrationFailureNamesExecutor`, `TestRegisterAllInvalidFilesystemConfigAbortsNamingProducer`), while an unconfigured one is skipped (`TestRegisterAllZeroConfigSkipsClaudeAgentWithoutCredentials`, `TestRegisterAllZeroConfigRegistersExecutorsSkipsProducers`). Config-wins precedence is enforced at two points: `lib/control/launch/supervisor.go`'s `mergeBundledExecutorAliases` skips seeding the resolver alias for any name already present in the configured executor map (so the bundled in-proc handler is never reachable under that name), and `config.BundledRegistrations.AdvertiseInto` skips the discovery-cache advertisement for the same overridden names — both covered by tests (`TestMergeBundledExecutorEntries_ConfiguredWinsOverBundled`, `TestAdvertiseInto_SkipsNamesOverriddenByConfiguredEndpoint`, `TestMergeBundledClaimProducers_ConfiguredWinsOverBundled`).
