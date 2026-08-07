---
issue: copyright-copying-scope-contradicts-licensing-yml
kind: audit
category: compliance
artifacts: []
status: repaired
opened: 2026-08-06T06:49:12Z
---

# Do `COPYRIGHT` and `COPYING.md` still scope the Apache layer narrower than the enforced `licensing.yml` and the SPDX headers?

Yes, confirmed on the current tree, and `licensing.yml` (the enforced,
machine-checked source of truth per `decision:licensing-enforced-by-license-lint`)
already settles the question: its `apache:` list includes both
`lib/protocols/` and `examples/` (labeled "Edge carve-out — not the wire
contract, but deliberately permissive"), and `decision:licensing-dual-apache-agpl`
independently confirms "a permissive open-source license covers the
protocols module and the examples module." `COPYRIGHT` and `COPYING.md`'s
"One bright line" section named only `lib/protocols/`, contradicting both.
Separately, `tools/license-check/main.go` runs four verifications
(`verifyHeaders`, `verifyImports`, `verifyEntriesExist`,
`verifyApacheClosure`); `COPYING.md`'s prose enumerated only the first
three, omitting `verifyApacheClosure` (the Apache-module third-party
dependency-closure license check).

Rule: align the stale prose to the commitment `licensing.yml`, the SPDX
headers, and the decision corpus already agree on — no commitment changed,
only the two prose files brought into line with the enforced source they
themselves say controls in case of conflict ("Where this guide and those
files differ, those files control" — but `COPYRIGHT` is one of the
controlling files, so it needed the same fix).

Repaired:
- `COPYRIGHT`: the embedder-layer (Apache) description now includes the
  `examples/` carve-out alongside `lib/protocols/`.
- `COPYING.md`: the "One bright line" Apache bullet now names the
  `examples/` carve-out; the verification-count prose now names all four
  checks (adding the third-party dependency-closure check).

Verified: `go run ./tools/license-check` reports "143 apache files, 1609
agpl files, 0 violations" against the now-consistent prose (the tool itself
was already correct; only the prose lagged it).
