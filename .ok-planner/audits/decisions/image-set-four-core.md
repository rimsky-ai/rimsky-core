---
audit: image-set-four-core
artifact: decision:image-set-four-core
determination: supported
commit: b767a27d
audited: 2026-08-02T09:34:08Z
---

# Distributed core image set

Supported. `Makefile`'s `core-images` target builds exactly 4 images, matching the decision's 4 named images by role: `rimsky` (`dockerfiles/Dockerfile.rimsky`, all four role binaries plus `rimsky-entrypoint` under one distroless base), `rimsky-all-in-one` (`dockerfiles/Dockerfile.all-in-one`, built `FROM rimsky:$(VERSION)` with SQLite defaults baked on top rather than into the base image), `rimsky-host-agent-proxy` (`dockerfiles/Dockerfile.go-base`), and `rimsky-conformance` (`dockerfiles/Dockerfile.conformance`). No fifth core image or per-role core image exists in the Makefile's `core-images`/`push-images` targets or under `dockerfiles/`, and the all-in-one image layers on top of the base via `ARG RIMSKY_BASE` rather than baking dev defaults into `Dockerfile.rimsky` itself, matching both rejected alternatives in the decision.
