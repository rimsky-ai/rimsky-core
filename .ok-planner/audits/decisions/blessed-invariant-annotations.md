---
audit: blessed-invariant-annotations
artifact: decision:blessed-invariant-annotations
determination: supported
commit: b767a27d
audited: 2026-08-02T09:44:16Z
---

# No dedicated invariant tag or numbered-invariant catalog; concepts name properties descriptively

Supported. `.ok-plumbline/config.json` configures exactly three citation
tags — `@concept:`, `@story:`, `@decision:` — with no fourth
invariant-specific tag, and a repo-wide grep found no `@blessed-invariant`
or similarly-shaped tag anywhere in source. `test/plumbline/numbered_invariant_test.go`
mechanically enforces the "no number" half project-wide: it walks every
`.go` file under the repo root (skipping `.git`, `.ok-planner`,
`node_modules`, `bin`, `tmp`) and fails on any line matching an
`invariant`/`inv` token followed by a digit, i.e. it forbids citing a
concept-doc invariant by number from code, error strings, or test
names/messages. Sampling concept docs' `## Invariants` sections (checked
`concepts/frame.md` and `concepts/dry-run.md`) confirms invariants are
named by descriptive bolded phrase (e.g. "The frame row is
cascade-immutable", "Every write is previewable"), never by number, and
code cites the owning concept via `@concept:` at the enforcement site
(467 `.go` files carry at least one `@concept:` annotation).
