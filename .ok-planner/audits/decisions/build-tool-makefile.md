---
audit: build-tool-makefile
artifact: decision:build-tool-makefile
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:17Z
---

# The root Makefile is the single build-orchestration entry point

Supported. The repo-root `Makefile` declares 39 `.PHONY` targets covering build, the five per-module test slices, lint, license-lint, all core and service image builds, image scanning, and the full release chain; no parallel task runner (Mage, Task, Just) or ad-hoc shell-script build path exists outside it. The one non-Makefile build tool present, `goreleaser` (`.goreleaser.yaml`, CGO-disabled CLI archives), is itself invoked only through Makefile targets (`cli-check`, `cli-snapshot`, and the release/publish targets), so it composes with rather than bypasses the Makefile.
