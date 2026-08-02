---
issue: service-spawn-dispatch-path-has-no-e2e-test
kind: audit
category: test-coverage
artifacts:
  - decision:late-bound-services-direct-spawn
  - decision:service-spawn-flag
status: repaired
opened: 2026-08-02T09:58:18Z
---

# Does either `run --service` or `compose run --service` have a test that dispatches through the spawned endpoint to terminal?

Both decisions describe a `--service <name>=<path>` flag that direct-spawns
a local binary and dispatches to it from the in-process supervisor with no
host-agent-proxy hop. Re-verification confirmed the flag shape and the
shared spawn primitive are real for both the self-host run verb and
compose-run, but no test exercised either path end to end — existing
tests covered flag parsing and the bundled in-proc executor only. The
remote/proxied late-bind path this pattern parallels IS proven end to end
(`test/scenarios/host_agent_cli_autostart_test.go`).

Rule that determined the fix: both mechanisms are already true in the
running code (confirmed by the two new tests below); only composed
end-to-end coverage was missing — outcome 2 (add the missing tests), no
commitment change.

What changed:
- Added `TestRunTemplateRun_SelfHostServiceFlagSpawnsBinaryAndDispatchesToTerminal`
  to `cmd/rimsky/cli/compose/template_run_test.go`: runs `RunTemplateRun`
  in-process with `--service codegen=<built stubchild binary>` against a
  template whose node's `executor:` names the spawned service directly,
  asserts exit 0 (terminal success reached via the spawned endpoint).
- Added `TestRunComposeRun_ServiceFlagSpawnsBinaryAndDispatchesToTerminal`
  to `cmd/rimsky/cli/compose/run_test.go`: same proof for `RunComposeRun`
  against a manifest+template pair.
- Both reuse the existing `lib/runtime/hostagent/testdata/stubchild` gRPC
  executor test fixture (built via `go build` at test time), the same
  binary the proxied late-bind e2e test already spawns.

Verified: both tests pass standalone (2.76s and 2.85s respectively,
no Docker — self-host/compose-run's unified-process SQLite stack runs
in-process). `go build ./cmd/...` and `go vet ./cmd/rimsky/cli/compose/...`
are clean; `go test ./cmd/rimsky/cli/compose/...` (full package) passes.
