---
issue: log-level-env-var-ignored-by-services-and-host-agent
kind: audit
category: inconsistent
artifacts:
  - decision:logging-slog-only
  - decision:env-var-convention-across-modes
  - decision:env-var-registry
status: verified
opened: 2026-08-16T09:35:07Z
---

# The log-level variable is honoured by the core roles and ignored by every bundled service and the host agent

The core roles, the migrate binary, the entrypoint and the proxy read a shared log-level variable into a structured JSON handler. None of the eleven bundled services reads it — each pins an info-level handler — and the host agent started through the CLI loads and defaults a log-level field that nothing consumes, logging through the standard library's default text handler; only the standalone host-agent binary (shipped in no image) honours it. An operator setting the variable on a service container gets a no-op. One decision commits bundled handlers to read operator config from the same names across modes; another says new bundled-service knobs are namespaced per service. The ruling decides which naming a cross-cutting knob takes; the host agent's unconsumed field is a bug either way.

## Options

- Commit every rimsky-authored process to the one unnamespaced log-level variable and the structured format, with an enumerating check over the binaries; cost: an exception to per-service namespacing.
- Make log level a namespaced per-service knob, retiring the one-variable framing; cost: eleven new variables and the loss of "set one var, everything follows".

The ruling decides how log level is configured across processes; the host agent is fixed regardless.

## Ruling

> Recommended ruling (/verify-issues): One variable — every rimsky-authored process, bundled services and the CLI-started host agent included, honours the shared log-level variable and logs structured JSON — pinned by a fitness check over the binaries; name it as the deliberate exception to per-service namespacing, which governs policy knobs, not infrastructure ones.
>
> Rationale: an operator's log level is a deployment-wide dial, and the namespacing decision's own rationale (collision and provenance in one process) does not apply to a knob every process shares. Flip case: if per-service verbosity is a real need (one noisy sensor), add namespaced overrides on top of the shared default rather than instead of it.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
