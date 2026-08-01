---
decision: release-notes-template
status: as-is
---

# Notes shape

## Choice

Release notes are template-driven: a fixed section set covering breaking changes, new features, fixes, internal changes, and each distribution channel, with every entry tracing to a diff hunk.

## Rationale

A fixed section set makes an omission visible as an empty section, and diff-traceability keeps every entry grounded in an actual change — comprehensive without editorializing.

## Alternatives

- Notes derived from the commit log — rejected: commit messages describe implementation steps, not consumer-visible change.
- Free-form prose per release — rejected: nothing makes an omission detectable.
