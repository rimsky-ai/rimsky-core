---
concept: rimsky-yml
status: as-is
aliases:
  - unified config
---

# rimsky.yml

## What it is

A single YAML file at a well-known default path (overridable by a config-path environment variable) read by all three runtime processes plus the migrate step. Declares: a persistence block (driver + blob sub-block + retention), a named-locks block, a claim-producers block, an executors block, and a publishers block. Each service entry has an optional protocol-membership list (defaulting to claim-producer only) declaring which rimsky protocols the binary speaks. A single config loader parses it.

## Purpose

The producer list and executor list are needed by every service-orchestrating process. A single file eliminates drift. The unified entrypoint sets only a process-role environment variable; everything else is in the YAML.

## Boundaries

Owns: the file shape, validations at startup (write-semantics-allowed subset, blob backend gating), a per-protocol `late_bind_service_proxies` map (protocol name → the proxy service name that fronts late-bound services for that protocol), the loader. Does NOT own: service protocol shapes (those are the protocol concepts' territory), per-feature defaults (live in code). Adjacent: `claim-producer`, `executor`, `lifecycle-subscriber`, `service`, `blob-backend`, `persistence-database`, `write-semantics`, `host-agent-proxy`.

## Invariants

- Single file consumed by all three processes; no per-process config files.
- `claim_producers:` is the canonical key.
- `write_semantics_allowed: [...]` is required per producer.
- Operator-declared `write_semantics_allowed` MUST be ⊆ producer-advertised set (validated at startup).
- All DSN config goes through the YAML.
- Each service entry declares its protocol membership via an explicit protocol-membership list.
- **No auth-related keys.** Auth state is data-derived (the active-status predicate over the persisted API-key ledger; see `concept:anonymous-mode`). Operators do not configure an auth mode, a bootstrap key, or any other auth knob in the yml file. The data state of the API-key ledger is the sole source of truth.
- Late-bound service names resolve at dispatch via the proxy named for the relevant protocol in the per-protocol `late_bind_service_proxies` map; an empty map leaves late-bind resolution inert. See `concept:host-agent-proxy`.
