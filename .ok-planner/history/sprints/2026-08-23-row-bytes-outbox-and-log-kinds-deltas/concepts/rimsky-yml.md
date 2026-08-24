---
concept: rimsky-yml
aliases:
  - unified config
---

# rimsky.yml

## What it is

The unified configuration file is the single configuration file rimsky reads (see `decision:config-format-yaml`). Every runtime process reads it, and so does the migration step, from a well-known default location. It declares the persistence a deployment uses, its named locks, and a block per service protocol naming the services that speak it; each service entry carries a protocol-membership list saying which rimsky protocols that service speaks. Tuning that belongs to one runtime role lives under that role's own section of the same file, and no process has a configuration file of its own (see `decision:launch-config-injection`). One loader parses it.

## Purpose

The unified configuration file keeps every service-orchestrating process reading the same declarations, so the producer list and the executor list cannot drift between them. The entrypoint then distinguishes only which role a process runs; everything else comes from the file.

## Boundaries

The unified configuration file owns its own shape, the validations it applies when a process loads it, the per-protocol mapping from a protocol to the proxy that fronts late-bound services for it, and the loader. Its claim-producer block is the canonical surface for declaring a claim-producer service.

It does not own the shape of any protocol a declared service speaks, which belongs to that protocol's own concept, nor a feature's defaults, which live in code. It does not own how a late-bound name resolves at dispatch, which belongs to `concept:host-daemon-proxy`, or which posture a deployment holds toward its services, which belongs to `concept:service-auth`.

see also: `claim-producer`, `executor`, `lifecycle-subscriber`, `service`, `persistence-database`, `write-semantics`, `host-daemon-proxy`, `service-auth`

## Aliases

- unified config
