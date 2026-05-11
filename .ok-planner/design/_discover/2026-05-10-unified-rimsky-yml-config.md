---
topic: unified-rimsky-yml-config
kind: choice
---

# Single `rimsky.yml` declares persistence, named-locks, claim-producers, executors

## Description

A multi-process orchestration platform typically picks one of: per-process env vars (12-factor), per-process config files, or a single shared config file consumed by every process. Rimsky chose the third.

A single YAML file (default `/etc/rimsky/rimsky.yml`, env-var override `RIMSKY_CONFIG`) declares the operator-side configuration in one place:

- **`persistence:`** — driver (`postgres` or `sqlite`), DSN (postgres) or path (sqlite), `blob:` sub-block (backend + spill threshold + retention), retention windows.
- **`named_locks:`** — per-name limits.
- **`claim_producers:`** (alias: `stores:`) — peer addresses, optional `protocols: [claim_producer, lifecycle_subscriber]`, required `write_semantics_envelope: [...]`.
- **`executors:`** — peer addresses, transport (`grpc` or `http+json`), kind, accepted node types, optional `observability_endpoint`.

Reference config: `deploy/rimsky.yml`. The unified-image variant `deploy/rimsky-all.yml` keeps the same shape. `modeling/config/` is the loader; `foundation/persistence/blob_config.go` is the typed struct for the persistence.blob sub-block.

The same file is read by all three runtime processes plus the migrate step. The unified-image entrypoint sets only `RIMSKY_PROCESS_ROLE` and lets the children read the same YAML — no process-specific env-var lists exist.

The `claim_producers:` alias for `stores:` is visible in the loader — both keys decode into the same struct. This preserves the legacy spelling transitionally without a separate config schema. CLAUDE.md "Non-obvious gotchas" calls out the alias: "YAML config: `claim_producers:` block (legacy alias `stores:`)."

Per-producer `write_semantics_envelope` is declared in YAML and validated at startup against each producer's advertised envelope (see `2026-05-10-write-semantics-envelope-handshake`). The legacy single-value `write_semantics:` is accepted as a single-element envelope shortcut. Operator's declared envelope MUST be ⊆ producer's advertised envelope.

`RIMSKY_DB_URL` and similar legacy env vars are gone (CLAUDE.md "Non-obvious gotchas"). All DSN-bearing fields route through the YAML. The transition is enforced by the absence of env-var reads in `modeling/config/` and `foundation/persistence/` for these fields.

Alternative considered: per-process config files. Not chosen — the producer list and executor list are needed by scheduler, supervisor, and control-api equally; duplicating them invites drift.

Alternative considered: env-var-only configuration. Not chosen — `claim_producers` and `executors` are lists with structured entries (protocols, envelopes), which don't translate cleanly to env vars.

## Code surface

- `deploy/rimsky.yml` — reference config.
- `deploy/rimsky-all.yml` — unified-image variant.
- `modeling/config/` — entire loader package.
- `foundation/persistence/blob_config.go` — blob sub-block.
- `foundation/locks/types.go:154-161` — write-semantics-envelope subset validation at startup.
- `cmd/rimsky-entrypoint/main.go` — sets `RIMSKY_PROCESS_ROLE` only.

## Prose surface

- `CLAUDE.md` "Reference deployment & local stack" — `rimsky.yml` is the unified config.
- `CLAUDE.md` "Non-obvious gotchas" — `RIMSKY_DB_URL` removed, alias `claim_producers/stores`, `protocols: [...]` lists.
- `docs/protocols/*.md` — config snippets per protocol.

## Adjacent topics

- `2026-05-10-postgres-only-runtime-state` — three processes read the same file.
- `2026-05-10-lifecycle-subscriber-opt-in` — `protocols: [...]` is set here.
- `2026-05-10-write-semantics-envelope-handshake` — envelope subset validation.
- `2026-05-10-blob-spill-pluggable-backends` — `persistence.blob:` sub-block.
- `2026-05-10-pre-v1-break-freely-migrations` — YAML config shape pre-v1.

## Observations

- The `claim_producers` / `stores` alias is a pre-v1 transition affordance. CLAUDE.md's "Vocabulary" section uses `claim producer` as the canonical name; `stores/` is the directory name for the bundled binaries. The YAML accepts both for now.
- The `write_semantics_envelope` strict-subset check fires at startup, so a misconfigured deployment fails fast. The check compares operator-config against the producer's `Capabilities()` response — a producer that's unreachable at startup blocks the check.
- The Helm chart at `deploy/kubernetes/rimsky-chart/` renders this YAML via a values file; CLAUDE.md notes the chart "may lag behind binary env-var renames."
- `protocols: [claim_producer]` is the default for a peer entry; declaring `[claim_producer, lifecycle_subscriber]` opts the peer into lifecycle fan-out. The opt-in is per-peer, not per-deployment.
