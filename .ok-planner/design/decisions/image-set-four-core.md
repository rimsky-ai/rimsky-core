---
decision: image-set-four-core
status: as-is
---

# Distributed core image set

## Choice

Four core images: a base image carrying every role binary plus the shared entrypoint, a dev-friendly all-in-one variant baked with SQLite defaults so it runs zero-config, the host-agent proxy image, and the conformance-runner image.

## Rationale

Flexible deployment topology + dev-friendly all-in-one.
