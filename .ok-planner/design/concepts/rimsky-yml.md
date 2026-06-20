---
concept: rimsky-yml
status: as-is
aliases:
  - unified config
---

# rimsky.yml

## What it is

A single YAML file at a well-known default path read by every runtime process plus the migrate step. Declares the persistence, named-locks, and per-protocol service blocks needed by every runtime process. Each service entry has an optional protocol-membership list declaring which rimsky protocols the binary speaks. A single config loader parses it.

## Purpose

The producer list and executor list are needed by every service-orchestrating process. A single file eliminates drift. The unified entrypoint distinguishes only the process role; everything else is in the YAML.

## Boundaries

Owns: the file shape, validations at startup (write-semantics-allowed subset, blob backend gating), a per-protocol late-bind-proxy mapping (protocol → the proxy service that fronts late-bound services for that protocol), the loader. Does NOT own: service protocol shapes (those are the protocol concepts' territory), per-feature defaults (live in code). Adjacent: `claim-producer`, `executor`, `lifecycle-subscriber`, `service`, `blob-backend`, `persistence-database`, `write-semantics`, `host-agent-proxy`.

## Invariants

- Single file consumed by every runtime process; no per-process config files.
- The claim-producer registry is the canonical surface for declaring claim-producer services.
- Each declared producer must enumerate the write-semantics it is allowed to use.
- An operator-declared producer's allowed write-semantics MUST be a subset of the producer's advertised set (validated at startup).
- All DSN config goes through the YAML.
- Each service entry declares its protocol membership via an explicit protocol-membership list.
- **No auth-related keys.** Auth state is data-derived (the active-status predicate over the persisted API-key ledger; see `concept:anonymous-mode`). Operators do not configure an auth mode, a bootstrap key, or any other auth knob in the yml file. The data state of the API-key ledger is the sole source of truth.
- Late-bound service names resolve at dispatch via the proxy named for the relevant protocol in the per-protocol late-bind-proxy mapping; an empty mapping leaves late-bind resolution inert. See `concept:host-agent-proxy`.
