---
concept: rimsky-yml
status: as-is
aliases:
  - unified config
references:
  - _discover/2026-05-10-unified-rimsky-yml-config.md
  - _discover/2026-05-10-postgres-only-runtime-state.md
  - _discover/2026-05-10-write-semantics-envelope-handshake.md
---

# rimsky.yml

## What it is

A single YAML file at a well-known default path (overridable by a config-path environment variable) read by all three runtime processes plus the migrate step. Declares: a persistence block (driver + blob sub-block + retention), a named-locks block, a claim-producers block, an executors block, and a publishers block. Each service entry has an optional protocol-membership list (defaulting to claim-producer only) declaring which rimsky protocols the binary speaks. A single config loader parses it.

## Purpose

The producer list and executor list are needed by every service-orchestrating process. A single file eliminates drift. The unified entrypoint sets only a process-role environment variable; everything else is in the YAML.

## Boundaries

Owns: the file shape, validations at startup (write-semantics-allowed subset, blob backend gating), the loader. Does NOT own: service protocol shapes (those are the protocol concepts' territory), per-feature defaults (live in code). Adjacent: `claim-producer`, `executor`, `lifecycle-subscriber`, `service`, `blob-backend`, `persistence-database`, `write-semantics`.

## Invariants

- Single file consumed by all three processes; no per-process config files.
- `claim_producers:` is the canonical key; `stores:` alias retired per `spec:2026-05-12-nomenclature-resolution` Group B.6 / C.1.
- `write_semantics_allowed: [...]` is required per producer (renamed from `write_semantics_envelope:` per `spec:2026-05-12-nomenclature-resolution` Group C.2); legacy single-value `write_semantics:` shortcut retired (Group C.1).
- Operator-declared `write_semantics_allowed` MUST be ⊆ producer-advertised set (validated at startup).
- The legacy DB-URL environment variable is gone; all DSN config goes through the YAML.
- Each service entry declares its protocol membership via an explicit protocol-membership list.
- **No auth-related keys.** Auth state is data-derived (the active-status predicate over the persisted API-key ledger; see `concept:anonymous-mode`). Operators do not configure an auth mode, a bootstrap key, or any other auth knob in the yml file. The data state of the API-key ledger is the sole source of truth.

## Aliases and historical names

Pre-`spec:2026-05-12-nomenclature-resolution`, the YAML accepted `stores:` as an alias for `claim_producers:` and accepted `write_semantics: <single value>` as a one-element shortcut for `write_semantics_envelope:`. Both aliases (and the `_envelope` suffix itself) are retired; the parser rejects them with a precise error message. The pre-2026 vocabulary used "peer" colloquially for service-orchestrated binaries; the current vocabulary is `service` (see `concept:service`).

## Open within this concept

(none live; tensions on `stores:` alias retirement, `write_semantics:` single-value shortcut, and `write_semantics_envelope` rename all resolved by `spec:2026-05-12-nomenclature-resolution`.)

## Notes

- `stores:` alias retired (Group B.6 / C.1); `write_semantics:` single-value shortcut retired (Group C.1); `write_semantics_envelope` → `write_semantics_allowed` (Group C.2); peer → service vocabulary swept (Group G). Per `spec:2026-05-12-nomenclature-resolution`. Resolves `tension:_resolved/yaml-stores-alias` and `tension:_resolved/yaml-write-semantics-alias`.
- [2026-05-15] Clarifying addition: the config file carries no auth-related keys; auth state is data-derived (see `concept:anonymous-mode`). Added by `spec:2026-05-15-control-plane-mcp-and-auth-design`.
- 2026-05-19 — A single service binary that plays multiple protocol roles (e.g. the bundled postgres store acting as both `concept:claim-producer` and `concept:executor`) is registered under each role's namespace in this file. Reusing the same logical name across the claim-producers and executors blocks for one binary is the canonical pattern; the entries' YAML shapes differ per the existing per-namespace conventions (URL-scheme endpoint for claim-producers, a transport selector plus bare host:port for executors). Per-namespace protocol enumerations are unchanged by this addition: claim-producer entries continue to advertise claim-producer (plus optional mix-ins); executor entries advertise executor. The new pattern is "same binary registered in both namespaces," not "new protocol values in either namespace." Per `spec:2026-05-19-multi-instance-template-ergonomics-design`.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.

