---
topic: rimsky-cli-compose-prefix-reservation
kind: discipline
---

# `compose:<project>:<...>` tag and instance-key prefix reserved for `rimsky-cli compose`; rejection is client-side only

## Description

Rimsky's tag namespace (`rimsky_template_tags`) and instance-key namespace are operator-managed strings. Tags are movable aliases pointing at content-addressed template hashes; instance keys are caller-supplied dedup keys. Both are free-form strings on the server side — the only constraints are uniqueness within their respective tables.

The `rimsky-cli compose` subcommand uses these strings to track manifest-driven deployments. A `compose:<project>:<name>` tag identifies a template that the CLI's compose workflow produced from a manifest file; a `compose:<project>:<name>` instance-key identifies an instance the workflow created. The prefix is reserved for the CLI's exclusive use.

CLAUDE.md "Non-obvious gotchas": "Compose owns project-prefixed names. Tags `compose:<project>:<...>` and instance keys `compose:<project>:<...>` are reserved for `rimsky-cli compose`. The CLI rejects manual registration with this prefix client-side."

The reservation is **client-side only**:

- The CLI checks the tag/key string at command-time and rejects names matching `compose:*` outside the `compose` subcommand.
- The server (control-api) does not enforce the reservation. A manual `curl POST /templates` with a `compose:` tag is accepted.

This is a deliberate trade-off documented in CLAUDE.md "Non-obvious gotchas": "The `compose:` prefix is client-side reserved — a future enforcement at the API would be an additive change." The CLI is the operator's tool of first resort; client-side reservation handles the common case without committing the server to a naming policy.

The compose workflow uses the prefix to:

- Find the template/instance pair belonging to a project (by prefix scan).
- Diff manifest state vs deployed state for the same project.
- Tear down all artifacts of a project (by deleting everything matching the prefix).

Without a reserved prefix, the compose workflow couldn't safely "delete everything for this project" — it would have to track its own state externally. The prefix reservation lets the CLI use the server's tag/key tables as the project's own state.

`docs/concepts/instance.md` notes the `instance_key` is "an optional dedup hint" supplied by the caller; the canonical instance ID is the rimsky-generated UUID. `compose:` keys are still hints in this sense — they're not load-bearing identifiers, just reservation tokens.

A future API-level enforcement would be a CHECK constraint or a chi route middleware that rejects `compose:` strings outside an authenticated-as-compose endpoint. The CLAUDE.md note explicitly leaves this as an additive future change.

## Code surface

- `cmd/rimsky-cli/` — main + compose subcommand (look for prefix validation).
- `modeling/cli/` — request builders; may not enforce.
- `foundation/persistence/templates.go` — tag CRUD (no enforcement).
- `foundation/persistence/instances.go` — instance-key CRUD (no enforcement).

## Prose surface

- `CLAUDE.md` "Non-obvious gotchas" — explicit reservation note.
- `docs/concepts/template.md` — "movable human-readable aliases" framing of tags.
- `docs/concepts/tag.md` — tag concept doc (if present).
- `docs/concepts/instance.md` — `instance_key` semantics.

## Adjacent topics

- `2026-05-10-rimsky-cli-thin-client` — CLI as thin HTTP client.
- `2026-05-10-content-addressed-templates` — tags pointing at template hashes.
- `2026-05-10-lifecycle-subscriber-opt-in` — `OnTemplateDeployed.tags` carries compose tags.

## Observations

- The reservation is convention-based; a non-CLI tool that also wants project-scoped naming could collide. The fix (server-side enforcement) is acknowledged but deferred.
- The prefix scheme is `compose:<project>:<...>` (two colons mandatory). A `compose:foo` tag without the project structure would still be allowed by the CLI's check (since it doesn't fit the `compose:<project>:<...>` template) but wouldn't fit the workflow either — a halfway-house case.
- Lifecycle subscribers receive the tag at `OnTemplateDeployed` (per `2026-05-10-lifecycle-subscriber-opt-in`); a subscriber that wants to surface "compose project X" can scan tags for the prefix.
- The compose workflow's "tear down all of project X" operation depends on the prefix being predictable. If a future API-level enforcement reshapes the namespace (e.g. into a separate `compose_artifacts` table), the workflow would need to be updated.
