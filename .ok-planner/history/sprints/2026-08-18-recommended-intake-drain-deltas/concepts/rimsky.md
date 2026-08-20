---
concept: rimsky
aliases:
  - rimsky-cli
---

# rimsky (CLI)

## What it is

Operator-facing CLI for rimsky: a thin HTTP+JSON client over the control-api for operating a deployed rimsky stack. It also carries two embedded one-shot orchestration modes that self-host the runtime stack without standing up rimsky infrastructure. The ephemeral-run verb (self-hosted by default when no endpoint is present) drives a single template to terminal, and the compose one-shot drives a compose manifest to terminal; both share the self-host machinery under the compose-run implementation. The binary also carries the per-protocol conformance suites. Each suite dials a peer service over that service's own protocol (see `concept:conformance`). The CLI is the binary operators invoke directly; the embedded stack reuses the same role implementations as the deployed binaries, configured for a single ephemeral run rooted at a per-run artifact directory.

The binary name is the same as the project name.

## Purpose

Operator tool of first resort. Thin pass-through means there's no client-side business logic duplicating server validation, and a new CLI release tracks the control-api routes by hand rather than via codegen.

## Boundaries

Owns: command-line UX, request building, the bundled role definitions (see `concept:role-template`), resolution of source-file references in spec YAML at template-register time, before the wire call that submits the template, the host-agent-daemon bundling (the CLI binary doubles as the `concept:host-agent` daemon when invoked under the agent-start verb), and client-side service-alias resolution for late-bound services. The wire-side spec is always resolved bytes. Does NOT own: control-api routes (server-side), authentication enforcement (server-side; the CLI carries an authentication token via a key flag or an API-key environment variable), the conformance library the surface wraps (see `concept:conformance`). Adjacent: `concept:control-api`, `concept:tag`, `concept:instance`, `concept:api-key`, `concept:role-template`, `concept:host-agent`, `concept:host-agent-proxy`, `concept:conformance`.

## Invariants

- The control-api client speaks HTTP+JSON only; no proto. The CLI assumes the routes it knows are present. The embedded self-host stack and the conformance surface speak the peer protocols in their own wire formats.
- The compose workflow prefixes every tag and instance key it creates with the manifest's project name. It scans, diffs, and tears down the project's artifacts by that prefix through the server's tag and key tables. The server reserves no prefix. Compose manages any tag or key another client names under a project's prefix like its own.
- **API key resolution**: every verb that dials the control-api accepts an API-key flag. The verb falls back to an API-key environment variable, then to the current context's key. It sends the resolved key as the authentication token. When no key resolves, it reports the server's unauthorized response. The auth-status and anonymous-bootstrap surfaces tolerate a missing key. These verbs stand outside the rule, and no others do:
  - The context-management verbs and the host-agent status and stop verbs read and write local state only. They dial no control-api and define no key flag.
  - The compose one-shot self-hosts the stack it drives and reaches it over loopback. It presents no operator credential.
  - The host-agent start verb hands the key to the `concept:host-agent-proxy` at registration, under its own flag. The proxy verifies the key against the control-api.
  - The interactive login verb reads the key from the terminal and stores it in the context.
  - The conformance verbs dial the service under test over that service's own protocol. They present no control-api key on that call.
- **Anonymous-mode bootstrap is special.** It posts a key-creation request without an authentication token and refuses to run when any active key exists — the server's anonymous-mode predicate is the authoritative gate; the CLI's pre-check is a UX nicety.
- **Ephemeral-run template + param + service surfaces.** The ephemeral-run verb resolves a template by either a positional file argument or a named-template flag (mutually exclusive), and plays a dual role: self-hosted by default when no endpoint is present; remote dispatch when an endpoint flag is passed or a context endpoint is configured; an explicit self-host flag overrides a configured context. Params are supplied via a whole-params-blob flag and/or a repeatable per-entry flag (mixable, later-wins). A late-bound service binds a service name to a local binary path.
- **Per-context api-key.** Each CLI context grows an api-key field alongside its endpoint, populated at login time and consumed by the `concept:host-agent` for outbound authentication. The api-key field is optional on a context config.
- **Source-file resolution is confined to the template's directory subtree.** A source-file reference resolves relative to the template file's own directory; an absolute path or a reference escaping the subtree is rejected as an error before anything is sent to the server.

## Capability surfaces

The CLI exposes capability surfaces grouped by operator workflow. The durable model is the surfaces themselves; membership of each surface's verb set is owned by the CLI code and its operator-facing reference, not enumerated here.

- **Dev-loop surface** — interactive bring-up of work against a deployed stack (e.g. an ephemeral run).
- **Compose surface** — manifest-driven project orchestration (e.g. compose up).
- **Resource surface** — direct access to the control-api's resource families (e.g. templates).
- **Context surface** — switching between configured endpoints + credentials.
- **Authentication surface** — the API-key lifecycle, including anonymous-mode bootstrap (e.g. key creation; see `concept:role-template` for the bundled grant templates and `concept:api-key` for the wire-side key model).
- **Host-agent control surface** — lifecycle control for the embedded `concept:host-agent` daemon (e.g. starting it).
- **Protocol-conformance surface** — per-protocol suites a service implementer runs against their own endpoint to prove wire compatibility (see `concept:conformance`).
