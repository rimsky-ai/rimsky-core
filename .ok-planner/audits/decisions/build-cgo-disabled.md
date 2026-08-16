---
audit: build-cgo-disabled
artifact: decision:build-cgo-disabled
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 23
unaccounted: 4
---

# Whether every build in the tree disables CGO

Unsupported as a universal, though every shipped artifact carries it. The population is the 23 build invocations in the tree: 18 Dockerfiles that compile Go (four core image definitions, the bundled-service definitions, and the test-only ones), the release CLI build in the goreleaser configuration, and four Makefile targets that invoke the Go toolchain directly. Every Go compile line in all 18 Dockerfiles sets the CGO-disabling environment variable — checked line by line, none missing — and so does the goreleaser build; the one Dockerfile without it compiles nothing, deriving from an already-built image. The four Makefile targets set nothing and inherit the toolchain default, so on a developer machine with a C toolchain they link against it. Nothing in the four modules imports the C pseudo-package, and the pure-Go SQLite driver removes the usual reason to need it, so the resulting binaries carry no C dependency of the project's own making; the gap is that the declared posture is not applied where the project's own build orchestrator compiles.

## Unaccounted

- The Makefile's `build` target compiles the root module with no CGO setting.
- The Makefile's `build-all` target compiles all four modules with no CGO setting.
- The Makefile's `cli` target emits the local `rimsky` binary with no CGO setting — the only one of the four that produces a distributable-shaped artifact.
- The Makefile's `build-docker` target compiles inside a container with no CGO setting.
