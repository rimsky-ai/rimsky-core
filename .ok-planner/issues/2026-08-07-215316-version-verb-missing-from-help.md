---
issue: version-verb-missing-from-help
kind: human
category: cli
artifacts:
  - decision:cli-verb
  - decision:doc-accuracy-gates
status: promoted
sprint: 2026-08-08-ruled-intake-drain.md
opened: 2026-08-07T21:53:16Z
github: https://github.com/rimsky-ai/rimsky-core/issues/92
---

# `rimsky version` is dispatched but missing from `rimsky help`

The CLI answers `version`, `--version`, and `-v`, and its own help text never mentions any of them. A user reading the help concludes the command doesn't exist.

The omission costs more than a typo would, because the root help function is not just what a user sees — it is the source the project's published CLI reference is generated from. A verb missing there propagates outward as a document that is quietly incomplete rather than one that is visibly wrong. This is the same failure the project already fixed once, for `auth login`.

Verified against the current tree: the dispatcher handles all three spellings before reaching its main switch, and the root usage printer never emits a line for them (`cmd/rimsky/main.go`).

## Ruling

> Generated ruling (/verify-issues): add a `version` line to the root help,
> in the same style as the other top-level command lines, naming the verb and
> both flag spellings. The project's doc-accuracy discipline requires the
> enumerating help text to match the verbs the binary actually dispatches, and
> a real verb absent from it is the exact gap that discipline exists to catch.
> Verified against the tree as it stands; nothing was applied.
