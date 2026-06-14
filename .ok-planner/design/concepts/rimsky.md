---
concept: rimsky
status: as-is
aliases:
  - rimsky-cli
---

# rimsky (CLI)

## What it is

Operator-facing CLI for rimsky: a thin HTTP+JSON client over the control-api for operating a deployed rimsky stack, plus an embedded one-shot orchestration mode that self-hosts the runtime stack to drive a manifest to terminal without standing up rimsky infrastructure. The CLI is the binary operators invoke directly; the embedded stack reuses the same role implementations as the deployed binaries, configured for a single ephemeral run rooted at a per-run artifact directory.

The binary name is `rimsky`.

## Purpose

Operator tool of first resort. Thin pass-through means there's no client-side business logic duplicating server validation, and a new CLI release tracks the control-api routes by hand rather than via codegen.

## Boundaries

Owns: command-line UX, request building, the `compose:` prefix reservation discipline (client-side only), the bundled role definitions (see `concept:role-template`), resolution of `source_file:` references in spec YAML at template-register time, before the wire call that submits the template, the host-agent-daemon bundling (the CLI binary doubles as the `concept:host-agent` daemon when invoked as `rimsky agent start`), and client-side `--service` alias resolution. The wire-side spec is always resolved bytes. Does NOT own: control-api routes (server-side), authentication enforcement (server-side; the CLI carries a Bearer token via a `--key` flag or an API-key environment variable). Adjacent: `concept:control-api`, `concept:tag`, `concept:instance`, `concept:api-key`, `concept:role-template`, `concept:host-agent`.

## Invariants

- HTTP+JSON only; no proto. The CLI assumes the routes it knows are present.
- `compose:<project>:<...>` tag and instance-key prefix reservation is enforced client-side only.
- The `compose` workflow uses the prefix to scan/diff/teardown project artifacts via the server's tag/key tables.
- **API key resolution**: every verb takes `--key=<token>` and falls back to an API-key environment variable. `auth status` and `auth init` tolerate a missing key (anonymous-mode bootstrap path); other verbs send the key as a Bearer token and surface 401 when missing.
- **`auth init` is special.** It posts a key-creation request without a Bearer token (anonymous-mode bootstrap) and refuses to run when any active key exists — the server's anonymous-mode predicate is the authoritative gate; the CLI's pre-check is a UX nicety.
- **`rimsky run` template + param + service flags.** A template is supplied by either a positional `<file>` argument or `--template <name>` (mutually exclusive). Params are supplied by `--params <json>` and/or repeatable `--param k=v` (mixable, later-wins). `--service <name>=<path>` binds a late-bound service.
- **Per-context api-key.** Each CLI context grows an api-key field alongside its endpoint, populated by `auth login` and consumed by the `concept:host-agent` for outbound authentication. The api-key field is optional on a context config.

## Subcommand groups

- **Dev loop**: `run`, `register`, `deploy`, `undeploy`, `instantiate`, `rm-instance`, `ls`, `logs`, `health`, `init`
- **Compose**: `compose`, `dev`
- **Literal API**: `template`, `tag`, `instance`, `node`, `admin`, `messages`, `backfill`, `asset`, `lineage`, `parked`
- **Context**: `ctx`
- **Auth**: `auth init | login | create-key | list | show | revoke | rotate | status`
- **Agent**: `agent start | status | stop` — runs the bundled `concept:host-agent` daemon
