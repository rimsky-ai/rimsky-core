---
audit: spawned-local-services
artifact: story:spawned-local-services
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:40:12Z
---

# `--service <name>=<path>` lets a binary be spawned for one run and torn down with it

Supported. `cmd/rimsky/cli/run.go` exposes the `--service` flag documented exactly as the story describes (a name-to-local-binary-path binding, repeatable). `test/scenarios/host_agent_cli_autostart_test.go`'s `TestCLIRunService_AutoStartsAgentAndSpawnsHoldsReapsBoundBinary` proves the full lifecycle end to end: a fresh `HOME` with no agent running, `rimsky run --service <name>=<path> --endpoint ...` auto-starts the host-agent daemon, the bound binary is spawned and dispatched to, and the run reaches `terminal/success` — no service installer or pre-existing daemon required. "Disappears with it" (the single-run lifetime) is backed by `cmd/rimsky/cli/compose/shutdown_test.go`'s `Drain` tests, which confirm the shared shutdown coordinator SIGTERMs and (on resistance) SIGKILLs every spawned service process before the run command returns, and by the host-agent's own per-run-scope reap (see `story:host-agent-per-run-scope-isolation`).
