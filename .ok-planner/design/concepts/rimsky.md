---
concept: rimsky
status: as-is
aliases:
  - rimsky-cli
---

# rimsky (CLI)

## What it is

Operator-facing CLI for rimsky: a thin HTTP+JSON client over the control-api for operating a deployed rimsky stack. It also carries two embedded one-shot orchestration modes that self-host the runtime stack without standing up rimsky infrastructure. The ephemeral-run verb (self-hosted by default when no endpoint is present) drives a single template to terminal, and the compose one-shot drives a compose manifest to terminal; both share the self-host machinery under the compose-run implementation. The CLI is the binary operators invoke directly; the embedded stack reuses the same role implementations as the deployed binaries, configured for a single ephemeral run rooted at a per-run artifact directory.

The binary name is the same as the project name.

## Purpose

Operator tool of first resort. Thin pass-through means there's no client-side business logic duplicating server validation, and a new CLI release tracks the control-api routes by hand rather than via codegen.

## Boundaries

Owns: command-line UX, request building, origination of compose-tag-prefixed tags and instance keys as the server's designated compose origin, the bundled role definitions (see `concept:role-template`), resolution of source-file references in spec YAML at template-register time, before the wire call that submits the template, the host-agent-daemon bundling (the CLI binary doubles as the `concept:host-agent` daemon when invoked under the agent-start verb), and client-side service-alias resolution for late-bound services. The wire-side spec is always resolved bytes. Does NOT own: control-api routes (server-side), authentication enforcement (server-side; the CLI carries an authentication token via a key flag or an API-key environment variable). Adjacent: `concept:control-api`, `concept:tag`, `concept:instance`, `concept:api-key`, `concept:role-template`, `concept:host-agent`.

## Invariants

- HTTP+JSON only; no proto. The CLI assumes the routes it knows are present.
- The compose-tag prefix reservation on tag and instance-key namespaces is server-enforced; the CLI's compose workflow identifies itself as the compose origin so the server permits it to create prefixed tags and instance keys.
- The compose workflow uses the prefix to scan/diff/teardown project artifacts via the server's tag/key tables.
- **API key resolution**: every verb accepts an API-key flag and falls back to an API-key environment variable. The auth-status and anonymous-bootstrap surfaces tolerate a missing key; every other verb sends the key as the authentication token and surfaces an unauthorized response when missing.
- **Anonymous-mode bootstrap is special.** It posts a key-creation request without an authentication token and refuses to run when any active key exists — the server's anonymous-mode predicate is the authoritative gate; the CLI's pre-check is a UX nicety.
- **Ephemeral-run template + param + service surfaces.** The ephemeral-run verb resolves a template by either a positional file argument or a named-template flag (mutually exclusive), and plays a dual role: self-hosted by default when no endpoint is present; remote dispatch when an endpoint flag is passed or a context endpoint is configured; an explicit self-host flag overrides a configured context. Params are supplied via a whole-params-blob flag and/or a repeatable per-entry flag (mixable, later-wins). A late-bound service binds a service name to a local binary path.
- **Per-context api-key.** Each CLI context grows an api-key field alongside its endpoint, populated at login time and consumed by the `concept:host-agent` for outbound authentication. The api-key field is optional on a context config.

## Capability surfaces

The CLI exposes capability surfaces grouped by operator workflow. The durable model is the surfaces themselves; the verbs named below are illustrative of each surface's shape, not an exhaustive or owned contract — the CLI code and its operator-facing reference are authoritative for exact verbs and flags.

- **Dev-loop surface** — interactive bring-up of work against a deployed stack: ephemeral runs, template register / deploy / undeploy, instance instantiate / remove, listing, logs, health, and operator-init.
- **Compose surface** — manifest-driven project orchestration: compose-family verbs (up, down, plan, status, run) plus a dev shorthand.
- **Resource surface** — direct access to the control-api's resource families: templates, tags, instances, nodes, admin actions, messages, assets, lineage, parked-state.
- **Context surface** — switching between configured endpoints + credentials.
- **Authentication surface** — anonymous-mode bootstrap, login, key creation, listing, detail, revoke, rotate, and status (see `concept:role-template` for the bundled grant templates and `concept:api-key` for the wire-side key model).
- **Host-agent control surface** — start / status / stop verbs for the embedded `concept:host-agent` daemon.
