---
decision: image-two-stage
status: as-is
---

# Docker image structure

## Choice

`golang:1.25-alpine` for build stage; `gcr.io/distroless/static-debian12:nonroot` for runtime.

## Rationale

Minimal runtime attack surface; nonroot by default.
