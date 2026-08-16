---
audit: release-semver-sha-dot-joined
artifact: decision:release-semver-sha-dot-joined
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:47:25Z
---

# Whether the pre-release commit hash rides the pre-release segment rather than build metadata

Supported. The dev-release script composes its version by dot-joining the build date and the short commit hash into the pre-release segment after a dev marker, and the build-metadata separator appears nowhere in it. That single derived string is then used as the repository tag, the module tag, the published package version, and the image version tag, which is exactly why the separator cannot be used: the same string has to satisfy the image-tag, package-manager, and module grammars at once. A fitness test asserts the dot-joined form and fails if the hash is ever appended as build metadata in either spelling.
