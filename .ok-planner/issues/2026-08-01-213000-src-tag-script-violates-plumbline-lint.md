---
issue: src-tag-script-violates-plumbline-lint
kind: human
category: other
artifacts:
  - decision:coding-style
status: open
opened: 2026-08-01T21:30:00Z
---

# The suite-owned src-tag script fails the suite's own lint — ignore entry papers over it

## Problem

`tools/image-src-tag.sh` is ok-workspaces' canonical script — suite-owned, refreshed on every `/ok` converge, marked do-not-hand-edit. The v14.1.0 copy fails the v14.1.0 plumbline lint twice: its prose header comments are not recognized machine directives, and it carries `@decision: content-addressed-src-tag`, a citation that resolves to no artifact in this project's decision catalog. Because the file cannot be hand-edited (the next converge overwrites it), this session added it to `.ok-plumbline/config.json`'s `ignore` list so the repo-wide clean gate passes — a paper-over, not a fix. The mismatch is the suite's to resolve: either the script ships lint-clean (and cites nothing, or ships the decision it cites), or the suite's own converge writes the ignore entry.

## Candidates

- Report upstream to ok-plugins: make the ok-workspaces payload's script lint-clean under ok-plumbline's own rules (and drop or satisfy its `@decision:` citation), then remove the project-side ignore entry.
- Keep the project-side ignore entry permanently and record it as the sanctioned boundary for suite-owned files.
- Author a project decision `content-addressed-src-tag` so the citation resolves — only right if the owner wants the src-tag scheme recorded as a project decision rather than suite machinery.
