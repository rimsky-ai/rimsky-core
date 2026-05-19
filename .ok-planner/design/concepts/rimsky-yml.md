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

A single YAML file (default `/etc/rimsky/rimsky.yml`, env-var override `RIMSKY_CONFIG`) read by all three runtime processes plus the migrate step. Declares: `persistence:` (driver + blob sub-block + retention), `named_locks:`, `claim_producers:`, `executors:`. Each service entry has an optional `protocols: [claim_producer, lifecycle_subscriber]` list (default `[claim_producer]`) declaring which rimsky protocols the binary speaks. Loader is `code:control/config/`.

## Purpose

The producer list and executor list are needed by every service-orchestrating process. A single file eliminates drift. The unified `rimsky-entrypoint` sets only `RIMSKY_PROCESS_ROLE`; everything else is in the YAML.

## Boundaries

Owns: the file shape, validations at startup (write-semantics-allowed subset, blob backend gating), the loader. Does NOT own: service protocol shapes (those are in `protocols/`), per-feature defaults (live in code). Adjacent: `claim-producer`, `executor`, `lifecycle-subscriber`, `service`, `blob-backend`, `persistence-database`, `write-semantics`.

## Invariants

- Single file consumed by all three processes; no per-process config files.
- `claim_producers:` is the canonical key; `stores:` alias retired per `spec:2026-05-12-nomenclature-resolution` Group B.6 / C.1.
- `write_semantics_allowed: [...]` is required per producer (renamed from `write_semantics_envelope:` per `spec:2026-05-12-nomenclature-resolution` Group C.2); legacy single-value `write_semantics:` shortcut retired (Group C.1).
- Operator-declared `write_semantics_allowed` MUST be ⊆ producer-advertised set (validated at startup).
- `RIMSKY_DB_URL` legacy env var is gone; all DSN config goes through the YAML.
- Each service entry declares its protocol membership via `protocols: [...]`.
- **No auth-related keys.** Auth state is data-derived (the active-status predicate on `table:rimsky_api_keys`; see `concept:anonymous-mode`). Operators do not configure an auth mode, a bootstrap key, or any other auth knob in the yml file. The data state of `rimsky_api_keys` is the sole source of truth.

## Aliases and historical names

Pre-`spec:2026-05-12-nomenclature-resolution`, the YAML accepted `stores:` as an alias for `claim_producers:` and accepted `write_semantics: <single value>` as a one-element shortcut for `write_semantics_envelope:`. Both aliases (and the `_envelope` suffix itself) are retired; the parser rejects them with a precise error message. The pre-2026 vocabulary used "peer" colloquially for service-orchestrated binaries; the current vocabulary is `service` (see `concept:service`).

## Open within this concept

(none live; tensions on `stores:` alias retirement, `write_semantics:` single-value shortcut, and `write_semantics_envelope` rename all resolved by `spec:2026-05-12-nomenclature-resolution`.)

## Notes

- `stores:` alias retired (Group B.6 / C.1); `write_semantics:` single-value shortcut retired (Group C.1); `write_semantics_envelope` → `write_semantics_allowed` (Group C.2); peer → service vocabulary swept (Group G). Per `spec:2026-05-12-nomenclature-resolution`. Resolves `tension:_resolved/yaml-stores-alias` and `tension:_resolved/yaml-write-semantics-alias`.
- [2026-05-15] Clarifying addition: rimsky.yml carries no auth-related keys; auth state is data-derived (see `concept:anonymous-mode`). Added by `.ok-planner/specs/2026-05-15-control-plane-mcp-and-auth-design.md`.

