---
audit: per-service-load-opts-from-env
artifact: decision:per-service-load-opts-from-env
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:57Z
---

# Each bundled service exposes one LoadOptsFromEnv, shared by its standalone main and the bundled in-process registration

Supported. All 6 bundled services with a `LoadOptsFromEnv` function checked (`claim_producers/filesystem/server`, `claim_producers/postgres/server`, `executors/claude-agent`, `executors/http-node`, `executors/verifier-http`, `executors/verifier-shape-checks`) — the population enumerated by grepping `func LoadOptsFromEnv` under `lib/services` — have exactly one such constructor, called both by that service's `cmd/main.go` and by `lib/services/bundled/bundled.go::registerExecutors`/`registerClaimProducers`; neither surface re-parses env independently. The unconfigured/misconfigured split matches the decision exactly: both claim-producer standalone mains treat `!opts.Configured` as a fatal startup error while `bundled.go` skips with a log line naming the producer and its config env var; the claude-agent standalone `Serve` returns `ErrCredentialsMissing` as a startup error when neither `ANTHROPIC_API_KEY` nor `CLAUDE_CODE_OAUTH_TOKEN` nor stub mode is set, while `bundled.go` wraps the same check in a skip sentinel (`errExecutorSkipped`). Present-but-invalid configuration is proven to abort bundled registration by `TestRegisterAllInvalidFilesystemConfigAbortsNamingProducer`, alongside `TestRegisterAllZeroConfigSkipsClaudeAgentWithoutCredentials` and `TestRegisterAllZeroConfigRegistersExecutorsSkipsProducers` in `lib/services/bundled/bundled_test.go`.
