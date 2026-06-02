---
concept: sdk
status: retired
aliases:
  - rimsky-sdk
  - sdk/go
---

> **Retired** by `spec:2026-05-26-collapse-sdk-into-protocols`. The separate Go SDK module dissolved into the protocols module: the server-side and publisher-side implementer scaffolding, the claim-producer action vocabulary, and the conformance library all moved into the protocols module; the ops glue was demoted to a rimsky-internal package; the Postgres testcontainer helper was carved into its own opt-in test-helper module. For Go there is no separate SDK — the protocols module is the single public surface a service implementer imports. A future development kit serves a different purpose, is Python-first, and sits as an authoring layer above the contract; it is not a Go SDK successor and must not reintroduce a satellite Go module.

# SDK

## What it is

The Go-side SDK is the canonical implementer-facing surface for building services that rimsky talks to. It is a peer Go module within the rimsky repo, alongside the protocols module and the foundation module. Houses:

- Server scaffolding for claim-producer / executor / lifecycle-subscriber / blob-backend / publisher protocols
- Publisher-side helpers (message-emit retry+backoff, idempotency-key header, callback POST handling)
- A conformance library — invocable from service authors' Go tests in addition to the thin per-protocol CLI conformance wrappers
- Testcontainer helpers — plain Postgres provisioning for services testing their own state-DB schema. (Migrations-applying variants stay rimsky-internal, since they import the persistence layer, which the SDK-purity import boundary forbids in the SDK.)
- Ops glue — structured-logging setup, healthcheck HTTP endpoint, DSN env-var parser

## Purpose

Remove footguns from third-party and bundled service authors (canonical example: an async-callback executor must POST its callback body under the exact key the supervisor's callback route expects, and a mismatched key silently fails — the kind of cross-language wire-detail trap the helpers encode once). Provide one paved path to "implement a service rimsky calls."

## Boundaries

Owns: the implementer-facing surface listed above. Does NOT own: the calling-side wire code (rimsky-internal infrastructure tightly coupled to `concept:supervisor`, `concept:terminal-resolution`, `concept:discovery-cache` — stays in rimsky's runtime peer layer). Does NOT own: non-Go languages (a future TypeScript SDK would be a separate concept if/when it lands).

## Invariants

- SDK-purity import boundary: the SDK imports only the protocols module + stdlib + minimal third-party. No imports from the foundation, graph, runtime, control, or command-entrypoint layers.
- Lockstep tagging with rimsky-core: the root module and the SDK sub-module share a version and are cut by the same release script.
- Break-freely pre-v1 license. No deprecation-alias discipline; the release changelog is the visibility surface for breaks.

## Aliases and historical names

`rimsky-sdk` informally; "sdk/go" in path-form. Created in the 2026-05-24 reorganization (`spec:2026-05-24-repo-reorganization-design`).

## Notes

- 2026-05-24: created as part of the repo reorganization. SDK birth covered in `spec:2026-05-24-repo-reorganization-design` phase P2.
- 2026-05-24: the cross-implementer claim-producer action vocabulary was promoted out of the bundled stores into the SDK during the P3 bundled-services migration — it's implementer-facing surface, so it belongs on the SDK side of the boundary. Pass 5 ride-along (`spec:2026-05-24-repo-reorganization-design`).
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- 2026-05-26 — Retired per spec:2026-05-26-collapse-sdk-into-protocols. The separate Go SDK module dissolved into the protocols module: the server-side and publisher-side implementer scaffolding, the claim-producer action vocabulary, and the conformance library all moved into the protocols module; the ops glue was demoted to a rimsky-internal package; the Postgres testcontainer helper was carved into its own opt-in test-helper module. For Go there is no separate SDK — the protocols module is the single public surface a service implementer imports. A future development kit serves a different purpose, is Python-first, and sits as an authoring layer above the contract; it is not a Go SDK successor and must not reintroduce a satellite Go module.
- 2026-06-02 — drift correction (spec:2026-06-02-rimsky-core-remediation-design): the Postgres testcontainer helper, carved into its own opt-in test-helper module at retirement (2026-05-26), was subsequently demoted in the 2026-05-27 reorg to a plain rimsky-internal test-support package — it is no longer a public module. The glossary summary was reconciled to that end state (dissolved-into-protocols; pg helper → test-support) to match `concept:module-layout`. No change to the retirement decision itself.
