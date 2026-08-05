---
issue: claude-agent-callback-host-env-unconsumed
kind: human
category: operator-config
artifacts:
  - concept:executor
  - decision:operator-env-namespaced-per-service
  - decision:env-var-registry
  - story:claude-agent
status: answered
opened: 2026-07-22T10:18:16Z
github: https://github.com/rimsky-ai/rimsky-core/issues/29
---

# Does `RIMSKY_EXECUTOR_CALLBACK_HOST` still get read into an unconsumed claude-agent option field?

Question: the filed Problem claimed `lib/services/executors/claude-agent/opts.go` read `RIMSKY_EXECUTOR_CALLBACK_HOST` into `Opts.CallbackHost` while nothing consumed it, offering operators a no-op knob.

Answer: the gap no longer exists. `opts.go`'s `Opts` struct carries no `CallbackHost` field and `LoadOptsFromEnv` no longer reads `RIMSKY_EXECUTOR_CALLBACK_HOST` at all (`lib/services/executors/claude-agent/opts.go`); the executor's internal MCP server hardcodes `127.0.0.1` as its default host (`lib/services/executors/claude-agent/agentrun.go:254`, `internalmcpserver.go:96`). The dead knob was deleted rather than wired up — the second of the issue's two candidates — landed in commit `2ef58038` ("claude-agent no longer reads RIMSKY_EXECUTOR_CALLBACK_HOST (dead knob deleted; loopback is the only behavior). Closes gh#29"). The env-var registry (`tools/env-registry/registry.md`) carries no entry for the retired name, so nothing here is left for a sprint to carry.
