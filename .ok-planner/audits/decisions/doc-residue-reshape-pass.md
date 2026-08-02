---
audit: doc-residue-reshape-pass
artifact: decision:doc-residue-reshape-pass
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:44:16Z
---

# Doc-residue comment-hygiene sites reshape into GoDoc/JSDoc form ahead of tag-or-delete

Supported. The archived sprint `.ok-planner/history/sprints/2026-06-13-plumbline-comment-hygiene-sweep-design.md`
and its completion report record this exact reshape-first rule (TD-doc-residue-reshape-pass)
being executed against all 849 doc-residue-clustered sites, landing in commit
`61e3b3b4`. Checking the current tree directly: the two `.go` files carrying
the `@plumbline:allow-docstrings` opt-in marker (`lib/graph/attribute/substitution.go`,
`lib/protocols/conformance/stubmode/stubmode.go`) are the full population of
files this rule can apply to, and every package-level declaration comment in
them — 10 checked across the two files (a package doc plus `DirectiveShape`,
`ParseDirectiveShape`, `topLevelPipeIndices` in the first; a package doc plus
`IsAsyncProbe`, `IsProbe`, `IsParkProbe`, `IsCancelProbe`, `ResponseDelta`,
`ConfirmsStub` in the second) — opens by naming the declaration and
describes what it is, exactly the shape the reshape rule prescribes. Running
the vendored `plumbline` binary against the repo root exits 0 (clean), and
`test/plumbline/clean_test.go` asserts every check named in
`.ok-plumbline/config.json`'s `checks` map is `true` (the map is currently
empty, which the test treats as both checks active), consistent with the
doc-residue cluster having reached and stayed at zero.
