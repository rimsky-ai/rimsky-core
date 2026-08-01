---
decision: image-two-stage
status: as-is
---

# Docker image structure

## Choice

An Alpine-based Go toolchain image for the build stage; a distroless static base image running as a non-root user for the runtime stage, except for a runtime that must spawn an external CLI needing a real userland (a shell, git) — that runtime stage uses a non-distroless glibc-based base instead, still running as a non-root user.

## Rationale

Minimal runtime attack surface; nonroot by default. The glibc exception exists only where the runtime's whole job is spawning an external CLI binary that itself depends on a shell and standard userland tools no static distroless base provides; nonroot and a minimized package set still apply there.

## Alternatives

- Single-stage images carrying the Go toolchain into the runtime — rejected: ships a compiler and package tooling as attack surface for no runtime benefit.
- A full-distro runtime base everywhere — rejected: pays the userland attack surface universally when only the CLI-spawning runtime needs it.
