---
decision: operator-env-namespaced-per-service
---

# New bundled-service operator env vars are namespaced per service

## Choice

Operator env vars introduced for a bundled service carry a per-service prefix of the form RIMSKY_<SERVICE>_*. The generic per-executor env vars (host, ports, binary override, declared tags, timeouts, stub mode) stay unprefixed — they are relevant only to standalone deployment; an in-process handler binds no ports and reads no transport envs, so cross-service collision does not materialize there. The rule governs a service's own policy knobs. An infrastructure knob that every rimsky-authored process reads the same way stays unprefixed and carries one name across the deployment. The log level is such a knob (see `decision:logging-slog-only`).

## Rationale

In a unified process the handler shares its environment with rimsky-core's own env vars and other bundled handlers'. Namespacing prevents collision on the new operator envs and makes provenance obvious. Neither reason applies to a knob every process reads identically. One name that means one thing everywhere collides with nothing, and its provenance is the whole deployment.

## Alternatives

- Non-namespaced names — rejected: risks collision with unrelated env in the host process; makes provenance unclear.
- Namespace the log level per service alongside the policy knobs — rejected: an operator sets deployment-wide verbosity once, and a per-service name makes that eleven settings.
