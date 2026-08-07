---
issue: examples-readme-signoff-validator-stale-paths
kind: human
category: doc-drift
artifacts:
  - decision:signoff-crypto-ed25519
status: answered
opened: 2026-08-06T08:07:22Z
github: https://github.com/rimsky-ai/rimsky-core/issues/52
---

# Does `examples/README.md` still document the sign-off validator at dead TypeScript paths, with the wrong signed field, and an incomplete Layout table?

No — this exact problem was already found and repaired by a prior
`/verify-issues` pass (`.ok-planner/history/issues/2026-08-06-064910-examples-readme-ts-era-signoff-section.md`,
status `repaired`), which landed before this duplicate filing was reconciled.
Re-verifying the current tree against all three of this issue's claims:

1. **Dead TypeScript paths** — gone. The "Sign-off validator" section now
   points at `lib/services/executors/claude-agent/signoff.go`
   (`BuildSignoffMessage`, `VerifyRequiredSignoffs`) and
   `testdata/signoff-wire-compat.json`; no `src/`, no `.ts` reference
   remains.
2. **Wrong signed field** — the README's `dispatch_id` phrasing was already
   correct at the time of the prior repair (the Go parameter is named
   `nodeRunID` internally, but the wire/JSON field is `dispatch_id` per
   `dispatchcontext.go`'s `json:"dispatch_id"` tag and
   `testdata/signoff-wire-compat.json`); this filed issue's claim that the
   implementation signs `node_run_id` does not match the wire contract.
3. **Incomplete Layout table** — fixed. `examples/README.md` now carries a
   second table enumerating all nine worked-example directories and seven
   root-level demo scripts against the story each demonstrates.

No further change made; this issue is a duplicate of the already-closed one.
