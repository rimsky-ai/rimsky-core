---
decision: services-source
status: adopted
---

# services-source

## Choice

The compose manifest schema carries an `executors:` block and a `claim_producers:` block, mirroring the corresponding blocks in `concept:rimsky-yml`. Each entry is a name → entry map where the entry carries transport (executors only), endpoint, TLS mode, declared capabilities (claim producers), protocols list, and an optional observability endpoint. The claim-producers block follows the `concept:rimsky-yml` rule that each entry carries a `write_semantics_allowed` list. The compose file is the primary source; a sibling unified-config file is a secondary source (loaded automatically when present in the manifest's directory). Publishers and named-locks blocks are not in the compose schema; they pass through from the sibling unified-config file.

## Rationale

Single-file is the cleanest shape for the simplest usage model. Mirroring the existing block shape lets the loader reuse the same validators; operators familiar with one are immediately fluent in the other.

## Alternatives

Sibling-only unified config (two-file friction). Inventing a unified `services:` namespace (departs from existing block shape).
