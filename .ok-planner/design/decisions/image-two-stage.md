---
decision: image-two-stage
status: as-is
---

# Docker image structure

## Choice

An Alpine-based Go toolchain image for the build stage; a distroless static base image running as a non-root user for the runtime stage.

## Rationale

Minimal runtime attack surface; nonroot by default.
