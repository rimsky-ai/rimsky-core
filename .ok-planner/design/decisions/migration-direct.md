---
decision: migration-direct
status: adopted
---

# migration-direct

## Choice

The verb calls the persistence driver's migrate operation directly against the freshly-created sqlite database before starting any role runner. No separate migrate-binary subprocess.

## Rationale

A one-shot run owns its database top-to-bottom; the existing migrate-binary subprocess exists to coordinate migrations across multi-process deployments, a coordination this verb does not need. Migrating in-process keeps the verb self-contained — no second process to fork, no extra runtime-environment dependencies, no extra path for failures to take.
