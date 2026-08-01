---
decision: registry-hub-rimskyai-namespace
status: as-is
---

# Container-registry namespace

## Choice

Images publish under the hyphenless `rimskyai` namespace on Docker Hub, distinct from the hyphenated `rimsky-ai` GitHub organization name.

## Rationale

Docker Hub's namespace grammar disallows hyphens, so the GitHub organization name cannot be reused verbatim; dropping the hyphen keeps the two identities as close as the grammar permits.

## Alternatives

- Publishing under a registry whose namespace grammar admits the hyphenated organization name (e.g. GHCR) — rejected: Docker Hub is where consumers look for images by default.
- An unrelated namespace string — rejected: gratuitous divergence from the organization identity.
