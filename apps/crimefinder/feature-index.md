# Crimefinder Feature Index

| Feature | Files |
|---|---|
| Gate vocabulary (executor side) | `executor/src/gates/` (review-context, review-finding, review-coverage, review-complete, review-run-tests, review-commit-fix, review-defer, review-skip-zone, review-request-help, review-dedup-mark), `executor/src/internal-mcp-server.ts`, `executor/src/internal-mcp-tools.ts` |
| Internal MCP server / token auth | `executor/src/internal-mcp-server.ts`, `executor/src/token-registry.ts` |
| Class-5b auto-routing | `producer/src/state/class-5b-rule.ts`, `producer/src/state/append-finding.ts`, `producer/src/concepts/parser.ts` |
| Atomic commit-fix | `producer/src/state/commit-fix.ts`, `producer/src/state/commit-mutex.ts`, `producer/src/git-ops.ts` |
| Zone partitioning | `producer/src/zones/partition.ts`, `producer/src/claim-producer/split-scope.ts` |
| Zone coverage | `producer/src/zones/coverage.ts`, `producer/src/state/append-coverage.ts` |
| JSONL substrate | `producer/src/jsonl-store.ts`, `producer/src/jsonl-mutex.ts`, `shared/src/jsonl-rows.ts` |
| Fingerprinting / dedup | `shared/src/fingerprint.ts`, `producer/src/dedup/group.ts`, `producer/src/dedup/resolve.ts` |
| Per-pass iteration counter (durable) | `producer/src/state/iteration-counter.ts` |
| Session-token registry (producer-side) | `producer/src/state/session-tokens.ts` |
| RunTests + cache | `producer/src/state/run-tests.ts`, `producer/src/state/test-cache.ts`, `producer/src/state/run-tests-handler.ts` |
| Recovery scan | `producer/src/recovery/startup-scan.ts` |
| Concept-doc parsing + annotations | `producer/src/concepts/parser.ts`, `producer/src/concepts/scanner.ts` |
| Scope handlers (12 selectors) | `producer/src/scopes/*.ts` |
| ClaimProducer wire | `producer/src/claim-producer/*.ts`, `producer/src/capabilities.ts` |
| CrimefinderState wire (gRPC handlers) | `producer/src/state/*.ts` (append-finding, query-findings, update-status, append-coverage, run-tests-handler, commit-fix, defer-finding, skip-zone, request-help, aggregate-findings, get-zone-coverage, get-review-context, mark-duplicate) |
| Producer health endpoint | `producer/src/health.ts` |
| Producer gRPC server bootstrap | `producer/src/server.ts`, `producer/src/main.ts` |
| Template + sub-graph + prompts | `templates/code-review-pass.yml`, `templates/prompts/*.md`, `templates/validate.mjs` |
| Host executor gRPC server | `executor/src/server.ts`, `executor/src/main.ts` |
| Host executor agent-run pipeline | `executor/src/agent-run.ts`, `executor/src/stub-mode.ts`, `executor/src/silence-watch.ts` |
| Claude CLI subprocess + auth | `executor/src/cli-runner.ts`, `executor/src/cli-env.ts` |
| Executor → producer typed client | `executor/src/state-client.ts` |
| Prompt loader (userdata-supplied) | `executor/src/prompt-loader.ts` |
| Executor capabilities + observability | `executor/src/capabilities.ts`, `executor/src/userdata-schema.ts`, `executor/src/observability.ts` |
| CLI wrapper | `cli/src/main.ts`, `cli/src/commands/*.ts`, `cli/src/rimsky-cli.ts` |
| Shared types package | `shared/src/*.ts` (ids, fingerprint, error-classes, jsonl-rows, gate-io, scope-addresses, named-events, class-codec) |
| Proto package | `proto/v1/crimefinder_state.proto` |
| Deploy artifacts | `deploy/Dockerfile.producer`, `deploy/docker-compose.fragment.yml`, `deploy/rimsky.yml.fragment` |
| Scenario test harness + tests | `test/scenarios/harness.ts`, `test/scenarios/*.test.ts` |
| Gated end-to-end smoke | `test/e2e/smoke.test.ts` |
| Real-rimsky integration harness + scenario | `test/integration/harness.ts`, `test/integration/full-pass.test.ts`, `test/integration/fixtures/full-pass/`, `test/vitest.integration.config.ts` |
