---
audit: image-tagging-version-and-channel
artifact: decision:image-tagging-version-and-channel
determination: supported
commit: b767a27d
audited: 2026-08-02T09:34:08Z
---

# Image tag scheme

Supported. `Makefile`'s `push-images` target tags every published image with both `$(REGISTRY)/<image>:$(VERSION)` (immutable, derived from `git describe`) and `$(REGISTRY)/<image>:$(LATEST_TAG)` (mutable), across all 4 core images plus all 11 bundled-service images checked in that target. `LATEST_TAG` defaults to `latest` for the formal `release` chain and `tools/dev-release.sh` invokes `LATEST_TAG=dev VERSION="${DEV_VERSION}" make release` for the dev channel, so `:latest` and `:dev` are two separate floating pointers that never collide — formal releases never move `:dev` and dev releases never move `:latest`. Local `build-image` tagging (used by `core-images`/`service-images`) separately adds the content-addressed `:$(SRC_TAG)`, which is outside this decision's scope (image identity, not version/channel).
