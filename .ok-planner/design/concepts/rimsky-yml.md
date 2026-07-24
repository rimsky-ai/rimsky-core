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

Owns: the file shape, validations at startup (write-semantics-allowed subset, blob backend gating), a per-protocol late-bind-proxy mapping (protocol → the proxy service that fronts late-bound services for that protocol), the loader. Does NOT own: service protocol shapes (those are the protocol concepts' territory), per-feature defaults (live in code). Adjacent: `claim-producer`, `executor`, `lifecycle-subscriber`, `service`, `blob-backend`, `persistence-database`, `write-semantics`, `host-agent-proxy`, `peer-auth`.

## Invariants

- Single file consumed by every runtime process; no per-process config files.
- The claim-producer registry is the canonical surface for declaring claim-producer services.
- Each declared producer must enumerate the write-semantics it is allowed to use.
- An operator-declared producer's allowed write-semantics MUST be a subset of the producer's advertised set (validated at startup).
- All DSN config goes through the YAML.
- Each service entry declares its protocol membership via an explicit protocol-membership list.
- **No API-key ledger state in the YAML.** The ledger's data — principals, keys, active-status — is the sole source of truth for auth state, never mirrored or overridden by YAML (see `concept:anonymous-mode`). Transport-level peer-auth posture is a separate concern and IS a legal YAML key; see `concept:peer-auth`.
- **Env-reference expansion is strict.** The loader expands env references in the YAML at load time; unset referenced variables are a load-time hard error rather than an empty-string substitution. The specific reference syntax and the single-implementation commitment (root loader, sibling-block re-reads, and service opts loaders share one implementation) live in `decision:config-yaml-loading-policy`.
- **Strict YAML decoding.** Loaders decode strictly: any unknown key — typo, guess, stale example, retired key — fails at load with the offending key named. No per-retired-key compat shim (retired stuff is purely removed per `decision:pre-v1-pure-removal-for-retired-surfaces`). The specific parser API used lives in `decision:config-yaml-loading-policy`.
- Late-bound service names resolve at dispatch via the proxy named for the relevant protocol in the per-protocol late-bind-proxy mapping; an empty mapping leaves late-bind resolution inert. See `concept:host-agent-proxy`.
