---
decision: registry-hub-rimskyai-namespace
status: as-is
---

# Container-registry namespace

## Choice

Use a hyphenless namespace on the Docker registry, distinct from the hyphenated GitHub organization name.

## Rationale

The Docker registry disallows hyphens; the GitHub organization name is hyphenated and therefore cannot be reused verbatim.
