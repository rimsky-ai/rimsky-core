---
issue: executor-example-documents-nonexistent-go-mod
kind: human
category: developer-experience
artifacts:
  - concept:module-layout
  - story:executor-protocol
status: repaired
opened: 2026-08-07T08:49:14Z
github: https://github.com/rimsky-ai/rimsky-core/issues/66
---

# Does examples/executor/ document a per-example go.mod that does not exist, blocking the copy-and-rename onramp?

Yes, confirmed on the current tree: `examples/executor/README.md` listed
`go.mod`/`go.sum` in the directory's File layout table and instructed
"Rename the module in `go.mod`" as migration step 2. There is no `go.mod`
under `examples/executor/` — `ls` shows only `.go` files and the README
itself; the directory is part of the single shared `examples/go.mod`
workspace module (per `decision:module-split`'s Choice: "Root + the
foundation module + the protocols module + the services module + the
examples module" — one examples module, not one per example).

**Why the "add a per-example go.mod" option was not taken.** Introducing a
nested `go.mod` under `examples/executor/` would carve a new Go module
boundary out of the existing `examples` module. Unless also added to
`go.work`, Go's nested-module semantics silently exclude that directory
from `examples/go.mod`'s own package tree — dropping `examples/executor`
out of the coverage `examples/README.md`'s own "Building in-tree" section
promises (`go build ./...`, `go test ./...`, `cd examples && golangci-lint
run`). That is a structural change to `decision:module-split`'s five-module
choice with real build-gate consequences, not a doc-only fix the rules
determine on their own — so this issue took the doc-rewrite branch the
filer named as the alternative, which changes no commitment.

`examples/executor/executor.go` and `main.go` import only
`lib/protocols/proto/v1/gen` and `lib/protocols/serverkit` (stdlib +
`google.golang.org/grpc` otherwise) — confirmed by grep — matching the
README's own claim that the build-time dep is `lib/protocols` alone,
and `lib/protocols/go.mod` carries zero `replace` directives, so it
resolves via a real published version tag outside a rimsky-core checkout
(unlike the root module, which `RELEASING.md` already documents as
`go install`-incompatible for exactly this reason).

**Change:** `examples/executor/README.md` —
- File layout table: replaced the false `go.mod`/`go.sum` row with prose
  stating the directory carries no `go.mod` of its own and pointing at
  "Migrating from this example" for what a copier adds.
- Top-of-file blurb: replaced "rename the module in `go.mod`" with a
  pointer to the same section.
- "Migrating from this example": rewritten to instruct `go mod init
  <your-module-path>` followed by `go get
  github.com/rimsky-ai/rimsky-core/lib/protocols@vX.Y.Z`, explaining why
  that resolves outside this repo.

**Verified:** `go build ./...` at repo root (pass, no code touched);
confirmed via `ls examples/executor/` that no `go.mod` exists; confirmed
via grep that `executor.go`/`main.go`'s only non-stdlib imports are
`lib/protocols/proto/v1/gen`, `lib/protocols/serverkit`, and
`google.golang.org/grpc`; confirmed `lib/protocols/go.mod` has no `replace`
directives.
