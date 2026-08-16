---
experiment: clean-lint
commit: PENDING
---

# The Plumbline lint over the whole tree, with both checks demonstrably live

## What it ran against

The repository checkout itself. `run.py` runs the lint binary the repository
vendors at `.ok-plumbline/bin/plumbline` — the same binary the CI workflow points
`PLUMBLINE_BIN` at — over the repository root, reads
`.ok-plumbline/config.json`, and then runs the same binary over three throwaway
fixture trees it builds under a temporary directory, each carrying a copy of the
repository's own lint configuration and one seeded file. It needs `node` on
PATH and touches nothing in the repository. Re-run unchanged at this tree.

## What was observed

Three legs, seven checks, none failing.

The vendored lint binary is present and executable. The configuration switches
off no check, and declares five citation tags: `@concept:`, `@story:`,
`@decision:`, `@subject:`, `@practice:`.

Run over the repository root, the lint exited 0 with no output.

Both checks fire under that same configuration. A fixture carrying a stray Go
comment exited 2 with `plumbline/comment-hygiene`. A fixture carrying
`// @concept: no-such-concept` exited 2 with `plumbline/citation-unresolved`.
A third fixture carrying `// @concept: node` against a concept file that exists
exited 0, so the citation check discriminates rather than always failing.
