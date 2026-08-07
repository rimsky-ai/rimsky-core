---
issue: examples-readme-ts-era-signoff-section
kind: audit
category: doc-drift
artifacts: []
status: repaired
opened: 2026-08-06T06:49:10Z
---

# Does `examples/README.md` still point at a dead TypeScript path, a wrong signed field, and an incomplete Layout table?

Two of the three filed claims held on re-verification; one did not.

1. **Dead TypeScript path — confirmed and repaired.** `lib/services/executors/claude-agent/` is flat Go (no `src/`, no `.ts` files, per `decision:implementation-language-go-plus-ts`); the cited `signoff-validator/reference-validator.ts` and `signoff-test-signer.ts` do not exist anywhere in the tree (grepped repo-wide). There is no separate copyable validator package to point at anymore.
2. **Signed field — NOT confirmed; the filed claim was wrong.** `signoff.go`'s `BuildSignoffMessage(nodeRunID string, ...)` uses a Go parameter literally named `nodeRunID`, but the *wire* term is `dispatch_id`: `dispatchcontext.go`'s `NodeRunID` field carries `json:"dispatch_id"`, the `testdata/signoff-wire-compat.json` cross-implementation test vectors key on `"dispatch_id"`, and `decision:signoff-crypto-ed25519` itself says "the dispatch id." The Go identifier is an internal rename; the wire contract a validator implements is still `dispatch_id`. No change made here — the README's `dispatch_id` phrasing was already correct.
3. **Incomplete Layout table — confirmed and repaired.** The directory carries nine worked-example directories (`fanout-any-source/`, `fanout-fs-expand-folder/`, `fanout-fs-list-array/`, `fanout-pg-list-array/`, `fanout-intent-inheritance/`, `inproc-loop-counter/`, `messages-as-nodes/`, `park-resume/`, `sub-claim-payload/`) plus seven root-level demo scripts, none listed in the old Layout table, each carrying its own `@story:` citation naming the story it demonstrates.

Repaired in `examples/README.md`:
- Rewrote the "Sign-off validator" section to point at the real Go artifacts — `signoff.go` (`BuildSignoffMessage`, `VerifyRequiredSignoffs`) and `testdata/signoff-wire-compat.json` — kept the `dispatch_id` wire-field name (confirmed correct, see above), and replaced the dead `signoff-test-signer.ts` reference with the actual in-tree equivalent (the unexported `testSigner` helper in `signoff_test.go`).
- Added a second Layout table enumerating the worked-example directories and demo scripts against the story each demonstrates (read from each file's own `@story:` annotation).

Verified: the cited Go symbols (`BuildSignoffMessage`, `VerifyRequiredSignoffs`, `SignoffDomain`) and files (`testdata/signoff-wire-compat.json`, `signoff_test.go`) exist as described; `go build ./...` and `go test ./lib/services/executors/claude-agent/...` pass (docs-only change in a sibling module, no behavior touched).
