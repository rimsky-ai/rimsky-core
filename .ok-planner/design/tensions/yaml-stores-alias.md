---
tension: yaml-stores-alias
category: inconsistent
status: open
affects:
  - rimsky-yml
  - claim-producer
---

# YAML `stores:` is a legacy alias for `claim_producers:`; both decode into the same struct

## What is muddy

`rimsky.yml` accepts two keys for the same block:

- `claim_producers:` — the current canonical name.
- `stores:` — legacy alias; both decode into the same struct.

CLAUDE.md "Non-obvious gotchas": "YAML config: `claim_producers:` block (legacy alias `stores:`)."

This is a pre-v1 transition affordance. Two operators in two deployments may write the same content under different keys; a future YAML linter or config migration would have to handle both.

## Why it matters

Configuration drift. Documentation snippets cite one or the other inconsistently. A future reader is unsure which is authoritative without checking the loader.

## Resolution candidates (do NOT pick)

- Remove the `stores:` alias once all reference configs are updated.
- Keep both indefinitely; emit a deprecation warning when `stores:` is used.
- Lint the YAML to reject mixed usage.

## Evidence

- `_discover/2026-05-10-unified-rimsky-yml-config.md` Description and Observations.
- CLAUDE.md "Non-obvious gotchas" — claim_producers / stores alias.

