---
issue: proto-files-carry-no-spdx-identifier
kind: human
category: licensing
artifacts:
  - decision:licensing-enforced-by-license-lint
status: repaired
opened: 2026-08-07T08:49:21Z
github: https://github.com/rimsky-ai/rimsky-core/issues/74
---

# Do the ten Apache .proto files carry no SPDX identifier, invisibly to the license lint?

Yes, confirmed on the current tree: `apacheHeaderProto` in
`tools/license-check/headers.go` was still the one stamper template
emitting the retired prose form (`// Licensed under the Apache License,
Version 2.0.`) instead of an `SPDX-License-Identifier:` line, and all ten
shipped `.proto` files carried it. `detectHeader`'s `markerApache = "Apache
License"` substring check accepted the prose phrase as a valid Apache
marker, so `make license-lint` passed without ever seeing the gap.

`COPYRIGHT` is the binding text (`COPYING.md` names it as such) and already
committed every Apache-layer file — no per-kind exception named — to being
"identified per-file by an `SPDX-License-Identifier: Apache-2.0` header."
The proto template was the one stamper out of step with that existing
commitment, so the rules left exactly one compliant end state: bring
`apacheHeaderProto` to the same SPDX form every other kind already uses.
No commitment changed.

**Change:**
- `tools/license-check/headers.go`: `apacheHeaderProto` now emits `//
  SPDX-License-Identifier: Apache-2.0` instead of the prose sentence
  (`agplHeaderProto` was already SPDX-form and untouched).
- Restamped the ten `.proto` files under `lib/protocols/proto/v1/` to the
  new header.
- Ran `make proto-gen` to regenerate the nineteen `.pb.go`/`_grpc.pb.go`
  bindings under `lib/protocols/proto/v1/gen/` (their leading comment is
  copied from the source `.proto`'s file-level comment) — a header-only,
  one-line-per-file diff.

**Verified:** `go build ./tools/license-check/... && go test
./tools/license-check/...` (pass), `go build ./... ` at repo root (pass),
`cd lib/protocols && go build ./... && go test ./...` (pass), `make
license-lint` (0 violations, 150 apache / 1610 agpl files), `golangci-lint
run ./tools/license-check/...` (clean).
