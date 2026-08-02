---
audit: image-set-bundled-services
artifact: decision:image-set-bundled-services
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:57Z
---

# One image per bundled service, no combined image

Supported. The Makefile's `service-images` target builds exactly 11 images, one `docker build` call per bundled service — 2 claim producers (filesystem, postgres), 4 sensors (cron, http, object-store, webhook), 1 subscriber (openlineage), and 4 executors (http-node, verifier-http, verifier-shape-checks, claude-agent) — matching one-to-one against the 11 non-test `Dockerfile*` files found under `lib/services/{claim_producers,executors,sensors,subscribers}` (population enumerated by filesystem search, excluding the 4 additional Dockerfiles under `lib/services/test/`). No combined-services Dockerfile or image tag exists anywhere in the tree.
