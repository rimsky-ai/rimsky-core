---
issue: dependency-advisories-crypto-grpc
kind: human
category: security
artifacts: []
status: repaired
opened: 2026-08-05T01:23:54Z
github: https://github.com/rimsky-ai/rimsky-core/issues/42
---

# golang.org/x/crypto and google.golang.org/grpc carried open security advisories

Re-verified on the current tree (well past v0.13.0, at v0.14.0 plus dev
commits): the pins had not moved — all five workspace modules still carried
`golang.org/x/crypto v0.51.0` and `google.golang.org/grpc v1.80.0`, matching
the filed evidence exactly.

**Rule that determined the fix.** Not a design question — a mechanical
dependency bump with a fully specified target (`golang.org/x/crypto@v0.52.0`,
`google.golang.org/grpc@v1.82.1`) and no behavior the project commits to
changing. `decisions/release-scan-docker-scout.md`'s choice ("Docker Scout
scans every locally-built image ... failing the release on any unaddressed
critical or high severity finding") and the project's general pre-v1
"break freely" posture (`.claude/rules/rules.md`) both point the same
direction: clear the advisories now rather than carry them.

**What changed.** Ran `go get golang.org/x/crypto@v0.52.0
google.golang.org/grpc@v1.82.1 && go mod tidy` in each of the five workspace
modules (root, `examples/`, `lib/foundation/`, `lib/protocols/`,
`lib/services/`; `lib/protocols/go.mod` has no direct or indirect
`golang.org/x/crypto` dependency, so only the grpc bump applied there).
Transitive `go.opentelemetry.io/otel*` packages and `google.golang.org/genproto`
moved along with the grpc bump. All five `go.mod`/`go.sum` pairs updated.

**Verified.** `go build ./...` and `go test ./...` (module-scoped, per module)
pass across all five modules; `make lint` clean.
