---
concept: rimsky
aliases:
  - rimsky-cli
---

# rimsky (CLI)

## What it is

The rimsky CLI is the operator-facing command-line tool: a thin client over the control API for operating a deployed rimsky stack. It carries two embedded one-shot modes that self-host a runtime stack instead of dialing a deployed one — one drives a single template to terminal, the other drives a manifest of them — and both rest on the same self-host machinery. It also carries the conformance suite for each peer protocol, each of which dials a peer service over that service's own protocol (see `concept:conformance`). The CLI is the binary an operator invokes directly; the stack it embeds reuses the same role implementations the deployed binaries run, configured for a single ephemeral run rooted in a per-run artifact directory. The binary's name is the project's name.

## Purpose

The rimsky CLI is an operator's tool of first resort: one binary reaches a deployed stack, stands up a throwaway one, and proves a peer service's wire compatibility. Passing operator requests straight through keeps client-side logic from duplicating the validation the server already performs.

## Boundaries

The rimsky CLI owns its command-line experience, the requests it builds, the bundled role definitions it ships (see `concept:role-template`), the resolution of a template spec's source-file references before it submits that template, the bundling of the host-agent daemon into the same binary (see `concept:host-agent`), and client-side resolution of a service alias to a late-bound service. What reaches the wire is always resolved bytes.

The CLI exposes its capabilities as surfaces grouped by operator workflow: a dev-loop surface for bringing work up interactively against a deployed stack, a compose surface for manifest-driven project orchestration, a resource surface reaching the control API's resource families directly, a context surface for switching between configured endpoints and their credentials, an authentication surface covering the life of an api-key (see `concept:api-key`), a host-agent control surface for the embedded daemon, and a protocol-conformance surface a service implementer runs against its own service to prove wire compatibility. The surfaces are the durable model; which verbs belong to each one is the CLI's to carry.

The CLI does not own the control API's routes or the enforcement of authentication, both of which are the server's — the CLI only carries the operator's credential to it. It does not own the conformance library its surface wraps, which belongs to `concept:conformance`.

see also: `control-api`, `tag`, `instance`, `api-key`, `role-template`, `host-agent`, `host-agent-proxy`, `conformance`

## Aliases

- rimsky-cli
