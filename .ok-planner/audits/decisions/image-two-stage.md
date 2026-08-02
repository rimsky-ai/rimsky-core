---
audit: image-two-stage
artifact: decision:image-two-stage
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:34:08Z
---

# Docker image structure

Supported. Checked all 14 shipped-image Dockerfiles that define their own build stage — the 3 core Dockerfiles with a build stage (`dockerfiles/Dockerfile.rimsky`, `Dockerfile.go-base`, `Dockerfile.conformance`; `Dockerfile.all-in-one` FROMs the already-built `rimsky` image and adds no stage of its own) plus the 11 `lib/services/**/Dockerfile*` files driving `make service-images` (2 claim producers, 4 sensors, 4 executors, 1 subscriber). All 14 use a `golang:1.25-alpine` build stage. 13 of the 14 run their final stage on `gcr.io/distroless/static` or `gcr.io/distroless/static-debian12:nonroot`, as `nonroot`/UID 65532. The one exception, `lib/services/executors/claude-agent/Dockerfile`, runs its final stage on a pinned `cgr.io/chainguard/wolfi-base` (glibc) image instead, with an in-file comment stating the exact reason the decision names — the executor's job is spawning the `claude` CLI, which needs a shell and `git` — and still creates and runs as an unprivileged `nonroot` user. No Dockerfile in the checked population carries the Go toolchain into its runtime stage, and no Dockerfile uses a full-distro base outside this one documented exception.
