---
audit: build-cgo-disabled
artifact: decision:build-cgo-disabled
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:39:17Z
---

# CGO is disabled for every Go build in the repo

Supported. Every Go-build invocation in the tree sets `CGO_ENABLED=0`: checked all 24 `Dockerfile*`/`Dockerfile.example` files under `dockerfiles/`, `examples/`, and `lib/services/` that run `go build` or `go test -c`, plus `.goreleaser.yaml`'s CLI build — all 24 carry `CGO_ENABLED=0`, none omit it. A root-module test (`test/plumbline/build_chain_test.go::TestImagesAreTwoStageStaticNonRoot`) additionally asserts this for the three core-image Dockerfiles as a standing check. `modernc.org/sqlite` (pure-Go) is the SQLite driver, consistent with the rationale that a pure-Go driver removes the CGO dependency the choice describes rejecting.
