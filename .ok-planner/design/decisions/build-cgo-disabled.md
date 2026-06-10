---
decision: build-cgo-disabled
status: as-is
---

# CGO posture

## Choice

`CGO_ENABLED=0` for all builds.

## Rationale

Pure-Go binaries, no C runtime dep.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
