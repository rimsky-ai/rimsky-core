---
decision: migration-direct
---

# One-shot runs migrate in-process

## Choice

A one-shot self-hosting run calls the persistence driver's migrate operation directly, in-process, against its freshly-created SQLite database before starting any role runner — no separate migrate-binary subprocess.

## Rationale

A one-shot run owns its database top-to-bottom; the migrate-binary subprocess exists to coordinate migrations across multi-process deployments, a coordination this path does not need. Migrating in-process keeps the verb self-contained — no second process to fork, no extra runtime-environment dependencies, no extra path for failures to take.

## Alternatives

- Fork the migrate binary as a subprocess, as multi-process deployments do — rejected: buys cross-process coordination the single-process case cannot need, at the cost of a second process to fork and a second failure path.
