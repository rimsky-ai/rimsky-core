---
audit: image-two-stage
artifact: decision:image-two-stage
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 18
unaccounted: 0
---

# Whether every image builds on an Alpine Go stage and runs distroless-nonroot, with the CLI-spawning exception

Supported, and the exception is exactly one image. All 18 image definitions that compile Go — four core, eleven bundled services, three test-only — open with the same Alpine Go toolchain stage and end in a separate runtime stage, and every one of the 18 declares a non-root user. Sixteen of those runtime stages are the distroless static base; the exception is the Claude-agent executor, whose runtime is a glibc-based Wolfi image because its job is spawning an external CLI that needs a shell and userland, and which still adds a pinned non-root user; the one test image built on top of it inherits that base. The remaining definition compiles nothing and derives from an already-built image. A pin test asserts the two-stage, distroless, non-root shape on the three core definitions, and a second test asserts the exception image's Wolfi base, its pinned non-root uid, and its init-process entrypoint.
