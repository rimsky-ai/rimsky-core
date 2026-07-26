---
issue: story-clean-lint-vacuous-proof
kind: audit
category: proof
artifacts:
  - story:clean-lint
status: repaired
opened: 2026-07-25T21:11:30Z
---

# The clean-lint story's proof is vacuous by construction

## Question

Is `story:clean-lint`'s only annotated proof a real fitness test, or a vacuous placeholder (`test/plumbline/doc.go`, a bare package declaration nothing could redden)?

## Repair

`test/plumbline/clean_test.go::TestPlumblineClean` already existed and does exactly what the story's Proof field requires — invokes the vendored `.ok-plumbline/bin/plumbline` binary against the repo tree via `node`, fails on any non-clean exit, and asserts every check listed in `.ok-plumbline/config.json`'s `checks` map is `true` — but the `@story: clean-lint` annotation was still on the vacuous `doc.go`, not on this test. The rule (`decision:coding-style`'s proof-protection discipline: a proof artifact must carry the annotation, and a proof nothing could redden is vacuous) forced a code-side fix: moved `// @story: clean-lint` onto `TestPlumblineClean` and deleted `doc.go` (it existed solely to carry the annotation — every other file in the package already declares `package plumbline` on its own). Also hardened `assertAllChecksActive` to iterate every key in the parsed `checks` map (previously a hardcoded two-name list) so the assertion literally covers "every configured check," not just the two known today.

Verified: `PLUMBLINE_BIN=.../.ok-plumbline/bin/plumbline go test ./test/plumbline/... -count=1` passes; `go build ./...` and `go vet ./test/plumbline/...` clean.
