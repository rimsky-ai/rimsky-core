---
topic: conformance-probe-stub-mode-handshake
kind: discipline
---

# `--require-stub-mode` + `rimsky-conformance-probe` sidecar prevents real-money LLM calls in conformance runs

## Description

LLM-calling executors (notably `executors/claude-agent/`) can be configured to run against real Claude APIs or against a stub that returns canned responses. Conformance runs would naturally hit real APIs unless the operator is careful; for an executor that costs real money per call, this is a budget risk.

`cmd/rimsky-executor-conformance` accepts the `--require-stub-mode` flag. When set, it issues a startup probe via the companion binary `cmd/rimsky-conformance-probe` against the target executor's `NodeExecutor.Execute` with a sentinel input. The probe verifies the executor returns the documented stub-mode signature in its response. If the probe fails or the response indicates non-stub mode, conformance refuses to proceed.

The pattern is captured in CLAUDE.md "Non-obvious gotchas": "Stub mode is required for conformance runs of LLM-calling executors. `rimsky-executor-conformance --require-stub-mode` issues a probe via `rimsky-conformance-probe` at startup; non-stubbed executors will fail."

The probe is a separate binary so the same probe logic can be invoked outside conformance (e.g. as a Kubernetes init container that verifies an executor pod is configured for stub mode before letting it accept production traffic). Conformance itself shells out to the probe binary.

The reference executor's stub-mode is configured by `RIMSKY_EXECUTOR_STUB_MODE=1` env var (CLAUDE.md "Reference deployment & local stack" mentions this as the docker-compose default). The TS executor's `cli-runner.ts` checks this and routes to stubbed responses instead of `claude` CLI spawning.

The decision to gate by environment-variable + protocol-level probe is structural:

1. **Env-var alone** is operator-trustworthy ("did the operator set the flag?") but not verifiable from the conformance harness — the operator could lie or forget.
2. **Probe alone** would be wire-format-explicit but require the executor to declare its mode in `Capabilities()` or every response.
3. **Both** is the chosen path: the env-var is the operator's intent declaration; the probe is the wire-protocol-level verification. Conformance refuses to proceed if the two disagree.

This is one of two places rimsky treats executor configuration as load-bearing (the other is the `userdata_schema` reported via `ExecutorObservability` for dispatch-time validation). Both are intentional opacity exceptions: the schema is metadata, the stub-mode probe is a safety property.

A conformance run without `--require-stub-mode` is allowed (no probe fires); the operator takes responsibility. This is the right default for stub-only executors (like `executors/stub/`) that have no real-API path.

## Code surface

- `cmd/rimsky-executor-conformance/main.go` — `--require-stub-mode` flag + probe invocation.
- `cmd/rimsky-conformance-probe/main.go` — entire probe (typically small).
- `executors/claude-agent/src/cli-runner.ts` — stub-mode env var check.
- `executors/claude-agent/src/cli-env.ts` — auth config (real vs stub).

## Prose surface

- `CLAUDE.md` "Non-obvious gotchas" — full description.
- `CLAUDE.md` "Build & test" — `rimsky-executor-conformance --require-stub-mode` example.

## Adjacent topics

- `2026-05-10-conformance-test-binaries` — parent family of conformance binaries.
- `2026-05-10-typescript-executor-claude-agent` — the executor that benefits most from this gate.
- `2026-05-10-observability-optional-protocols` — `userdata_schema` is the other place rimsky reads executor-side declarations.

## Observations

- The probe is a binary, not a library function. Conformance shells out, which means a future test harness wanting the same gate would also need to shell out (or reimplement the protocol-level call). A re-export of the probe logic as a Go function might simplify future tooling.
- The stub-mode env var is set by the executor; the probe verifies the executor's behavior, not the env var directly. This means a misconfigured executor that ignores the env var still gets caught.
- A test harness that runs against `executors/stub/` doesn't need `--require-stub-mode` because the stub executor never makes real calls. The flag is opt-in.
- The probe's sentinel input + response shape is implicit in the cmd code; a future documented protocol for "stub-mode signature" would let third-party LLM-calling executors implement the same gate.
