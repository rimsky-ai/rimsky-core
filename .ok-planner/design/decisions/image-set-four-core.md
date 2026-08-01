---
decision: image-set-four-core
status: as-is
---

# Distributed core image set

## Choice

Four core images: a base image carrying every role binary plus the shared entrypoint, a dev-friendly all-in-one variant baked with SQLite defaults so it runs zero-config, the host-agent proxy image, and the conformance-runner image.

## Rationale

Flexible deployment topology + dev-friendly all-in-one.

## Alternatives

- Per-role images for the core roles — rejected: role selection by container command on one image covers every topology without multiplying images.
- Bake the zero-config dev defaults into the base image itself — rejected: production split deployments should not carry dev defaults; layering the all-in-one variant on the base keeps the convenience without polluting it.
