---
audit: image-set-four-core
artifact: decision:image-set-four-core
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 4
unaccounted: 0
---

# Whether the core image set is the four described images, with the all-in-one layered on the base

Supported. The core build target builds exactly four images and no more, each matching its description: the base image compiles all three role binaries plus the migrate binary, the CLI, and the shared entrypoint into one image; the all-in-one is built from the base by build argument and adds only baked SQLite-default configuration and a wildcard bind, so the dev defaults sit in a layer above rather than in the base — which is what the rejected alternative says; the host-agent proxy is built from the shared Go base definition; and the conformance runner has its own. All four definitions exist on disk, the release push target pushes the same four, and a fitness test pins each image name to its definition and fails if a definition goes missing.
