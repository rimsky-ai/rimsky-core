---
decision: rimsky-run-self-hosts-templates
status: as-is
aliases: []
---

# The ephemeral-run verb self-hosts an all-in-one stack when no target endpoint is present

## Choice

`rimsky run <template>` boots an in-process all-in-one stack when no target endpoint is present — reusing the same self-host machinery the compose one-shot uses: run directory with synthetic config, bundled handler registration, role stack in-process, control-api readiness wait. It then registers, deploys, and instantiates the template against the local control-api, streams events until the instance reaches terminal, and exits, tearing the stack down. Passing `--endpoint <url>` (or having one configured via the CLI context) suppresses self-hosting and keeps the existing dev-loop dispatch against a remote rimsky. A user with a configured context endpoint who wants explicit self-host passes `--self-host`, which bypasses the context endpoint; combining `--self-host` with an explicit `--endpoint` is a usage error. Self-host is inherently one-shot: the process exits once the instance reaches terminal, tearing the stack down; the instance row and its history don't survive the process so `--keep` is a usage error there. `--template <name>` (an already-registered template) is likewise a usage error under self-host — a freshly booted stack has no registry to look the name up in.

## Rationale

Presence of a target endpoint is a natural discriminator for the common case: a user with nothing configured gets a working local run out of the box, and a user pointed at a real deployment keeps today's behavior untouched. `--self-host` is the explicit escape hatch for stale context configs.

## Alternatives

- New top-level verb for local one-shots — rejected: bloats the CLI surface for no ergonomic gain.
- Defaulting the endpoint to a well-known local port — rejected: guesses at infrastructure that may not exist and turns a clear "nothing configured" state into a connection error.
