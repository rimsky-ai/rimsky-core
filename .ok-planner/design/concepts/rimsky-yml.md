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

A single YAML file (default `/etc/rimsky/rimsky.yml`, env-var override `RIMSKY_CONFIG`) read by all three runtime processes plus the migrate step. Declares: `persistence:` (driver + blob sub-block + retention), `named_locks:`, `claim_producers:` (alias `stores:`), `executors:`. Loader is `modeling/config/`.

## Purpose

The producer list and executor list are needed by every process. A single file eliminates drift. The unified `rimsky-entrypoint` sets only `RIMSKY_PROCESS_ROLE`; everything else is in the YAML.

## Boundaries

Owns: the file shape, validations at startup (write-semantics envelope subset, blob backend gating), the loader. Does NOT own: peer protocol shapes (those are in `protocols/`), per-feature defaults (live in code). Adjacent: `claim-producer`, `executor`, `lifecycle-subscriber`, `blob-backend`, `persistence-driver`, `write-semantics`.

## Invariants

- Single file consumed by all three processes; no per-process config files.
- `claim_producers:` is an alias for legacy `stores:`; both decode into the same struct.
- `write_semantics_envelope: [...]` is required per producer; legacy single-value `write_semantics:` accepted as one-element shortcut.
- Operator envelope ⊆ producer envelope (validated at startup).
- `RIMSKY_DB_URL` legacy env var is gone; all DSN config goes through the YAML.

## Aliases and historical names

`stores:` is the legacy YAML key (pre-v1 transition affordance).

## Open within this concept

- YAML `stores:` legacy alias of `claim_producers:` — see `tensions/yaml-stores-alias.md`.
- YAML `write_semantics:` legacy alias of `write_semantics_envelope:` — see `tensions/yaml-write-semantics-alias.md`.

