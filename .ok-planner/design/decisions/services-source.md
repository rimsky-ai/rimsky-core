---
decision: services-source
status: adopted
---

# Compose manifest mirrors the unified-config service blocks

## Choice

The compose manifest schema carries an executors block and a claim-producers block mirroring the corresponding blocks in `concept:rimsky-yml` — same entry shape, same rules, including the per-entry permitted write-semantics list on claim producers. The compose file is the primary source; a sibling unified-config file is a secondary source, loaded automatically when present in the manifest's directory. Publishers and named-locks blocks are not in the compose schema; they pass through from the sibling unified-config file.

## Rationale

Single-file is the cleanest shape for the simplest usage model. Mirroring the existing block shape lets the loader reuse the same validators; operators familiar with one are immediately fluent in the other.

## Alternatives

- Sibling-only unified config — rejected: two-file friction in the simplest usage model.
- Inventing a unified services namespace for the compose schema — rejected: departs from the existing block shape and forfeits validator reuse.
