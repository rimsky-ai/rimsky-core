---
story: portable-template-across-modes
status: as-is
---

# The same template file runs in both modes without edits

## Story

As a rimsky user (a developer promoting a locally-authored template to production, or an operator adopting a template someone else authored), I can use the same template file in both modes — all-in-one and multi-container — without editing it, so that there's no dev-vs-prod template dialect and locally-working templates are directly promotable.

The template's node config, its structure, and its referenced service kinds are byte-identical across modes. What differs between modes is what belongs to modes — the rimsky.yml naming external service endpoints (containerized) or its absence (all-in-one), and per-service env vars set on the appropriate process (all-in-one process env vs container env).

No dev-vs-prod template dialect; locally-working templates promote directly to production.
