---
decision: rimsky-run-self-hosts-templates
status: as-is
aliases: []
---

# The ephemeral-run verb self-hosts an all-in-one stack when no target endpoint is present

## Choice

The ephemeral-run verb, given a template file and no target endpoint, boots an in-process all-in-one stack — reusing the compose one-shot's self-host machinery — drives the template to terminal against the local control-api, then exits and tears the stack down. A target endpoint, whether passed explicitly or configured via the CLI context, suppresses self-hosting and keeps the dev-loop dispatch against a remote rimsky; an explicit self-host flag bypasses a configured context endpoint, and combining it with an explicit endpoint is a usage error. Self-host is inherently one-shot — nothing survives the process — so options that presuppose a surviving instance row or a pre-existing template registry are usage errors under it.

## Rationale

Presence of a target endpoint is a natural discriminator for the common case: a user with nothing configured gets a working local run out of the box, and a user pointed at a real deployment keeps the remote dispatch behavior untouched. The explicit self-host flag is the escape hatch for stale context configs.

## Alternatives

- New top-level verb for local one-shots — rejected: bloats the CLI surface for no ergonomic gain.
- Defaulting the endpoint to a well-known local port — rejected: guesses at infrastructure that may not exist and turns a clear "nothing configured" state into a connection error.
