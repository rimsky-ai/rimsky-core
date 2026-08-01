---
issue: story-host-agent-anonymous-mode-proof-under-exhibits
kind: audit
category: proof
artifacts:
  - story:host-agent-anonymous-mode
  - code:test/scenarios/host_agent_anonymous_multi_agent_isolation_test.go
status: repaired
opened: 2026-07-24T00:00:00Z
---

# The test that promises "work never reaches the wrong agent" can't actually tell which agent got the work

Question: could a routing bug that silently swapped which agent ran which instance's dispatch — completing everything correctly, but on the wrong machine — pass `TestHostAgentAnonymousModeMultiAgentIsolation` green?

Under the current artifact-definitions.md (v14.1.0), a story carries no `Proof:`/Falsifier field — verification of `story:host-agent-anonymous-mode` is the periodic implementation audit's job, and the audit judges the ordinary test suite. `story:host-agent-anonymous-mode`'s `## Story` statement concretely promises "dispatches reach its target agent, and no other" / "dispatches for one never reach the other" — a mechanical, decidable claim, not a qualitative one (`{{DECIDABILITY-BOUNDARY}}`). Reproducing the filed Problem against the live test confirmed it still held: the stub child spawned by a host-agent recorded nothing about which agent spawned it, so `countWorkerRuns` could only assert "exactly one dispatch," never "the right agent did it." This is exactly the mechanical-rule example "add a missing assertion in a cited test" — the fix changes no commitment, only makes the existing test check what the story already promises.

What changed: `lib/runtime/hostagent/spawn.go` now forwards the spawning agent's routing label into the spawned child's environment as `RIMSKY_AGENT_ROUTING_LABEL` (a new `agentRoutingLabelEnvVar` constant, also now reused by `lib/runtime/hostagent/config.go`'s own read of that same variable). `test/scenarios/host_agent_anonymous_multi_agent_isolation_test.go` now gives each instance's binding its own `STUBCHILD_EXEC_LOG` and reads back the recorded `RIMSKY_AGENT_ROUTING_LABEL` value per instance, asserting it equals the target agent's own label. No `design/` file needed editing — the story's existing wording already stated the promise this test now checks.

How verified: `go build ./...` clean; `go test ./test/scenarios/... -run TestHostAgentAnonymousModeMultiAgentIsolation -v -count=1` passes; a manual fault injection (forcing the forwarded label to a wrong constant) reddened the test, confirming it now genuinely catches cross-routed dispatch; `go test ./test/plumbline/... -run TestEveryLiveEnvVarReadIsRegistered` stays green (the env var was already registered via `config.go`'s pre-existing read); `golangci-lint run ./lib/runtime/hostagent/... ./test/scenarios/...` clean.
