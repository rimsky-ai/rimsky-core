---
experiment: rules-doc-accuracy
commit: PENDING
---

# Every path the contributor rules cite, resolved against the checkout

## What it ran against

The repository checkout itself, through the artifact a contributor reads:
`.claude/rules/rules.md`. `run.py` extracts every backtick-quoted token from
that file, keeps the ones in a filesystem-path shape, and stats each against the
repository root. The shape rule is the one this project recognises: drop a
leading `./`; reject URLs, `make …` invocations, and tokens carrying `*` or
`{`; accept a trailing `/`; otherwise require a repository file extension and
either a `/` or a root-level filename shape. The Search Scoping line, which
lists paths to exclude from searches rather than paths to read, is dropped
before scanning. `run.py` also parses the Makefile for declared targets and asks
git whether the rules file is tracked.

## What was observed

Four legs, six checks, none failing.

Ten cited paths were in scope, and all ten resolve: `lib/services/test/`,
`CLAUDE.md`, `tools/gotest-guard.sh`, `lib/protocols/proto/v1/events.proto`,
`.golangci.yml`, `./CLAUDE.md`, `.ok-planner/sketches/`, `.ok-planner/issues/`,
`.ok-planner/workbench/`, `.claude/rules/citation-grammar.md`.

None of the four curated dead references appears in the file:
`deploy/build-images.sh`, `deploy/docker-compose.yml`, ``​`executors/claude-agent``,
`docs/2026-04-25-stores-redesign.md`. That set is the one this repository
curates.

All four make targets the rules name — `core-images`, `lint`, `proto-gen`,
`service-images` — are declared in the Makefile, and the file names
`make core-images` as the image-rebuild step. The rules file is tracked by git,
so every contributor's checkout carries the file that was measured.

The probe was repaired once before it passed. Its first shape rule admitted the
Go package patterns `./test/scenarios/...` and the bare filename
`plumbline-cheatsheet.md`, and it stripped leading dots off tokens such as
`.golangci.yml` before stating them. Neither the patterns nor the bare filename
is a filesystem-path citation, and the dot-stripping was a defect in the probe.
