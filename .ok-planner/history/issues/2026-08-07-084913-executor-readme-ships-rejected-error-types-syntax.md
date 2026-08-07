---
issue: executor-readme-ships-rejected-error-types-syntax
kind: human
category: doc-drift
artifacts:
  - concept:error-policy
  - story:template-error-policy
status: repaired
opened: 2026-08-07T08:49:13Z
github: https://github.com/rimsky-ai/rimsky-core/issues/65
---

# Does examples/executor/README.md ship copyable `error_types:` syntax the validator rejects?

Yes, confirmed on the current tree: `examples/executor/README.md`'s
walkthrough (leg 3, "Declared error class routes through `error_types:`")
showed `error_types: { example/forbidden: { policy: [give_up] } }`.
`lib/foundation/spec/policy.go`'s `ErrorTypePolicy` struct has exactly two
fields, `Action` (`yaml:"action"`) and `ReasonTemplate`
(`yaml:"reason_template,omitempty"`) — there is no `policy` field. The
example's own `main_e2e_test.go:182-184` (the real end-to-end test backing
this exact walkthrough leg) already uses the correct shape,
`"error_types": {"example/forbidden": {"action": "give_up"}}`, so the
README text and the test it describes had already diverged.

`ErrorTypePolicy`'s Go struct tags are the wire/YAML shape the registration
validator enforces; there is exactly one compliant spelling
(`action: give_up`), and `main_e2e_test.go` already models it. No commitment
changed — `concept:error-policy` already documents the four-action policy
chain (`pass`, `give_up`, `retry`, `release_and_requeue`); only the
README's inline example was wrong.

**Change:** `examples/executor/README.md` — leg 3 of the cross-stack
walkthrough now reads `error_types: { example/forbidden: { action: give_up
} }`.

**Verified:** matched the corrected text against
`lib/foundation/spec/policy.go`'s `ErrorTypePolicy` struct tags and against
`examples/executor/main_e2e_test.go:182-184`'s literal `map[string]any`
construction for the same test leg — exact match. Docs-only change; no code
touched.
