---
decision: build-cgo-disabled
status: as-is
---

# CGO posture

## Choice

CGO disabled for all builds.

## Rationale

Pure-Go binaries, no C runtime dep: static cross-compilation and minimal container images without a C toolchain in the build path.

## Alternatives

- CGO-enabled builds (the C-based SQLite driver is the usual driver of this) — rejected: reintroduces a C toolchain and platform C-library dependency into every build and image; the pure-Go SQLite driver removes the need.
