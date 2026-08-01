---
decision: project-agnostic
status: as-is
---

# Consumer neutrality

## Choice

No code/doc/test/example/comment names or assumes a specific consumer; templates use generic names.

## Rationale

Rimsky ships as an embedded platform to many consumers; any consumer-specific vocabulary baked into the tree becomes foreign residue in every other embedding.

## Alternatives

- Consumer-specific terminology and fixtures in templates, examples, and tests — rejected: couples the platform to one consumer and forces every other embedding to read around vocabulary that means nothing to it.
