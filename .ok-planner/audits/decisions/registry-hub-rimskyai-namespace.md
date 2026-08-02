---
audit: registry-hub-rimskyai-namespace
artifact: decision:registry-hub-rimskyai-namespace
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:34:08Z
---

# Container-registry namespace

Supported. `Makefile` sets `REGISTRY ?= docker.io/rimskyai`, with an adjacent comment stating Docker Hub's namespace grammar disallows hyphens and that this is why the hyphenated GitHub organization name (`rimsky-ai`) is not reused verbatim; `push-images` publishes every one of the 15 core + bundled-service images under this `REGISTRY` value. The same `docker.io/rimskyai/...` form is used consistently outside the Makefile: `RELEASING.md`, the `/release` skill, `dockerfiles/all-in-one.rimsky.yml`, `Dockerfile.all-in-one`'s usage comment, and all 11 committed `releases/v*.md` release notes checked reference `docker.io/rimskyai`. No file in the repo publishes or documents images under a hyphenated `rimsky-ai` Docker Hub namespace or any other registry namespace.
