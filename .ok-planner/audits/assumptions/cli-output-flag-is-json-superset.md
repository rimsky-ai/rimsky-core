---
assumption: cli-output-flag-is-json-superset
commit: d977250c
disposition: trap
synthesized: 2026-08-16T05:48:16Z
---

# `--output`/`-o` selects a format and `-o json` is interchangeable with `--json`; other formats (`yaml`, `table`) are available through it.

As operator scripting against the CLI, I would take it that `--output`/`-o` selects a format and `-o json` is interchangeable with `--json`; other formats (`yaml`, `table`) are available through it.

## Source

name-promise — `--output` and `--o` alongside `--json` in `cli-flags`

## What a run would observe

invoke a list verb with `-o json`, `-o yaml`, and `--json` and compare acceptance and payloads.

## Measured

`.ok-planner/experiments/assumption-cli-output-flag-is-json-superset` — built
for this run — settled all three claims at the parser, plus one byte
comparison on `rimsky ctx list`, which needs no server.

`-o json` and `--json` are never interchangeable, because no verb accepts
both: of 30 read verbs, 25 take `-o json` only, `auth list` takes `--json`
only, and 4 take neither. The two spellings sit on disjoint verb sets, so on
any given verb exactly one of them is a parse error.

`-o yaml` is rejected — `unknown output format "yaml" (want human|json)`.
`-o table` is worse than rejected: it is accepted and silently means human.
`ctx list -o table` returned the `-o human` rendering byte for byte, so an
operator who asks for a table gets no error and no table. `-o text` behaves
the same way. There are two formats behind the flag, and the error message
names them both.
