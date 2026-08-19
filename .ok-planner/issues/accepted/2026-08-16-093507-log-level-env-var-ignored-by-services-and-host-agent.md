---
issue: log-level-env-var-ignored-by-services-and-host-agent
kind: audit
category: inconsistent
artifacts:
  - decision:logging-slog-only
  - decision:env-var-convention-across-modes
  - decision:env-var-registry
status: promoted
opened: 2026-08-16T09:35:07Z
sprint: 2026-08-18-recommended-intake-drain.md
---

# The core roles honour the log-level variable; every bundled service and the host agent ignore it

The core roles, the migrate binary, the entrypoint and the proxy read a shared log-level variable into a structured JSON handler. None of the eleven bundled services reads it. Each pins an info-level handler. The host agent started through the CLI loads and defaults a log-level field that nothing consumes, and it logs through the standard library's default text handler. Only the standalone host-agent binary honours the variable, and no image ships that binary. An operator who sets the variable on a service container changes nothing. One decision commits bundled handlers to read operator config from the same names across modes. Another decision says new bundled-service knobs are namespaced per service. The ruling decides which naming a cross-cutting knob takes. The host agent's unconsumed field is a bug either way.

## Options

- Commit every rimsky-authored process to the one unnamespaced log-level variable and the structured format, and add a check that enumerates the binaries; cost: an exception to per-service namespacing.
- Make log level a namespaced per-service knob and retire the one-variable framing; cost: eleven new variables, and an operator can no longer set one variable and have everything follow.

The ruling decides how log level is configured across processes. The host agent is fixed regardless.

## Ruling

> Recommended ruling (/verify-issues): Use one variable. Every rimsky-authored process honours the shared log-level variable and logs structured JSON, the bundled services and the CLI-started host agent included, and a fitness check over the binaries pins that. Name the variable as the deliberate exception to per-service namespacing, which governs policy knobs and not infrastructure ones.
>
> Rationale: an operator's log level is a deployment-wide setting. The namespacing decision's own rationale is collision and provenance within one process, and that does not apply to a knob every process shares. Flip case: if one noisy sensor makes per-service verbosity a real need, add namespaced overrides on top of the shared default rather than in place of it.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
